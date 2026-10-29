package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

// 服务端批量编排（API.md 13）：前端把要生成的节点连同依赖一次交给服务端，worker 每 5 秒推进：
// 同步已提交任务的结果 → 上游失败的下游标跳过 → 依赖都就绪的按顺序提交（并发满就等下一轮，超预算 / 积分不足就暂停）。

// BatchItemRequest 创建批次时的一个节点。
type BatchItemRequest struct {
	NodeId  string              `json:"node_id"`
	Layer   string              `json:"layer"`
	Type    string              `json:"type"`
	Model   string              `json:"model"`
	Prompt  string              `json:"prompt"`
	Params  json.RawMessage     `json:"params"`
	Inputs  []BatchInput        `json:"inputs"`
	Depends []model.BatchDepend `json:"depends"`
}

// BatchInput 静态输入素材。
type BatchInput struct {
	AssetId int    `json:"asset_id"`
	Role    string `json:"role"`
}

// BatchRequest POST /user/canvas/:id/batch。
type BatchRequest struct {
	BudgetPoints int                `json:"budget_points"`
	Items        []BatchItemRequest `json:"items"`
}

// CreateBatch 校验并创建批次：同一画布同时只能有一个进行中的批次；依赖必须指向本批次节点或画布里已有产物的节点，且不能成环。
// teamId 为批次所在空间（API.md 20），worker 提交的每个任务都沿用它扣费。
func CreateBatch(userId, teamId int, cv *model.Canvas, req BatchRequest) (*model.GenerationBatch, error) {
	if err := CanUseTeamPool(userId, teamId); err != nil {
		return nil, err
	}
	if _, err := PayerForTeam(userId, teamId); err != nil {
		return nil, err
	}
	if len(req.Items) == 0 {
		return nil, errors.New("没有要生成的节点")
	}
	if len(req.Items) > model.BatchMaxItems {
		return nil, fmt.Errorf("一次最多批量生成 %d 个节点", model.BatchMaxItems)
	}
	if _, err := model.GetActiveBatchByCanvas(userId, cv.Id); err == nil {
		return nil, errors.New("这张画布已有进行中的批量生成，请等它完成或先取消")
	}
	g, err := parseComposeGraph(cv)
	if err != nil {
		return nil, err
	}
	graphNodes := map[string]composeGraphNode{}
	for _, n := range g.Nodes {
		graphNodes[n.Id] = n
	}
	inBatch := map[string]*BatchItemRequest{}
	for i := range req.Items {
		it := &req.Items[i]
		it.NodeId = strings.TrimSpace(it.NodeId)
		if _, ok := graphNodes[it.NodeId]; !ok {
			return nil, fmt.Errorf("画布里没有节点 %s", it.NodeId)
		}
		if inBatch[it.NodeId] != nil {
			return nil, fmt.Errorf("节点 %s 重复", it.NodeId)
		}
		inBatch[it.NodeId] = it
	}
	isMember := IsMemberForPricing(userId, teamId)
	items := make([]*model.GenerationBatchItem, 0, len(req.Items))
	estimate := 0
	for i := range req.Items {
		it := &req.Items[i]
		if !model.IsValidTaskType(it.Type) {
			return nil, fmt.Errorf("节点 %s 的任务类型无效", it.NodeId)
		}
		if strings.TrimSpace(it.Prompt) == "" {
			return nil, fmt.Errorf("节点 %s 缺少提示词", it.NodeId)
		}
		if FindProvider(it.Type, it.Model) == nil {
			return nil, fmt.Errorf("模型 %s 暂未接入", it.Model)
		}
		params := "{}"
		if len(it.Params) > 0 {
			var probe map[string]any
			if json.Unmarshal(it.Params, &probe) != nil {
				return nil, fmt.Errorf("节点 %s 的 params 必须是 JSON 对象", it.NodeId)
			}
			params = string(it.Params)
		}
		// 免积分标记只能由服务端写：批量条目也不能自带 params.agent（复核发现 high）。
		// 条目 params 会原样落库、由 worker 经 batchSubmitter 交给 StartTask，
		// 不挡的话自带 free=true 的批量任务在 ChatFreeEnabled=false 时也免单（跳过估价与预扣）。
		if err := CheckNoClientAgentParams(params); err != nil {
			return nil, fmt.Errorf("节点 %s：%w", it.NodeId, err)
		}
		if TaskResultCount(params) > 1 {
			return nil, errors.New("一次只能生成 1 个结果，需要多个请分多次提交")
		}
		for _, in := range it.Inputs {
			if _, err := model.GetUserAsset(userId, in.AssetId); err != nil {
				return nil, fmt.Errorf("输入素材 %d 不存在", in.AssetId)
			}
		}
		for _, dep := range it.Depends {
			if inBatch[dep.NodeId] != nil {
				continue
			}
			n, ok := graphNodes[dep.NodeId]
			if !ok || toInt(n.Data["assetId"]) <= 0 {
				return nil, fmt.Errorf("节点 %s 依赖的 %s 还没有产物，请把它也加进批量生成", it.NodeId, dep.NodeId)
			}
		}
		points, _, err := EstimateTaskPoints(it.Type, it.Model, params, isMember)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return nil, fmt.Errorf("模型 %s 未定价，请联系运营", it.Model)
			}
			return nil, err
		}
		estimate += points
		inputs, _ := json.Marshal(it.Inputs)
		depends, _ := json.Marshal(it.Depends)
		items = append(items, &model.GenerationBatchItem{
			NodeId: it.NodeId, Layer: truncate(it.Layer, 20), Type: it.Type, Model: it.Model, Prompt: it.Prompt,
			Params: params, Inputs: string(inputs), Depends: string(depends), Status: model.BatchItemWaiting, Estimate: points,
		})
	}
	if cycle := batchCycle(req.Items); cycle != "" {
		return nil, fmt.Errorf("节点依赖成环：%s", cycle)
	}
	// 审批门禁（API.md 26.1）：静态输入在创建时就能判定，直接标 blocked（不用等 worker），依赖产物的条目留给 worker 提交前再查
	if gate := CanvasReviewGate(cv.Id); gate != ReviewGateOff {
		reviews, _ := model.GetShotReviews(cv.Id)
		blockStaticItemsByGate(items, gate, reviews)
	}
	budget := req.BudgetPoints
	if budget < 0 {
		budget = 0
	}
	b := &model.GenerationBatch{UserId: userId, TeamId: teamId, CanvasId: cv.Id, ProjectId: cv.ProjectId, Status: model.BatchRunning, BudgetPoints: budget, EstimatePoints: estimate}
	b.WorkerTag = common.WorkerTag()
	if err := model.CreateBatch(b, items); err != nil {
		return nil, err
	}
	b.Items = items
	Track(userId, "batch_start", map[string]any{"batch_id": b.Id, "canvas_id": cv.Id, "items": len(items), "estimate": estimate, "budget": budget})
	return b, nil
}

// batchCycle 依赖成环时返回环上的一个节点。
func batchCycle(items []BatchItemRequest) string {
	deps := map[string][]string{}
	for _, it := range items {
		for _, d := range it.Depends {
			deps[it.NodeId] = append(deps[it.NodeId], d.NodeId)
		}
	}
	state := map[string]int{} // 0 未访问 1 访问中 2 完成
	var visit func(string) string
	visit = func(id string) string {
		switch state[id] {
		case 1:
			return id
		case 2:
			return ""
		}
		state[id] = 1
		for _, d := range deps[id] {
			if c := visit(d); c != "" {
				return c
			}
		}
		state[id] = 2
		return ""
	}
	for _, it := range items {
		if c := visit(it.NodeId); c != "" {
			return c
		}
	}
	return ""
}

// BatchView 批次 + 条目。
func BatchView(b *model.GenerationBatch) *model.GenerationBatch {
	items, _ := model.GetBatchItems(b.Id)
	b.Items = items
	return b
}

// PauseBatch / ResumeBatch / CancelBatch / RetryFailedBatch 用户操作。
func PauseBatch(b *model.GenerationBatch) error {
	if !model.TransitBatch(b.Id, []string{model.BatchRunning}, map[string]any{"status": model.BatchPaused, "pause_reason": "已手动暂停"}) {
		return model.ErrBatchStatus
	}
	return nil
}

func ResumeBatch(b *model.GenerationBatch) error {
	if !model.TransitBatch(b.Id, []string{model.BatchPaused}, map[string]any{"status": model.BatchRunning, "pause_reason": ""}) {
		return model.ErrBatchStatus
	}
	return nil
}

// CancelBatch 取消：没提交的直接取消；已提交还在跑的任务一并取消并退回积分。
func CancelBatch(b *model.GenerationBatch) error {
	if !model.TransitBatch(b.Id, []string{model.BatchRunning, model.BatchPaused}, map[string]any{"status": model.BatchCancelled, "finished_at": common.GetTimestamp()}) {
		return model.ErrBatchStatus
	}
	model.SetBatchItemsStatus(b.Id, []string{model.BatchItemWaiting}, map[string]any{"status": model.BatchItemCancelled})
	items, _ := model.GetBatchItems(b.Id)
	for _, it := range items {
		if it.Status != model.BatchItemSubmitted {
			continue
		}
		t, err := model.GetTaskById(it.TaskId)
		if err != nil {
			continue
		}
		switch t.Status {
		case model.TaskStatusPending, model.TaskStatusRunning:
			_ = CancelTask(b.UserId, it.TaskId)
			model.UpdateBatchItem(it.Id, []string{model.BatchItemSubmitted}, map[string]any{"status": model.BatchItemCancelled, "error": "批量生成已取消"})
		case model.TaskStatusSucceeded:
			// 取消前已经跑完的保留结果（worker 不再处理已取消的批次，这里顺手同步）
			model.UpdateBatchItem(it.Id, []string{model.BatchItemSubmitted}, map[string]any{"status": model.BatchItemSucceeded, "asset_id": t.ResultAssetId})
		default:
			model.UpdateBatchItem(it.Id, []string{model.BatchItemSubmitted}, map[string]any{"status": model.BatchItemFailed, "error": truncate(firstNonEmptyString(t.ErrorMsg, "生成失败"), 500)})
		}
	}
	refreshBatchCounters(b.Id)
	return nil
}

// RetryFailedBatch 只重跑失败和被跳过的节点，批次回到进行中。
func RetryFailedBatch(b *model.GenerationBatch) (int64, error) {
	if b.Status == model.BatchCancelled {
		return 0, model.ErrBatchStatus
	}
	if _, err := model.GetActiveBatchByCanvas(b.UserId, b.CanvasId); err == nil && b.Status == model.BatchDone {
		return 0, errors.New("这张画布已有进行中的批量生成")
	}
	n := model.SetBatchItemsStatus(b.Id, []string{model.BatchItemFailed, model.BatchItemSkipped, model.BatchItemBlocked}, map[string]any{"status": model.BatchItemWaiting, "error": "", "task_id": 0})
	if n == 0 {
		return 0, errors.New("没有失败的节点")
	}
	model.TransitBatch(b.Id, []string{model.BatchDone, model.BatchPaused, model.BatchRunning}, map[string]any{"status": model.BatchRunning, "pause_reason": "", "finished_at": 0})
	refreshBatchCounters(b.Id)
	return n, nil
}

func refreshBatchCounters(batchId int) {
	items, _ := model.GetBatchItems(batchId)
	succeeded, failed := 0, 0
	for _, it := range items {
		switch it.Status {
		case model.BatchItemSucceeded:
			succeeded++
		case model.BatchItemFailed, model.BatchItemSkipped, model.BatchItemBlocked:
			failed++
		}
	}
	model.UpdateBatchCounters(batchId, map[string]any{"succeeded": succeeded, "failed": failed})
}

// RunBatchWorker 推进所有进行中 / 暂停中的批次（暂停中只同步结果不提交）。main.go 每 5 秒调用。
func RunBatchWorker() {
	list, err := model.GetRunningOrPausedBatches(100)
	if err != nil {
		common.SysError("batch: query batches failed: " + err.Error())
		return
	}
	for _, b := range list {
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("batch %d panic: %v", b.Id, r))
				}
			}()
			advanceBatch(b)
		}()
	}
}

// batchSubmitter 提交一个任务（测试可替换）。
var batchSubmitter = func(task *model.GenerationTask) (*model.GenerationTask, error) {
	provider := FindProvider(task.Type, task.Model)
	if provider == nil {
		return nil, fmt.Errorf("模型 %s 暂未接入", task.Model)
	}
	task.Provider = provider.Name()
	return StartTask(task, provider)
}

func advanceBatch(b *model.GenerationBatch) {
	items, err := model.GetBatchItems(b.Id)
	if err != nil {
		return
	}
	byNode := map[string]*model.GenerationBatchItem{}
	for _, it := range items {
		byNode[it.NodeId] = it
	}

	// 1. 同步已提交任务的结果
	for _, it := range items {
		if it.Status != model.BatchItemSubmitted {
			continue
		}
		t, err := model.GetTaskById(it.TaskId)
		if err != nil {
			continue
		}
		switch t.Status {
		case model.TaskStatusSucceeded:
			if model.UpdateBatchItem(it.Id, []string{model.BatchItemSubmitted}, map[string]any{"status": model.BatchItemSucceeded, "asset_id": t.ResultAssetId}) {
				it.Status, it.AssetId = model.BatchItemSucceeded, t.ResultAssetId
			}
		case model.TaskStatusFailed, model.TaskStatusCancelled:
			msg := firstNonEmptyString(t.ErrorMsg, "生成失败")
			if model.UpdateBatchItem(it.Id, []string{model.BatchItemSubmitted}, map[string]any{"status": model.BatchItemFailed, "error": truncate(msg, 500)}) {
				it.Status, it.Error = model.BatchItemFailed, msg
			}
		}
	}

	// 2. 上游失败 / 跳过 / 取消的，下游标跳过（可能级联，循环到稳定）
	for changed := true; changed; {
		changed = false
		for _, it := range items {
			if it.Status != model.BatchItemWaiting {
				continue
			}
			for _, d := range it.ParseDepends() {
				dep := byNode[d.NodeId]
				if dep != nil && (dep.Status == model.BatchItemFailed || dep.Status == model.BatchItemSkipped || dep.Status == model.BatchItemCancelled || dep.Status == model.BatchItemBlocked) {
					msg := fmt.Sprintf("上游节点 %s 没有生成成功", d.NodeId)
					if model.UpdateBatchItem(it.Id, []string{model.BatchItemWaiting}, map[string]any{"status": model.BatchItemSkipped, "error": msg}) {
						it.Status, it.Error = model.BatchItemSkipped, msg
						changed = true
					}
					break
				}
			}
		}
	}

	// 3. 提交依赖都就绪的节点
	spent := b.SpentPoints
	if b.Status == model.BatchRunning {
		var graphNodes map[string]composeGraphNode
		// 审批门禁（API.md 26.1）：门禁开着时读一次结论，视频条目提交前校验
		gate := CanvasReviewGate(b.CanvasId)
		var reviews map[int]*model.ShotReview
		if gate != ReviewGateOff {
			reviews, _ = model.GetShotReviews(b.CanvasId)
		}
		for _, it := range items {
			if it.Status != model.BatchItemWaiting {
				continue
			}
			inputs, ready := batchInputs(b, it, byNode, &graphNodes)
			if !ready {
				continue
			}
			if gate != ReviewGateOff && it.Type == model.TaskTypeVideo {
				gi := make([]GateInput, 0, len(inputs))
				for _, in := range inputs {
					gi = append(gi, GateInput{AssetId: in.AssetId, Role: in.Role})
				}
				if err := ReviewGateCheck(gate, gi, reviews); err != nil {
					if model.UpdateBatchItem(it.Id, []string{model.BatchItemWaiting}, map[string]any{"status": model.BatchItemBlocked, "error": truncate(err.Error(), 500)}) {
						it.Status, it.Error = model.BatchItemBlocked, err.Error()
					}
					continue
				}
			}
			if b.BudgetPoints > 0 && spent+it.Estimate > b.BudgetPoints {
				model.TransitBatch(b.Id, []string{model.BatchRunning}, map[string]any{"status": model.BatchPaused, "pause_reason": fmt.Sprintf("已用 %d 积分，再提交会超过预算 %d 积分", spent, b.BudgetPoints)})
				break
			}
			inputsJSON, _ := json.Marshal(inputs)
			task := &model.GenerationTask{
				UserId: b.UserId, TeamId: b.TeamId, ProjectId: b.ProjectId, CanvasId: b.CanvasId, NodeId: it.NodeId,
				Type: it.Type, Model: it.Model, Prompt: it.Prompt, Params: it.Params, Inputs: string(inputsJSON),
			}
			started, err := batchSubmitter(task)
			if err != nil {
				var full *ErrConcurrencyFull
				if errors.As(err, &full) {
					break // 并发满：下一轮再提交
				}
				var insufficient model.ErrInsufficientPoints
				// 积分不足、团队版过期、已不在团队 / 成了查看者：后面的节点也一样提交不了，整批暂停等用户处理
				if errors.As(err, &insufficient) || errors.Is(err, ErrTeamEditionExpired) || errors.Is(err, model.ErrNotTeamMember) || errors.Is(err, ErrTeamViewer) {
					model.TransitBatch(b.Id, []string{model.BatchRunning}, map[string]any{"status": model.BatchPaused, "pause_reason": err.Error()})
					break
				}
				model.UpdateBatchItem(it.Id, []string{model.BatchItemWaiting}, map[string]any{"status": model.BatchItemFailed, "error": truncate(err.Error(), 500), "attempts": it.Attempts + 1})
				it.Status = model.BatchItemFailed
				continue
			}
			spent += started.PointsFrozen
			status := model.BatchItemSubmitted
			fields := map[string]any{"status": status, "task_id": started.Id, "attempts": it.Attempts + 1}
			if started.Status == model.TaskStatusFailed {
				status = model.BatchItemFailed
				fields["status"], fields["error"] = status, truncate(started.ErrorMsg, 500)
			}
			if model.UpdateBatchItem(it.Id, []string{model.BatchItemWaiting}, fields) {
				it.Status, it.TaskId = status, started.Id
			}
		}
	}

	// 4. 计数与收尾
	succeeded, failed, pending := 0, 0, 0
	for _, it := range items {
		switch it.Status {
		case model.BatchItemSucceeded:
			succeeded++
		case model.BatchItemFailed, model.BatchItemSkipped:
			failed++
		case model.BatchItemWaiting, model.BatchItemSubmitted:
			pending++
		}
	}
	model.UpdateBatchCounters(b.Id, map[string]any{"succeeded": succeeded, "failed": failed, "spent_points": spent})
	if pending == 0 && model.TransitBatch(b.Id, []string{model.BatchRunning, model.BatchPaused}, map[string]any{"status": model.BatchDone, "pause_reason": "", "finished_at": common.GetTimestamp()}) {
		Track(b.UserId, "batch_done", map[string]any{"batch_id": b.Id, "canvas_id": b.CanvasId, "succeeded": succeeded, "failed": failed, "spent": spent})
		title := "批量生成完成"
		body := fmt.Sprintf("成功 %d 个", succeeded)
		if failed > 0 {
			body += fmt.Sprintf("，失败 %d 个，可在向导里只重跑失败的节点", failed)
		}
		model.PushNotification(b.UserId, model.NotificationTypeTask, title, body, fmt.Sprintf("/canvas?id=%d", b.CanvasId))
	}
}

// batchInputs 依赖都就绪时返回完整输入（静态输入 + 依赖节点产物）；有依赖还没好返回 ready=false。
// 依赖不在本批次的，取画布里该节点当前的产物（创建时已校验存在）。
func batchInputs(b *model.GenerationBatch, it *model.GenerationBatchItem, byNode map[string]*model.GenerationBatchItem, graphNodes *map[string]composeGraphNode) ([]BatchInput, bool) {
	var inputs []BatchInput
	_ = json.Unmarshal([]byte(it.Inputs), &inputs)
	seen := map[int]bool{}
	for _, in := range inputs {
		seen[in.AssetId] = true
	}
	for _, d := range it.ParseDepends() {
		assetId := 0
		if dep := byNode[d.NodeId]; dep != nil {
			if dep.Status != model.BatchItemSucceeded {
				return nil, false
			}
			assetId = dep.AssetId
		} else {
			if *graphNodes == nil {
				*graphNodes = map[string]composeGraphNode{}
				if cv, err := model.GetUserCanvas(b.UserId, b.CanvasId); err == nil {
					if g, err := parseComposeGraph(cv); err == nil {
						for _, n := range g.Nodes {
							(*graphNodes)[n.Id] = n
						}
					}
				}
			}
			assetId = toInt((*graphNodes)[d.NodeId].Data["assetId"])
		}
		if assetId > 0 && !seen[assetId] && d.Role != "" {
			seen[assetId] = true
			inputs = append(inputs, BatchInput{AssetId: assetId, Role: d.Role})
		}
	}
	return inputs, true
}

// blockStaticItemsByGate 视频条目的静态输入（不依赖本批次产物的 first_frame 等）过一遍门禁，拦下的直接标 blocked。
func blockStaticItemsByGate(items []*model.GenerationBatchItem, gate string, reviews map[int]*model.ShotReview) {
	for _, it := range items {
		if it.Type != model.TaskTypeVideo || it.Status != model.BatchItemWaiting {
			continue
		}
		var inputs []BatchInput
		_ = json.Unmarshal([]byte(it.Inputs), &inputs)
		if len(inputs) == 0 {
			continue
		}
		gi := make([]GateInput, 0, len(inputs))
		for _, in := range inputs {
			gi = append(gi, GateInput{AssetId: in.AssetId, Role: in.Role})
		}
		if err := ReviewGateCheck(gate, gi, reviews); err != nil {
			it.Status, it.Error = model.BatchItemBlocked, truncate(err.Error(), 500)
		}
	}
}
