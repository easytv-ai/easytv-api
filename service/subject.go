package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"easytv-api/model"
)

// 主体库（F2）：角色 / 场景 / 道具的参考图与描述，画布任务与策划案引用它保持一致性。

// TeamIdOf 用户所属团队（不在团队里返回 0）。
func TeamIdOf(userId int) int {
	m, err := model.GetTeamMembership(userId)
	if err != nil || m == nil {
		return 0
	}
	return m.TeamId
}

// subjectViewPrompts 三视图 / 表情表的生成角度。
// 不要写「角色设定图」「三连」这类词：模型会画成海报拼贴，三张图就成了三个不同的人。
var subjectViewPrompts = map[string][]struct{ Label, Suffix string }{
	model.SubjectKindCharacter: {
		{"正面", "正面全身，站姿，只画这一个角色，干净纯色背景，不要文字，不要拼贴，不要多个人"},
		{"侧面", "左侧面全身，站姿，只画这一个角色，干净纯色背景，不要文字，不要拼贴，不要多个人"},
		{"表情", "同一张脸并排画出喜悦、平静、愤怒三种表情，五官和发型一致，干净纯色背景，不要文字，不要换成别的人"},
	},
	model.SubjectKindScene: {
		{"全景", "全景，环境氛围完整，无人物，不要文字"},
		{"中景", "中景，主要陈设清晰，无人物，不要文字"},
		{"细节", "局部细节特写，材质与光线清晰，无人物，不要文字"},
	},
	model.SubjectKindProp: {
		{"正面", "正面，居中，只画这一个道具，干净纯色背景，不要文字"},
		{"侧面", "45 度侧面，只画这一个道具，干净纯色背景，不要文字"},
		{"细节", "局部细节特写，材质清晰，不要文字"},
	},
}

// subjectImageModel 主体参考图优先用能吃参考图的图片模型，侧面 / 表情才能锁住正面那个人。
// 调用方指定的模型本身支持参考图时沿用；否则取定价表里第一个支持的。都没有时退回原模型，三张同时文生图。
func subjectImageModel(preferred string) (string, bool) {
	if preferred != "" && ImageInputModeOf(preferred) != model.ImageInputNone && FindProvider(model.TaskTypeImage, preferred) != nil {
		return preferred, true
	}
	prices, _ := model.GetModelPrices(true)
	for _, p := range prices {
		if p.Type != model.TaskTypeImage || p.EffectiveImageInput() == model.ImageInputNone {
			continue
		}
		if FindProvider(model.TaskTypeImage, p.Model) != nil {
			return p.Model, true
		}
	}
	if preferred == "" {
		preferred = PickDefaultModel(model.TaskTypeImage)
	}
	return preferred, preferred != "" && ImageInputModeOf(preferred) != model.ImageInputNone
}

func subjectPrompt(look, suffix string, withRef bool) string {
	prompt := fmt.Sprintf("%s，%s", strings.TrimSpace(look), suffix)
	if withRef {
		prompt += "。严格沿用参考图中的同一个主体，五官、发型、体型、服装、配色保持一致，不要重新设计，不要换成别人"
	}
	return prompt
}

func subjectNodePrefix(nodeId string) (string, bool) {
	if !strings.HasPrefix(nodeId, "subject-") {
		return "", false
	}
	rest := strings.TrimPrefix(nodeId, "subject-")
	i := strings.Index(rest, "-")
	if i <= 0 {
		return "", false
	}
	return "subject-" + rest[:i] + "-", true
}

func subjectWaitNode(params string) string {
	var m map[string]any
	if json.Unmarshal([]byte(params), &m) != nil {
		return ""
	}
	s, _ := m["subject_wait"].(string)
	return s
}

// GenerateSubjectViews 按 look 建三张参考图任务，完成后由 SyncSubjectRefs 回写。
// teamId 为请求空间（API.md 20）：团队空间扣团队积分池，产物进团队资产库。
// 模型能接收参考图时，先出第一张（正面 / 全景），另外两张先挂起；第一张成功后再带上它的图提交，避免三张各画各的人。
func GenerateSubjectViews(userId, teamId int, s *model.Subject, modelName string) ([]*model.GenerationTask, error) {
	if strings.TrimSpace(s.Look) == "" {
		return nil, errors.New("请先填写外观描述")
	}
	modelName, canRef := subjectImageModel(modelName)
	if modelName == "" {
		return nil, errors.New("暂无可用的图片模型")
	}
	provider := FindProvider(model.TaskTypeImage, modelName)
	if provider == nil {
		return nil, fmt.Errorf("模型 %s 暂未接入", modelName)
	}
	views := subjectViewPrompts[s.Kind]
	if len(views) == 0 {
		views = subjectViewPrompts[model.SubjectKindCharacter]
	}
	if err := CanUseTeamPool(userId, teamId); err != nil {
		return nil, err
	}
	payer, err := PayerForTeam(userId, teamId)
	if err != nil {
		return nil, err
	}
	isMember := IsMemberForPricing(userId, teamId)
	unit, _, err := EstimateTaskPoints(model.TaskTypeImage, modelName, `{"count":1}`, isMember)
	if err != nil {
		return nil, errors.New("图片模型未定价")
	}
	if unit*len(views) > 0 && model.GetPointsBalance(nil, payer) < unit*len(views) {
		return nil, fmt.Errorf("积分不足，生成 %d 张参考图需要 %d 积分", len(views), unit*len(views))
	}
	out := []*model.GenerationTask{}
	anchorNode := ""
	for i, v := range views {
		withRef := canRef && anchorNode != ""
		params := `{"count":1}`
		if withRef {
			b, _ := json.Marshal(map[string]any{"count": 1, "subject_wait": anchorNode})
			params = string(b)
		}
		task := &model.GenerationTask{
			UserId: userId, TeamId: teamId, Type: model.TaskTypeImage, Provider: provider.Name(), Model: modelName,
			Prompt: subjectPrompt(s.Look, v.Suffix, withRef), Params: params, Inputs: "[]",
			NodeId: fmt.Sprintf("subject-%d-%s", s.Id, v.Label),
		}
		if err := task.Insert(); err != nil {
			continue
		}
		if err := FreezeTaskPoints(task, unit); err != nil {
			_ = model.SetTaskFailed(task.Id, err.Error())
			continue
		}
		task.PointsFrozen = unit
		if withRef {
			// 等第一张成功后再提交，前端仍能轮询到这张，状态是排队中
			out = append(out, task)
			continue
		}
		if err := provider.Submit(task); err != nil {
			_ = FailTask(task.Id, err.Error())
			continue
		}
		if i == 0 {
			anchorNode = task.NodeId
		}
		out = append(out, task)
	}
	if len(out) == 0 {
		return nil, errors.New("参考图生成失败，请稍后重试")
	}
	Track(userId, "subject_generate", map[string]any{"subject_id": s.Id, "tasks": len(out)})
	return out, nil
}

// ContinueSubjectViews 第一张参考图成功后，把挂起的侧面 / 表情带上这张图提交。
func ContinueSubjectViews(taskId int) {
	t, err := model.GetTaskById(taskId)
	if err != nil || t.Status != model.TaskStatusSucceeded || t.ResultAssetId <= 0 {
		return
	}
	releaseSubjectViews(t, true)
}

// AbortWaitingSubjectViews 第一张失败或取消时，把还在排队的后续视图退掉，避免空等超时。
func AbortWaitingSubjectViews(t *model.GenerationTask) {
	if t == nil {
		return
	}
	releaseSubjectViews(t, false)
}

func releaseSubjectViews(anchor *model.GenerationTask, submit bool) {
	prefix, ok := subjectNodePrefix(anchor.NodeId)
	if !ok {
		return
	}
	var waiting []*model.GenerationTask
	model.DB.Where("user_id = ? AND status = ? AND node_id LIKE ?", anchor.UserId, model.TaskStatusPending, prefix+"%").Find(&waiting)
	inputs := "[]"
	if submit {
		b, _ := json.Marshal([]map[string]any{{"asset_id": anchor.ResultAssetId, "role": "image"}})
		inputs = string(b)
	}
	for _, w := range waiting {
		if subjectWaitNode(w.Params) != anchor.NodeId {
			continue
		}
		if !submit {
			_ = FailTask(w.Id, "参考图基准生成失败，后续视图已取消")
			continue
		}
		res := model.DB.Model(&model.GenerationTask{}).Where("id = ? AND status = ?", w.Id, model.TaskStatusPending).Update("inputs", inputs)
		if res.Error != nil || res.RowsAffected == 0 {
			continue
		}
		w.Inputs = inputs
		provider := FindProvider(w.Type, w.Model)
		if provider == nil {
			_ = FailTask(w.Id, "图片模型暂未接入")
			continue
		}
		if err := provider.Submit(w); err != nil {
			_ = FailTask(w.Id, err.Error())
		}
	}
}

// ResumeSubjectViews 进程在第一张完成、后续还没提交时重启的补偿。超时扫描之前跑，避免挂起的视图被判超时。
func ResumeSubjectViews() {
	var waiting []*model.GenerationTask
	model.DB.Where("status = ? AND node_id LIKE ? AND params LIKE ?", model.TaskStatusPending, "subject-%", "%subject_wait%").
		Limit(100).Find(&waiting)
	seen := map[string]bool{}
	for _, w := range waiting {
		node := subjectWaitNode(w.Params)
		key := fmt.Sprintf("%d:%s", w.UserId, node)
		if node == "" || seen[key] {
			continue
		}
		seen[key] = true
		var anchor model.GenerationTask
		err := model.DB.Where("user_id = ? AND node_id = ?", w.UserId, node).Order("id desc").First(&anchor).Error
		if err != nil {
			continue
		}
		switch anchor.Status {
		case model.TaskStatusSucceeded:
			ContinueSubjectViews(anchor.Id)
		case model.TaskStatusFailed, model.TaskStatusCancelled:
			AbortWaitingSubjectViews(&anchor)
		}
	}
}

// SyncSubjectRefs 把 subject-<id>-<label> 的已完成任务结果回写到主体参考图。
func SyncSubjectRefs(userId int, s *model.Subject) []model.SubjectRef {
	prefix := fmt.Sprintf("subject-%d-", s.Id)
	var tasks []*model.GenerationTask
	model.DB.Where("user_id = ? AND node_id LIKE ? AND status = ?", userId, prefix+"%", model.TaskStatusSucceeded).
		Order("id asc").Find(&tasks)
	refs := s.ParseRefs()
	seen := map[int]bool{}
	for _, r := range refs {
		seen[r.AssetId] = true
	}
	changed := false
	for _, t := range tasks {
		if t.ResultAssetId <= 0 {
			continue
		}
		label := strings.TrimPrefix(t.NodeId, prefix)
		replaced := false
		for i := range refs {
			if label == "" || refs[i].Label != label {
				continue
			}
			replaced = true
			if refs[i].AssetId == t.ResultAssetId {
				break
			}
			delete(seen, refs[i].AssetId)
			refs[i] = model.SubjectRef{AssetId: t.ResultAssetId, Url: t.ResultUrl, Label: label}
			seen[t.ResultAssetId] = true
			changed = true
			break
		}
		if replaced || seen[t.ResultAssetId] {
			continue
		}
		refs = append(refs, model.SubjectRef{AssetId: t.ResultAssetId, Url: t.ResultUrl, Label: label})
		seen[t.ResultAssetId] = true
		changed = true
	}
	if changed {
		_ = s.SetRefs(refs)
	}
	return refs
}

// ApplySubjectsToTask 把主体的参考图并入 inputs、描述追加到提示词末尾。
// 返回新的 prompt 与 inputs JSON；主体不可用时忽略。
// versions 为锁定的版本（主体 id → 版本号，API.md 14.2）：锁定且快照存在时按快照内容生成，否则用最新内容。
func ApplySubjectsToTask(userId, teamId int, ids []int, prompt, inputsJSON string, versions map[int]int) (string, string, []int) {
	subjects := model.GetSubjectsByIds(userId, teamId, ids)
	// 引用丢失（API.md 17）：主体已删除 / 不可见，或主体参考图的素材没了
	distinct := map[int]bool{}
	for _, id := range ids {
		if id > 0 {
			distinct[id] = true
		}
	}
	missing := len(distinct) - len(subjects)
	defer func() {
		if missing > 0 {
			Track(userId, model.TrackEventRefMissing, map[string]any{"source": "subject", "count": missing})
		}
	}()
	if len(subjects) == 0 {
		return prompt, inputsJSON, nil
	}
	for i, sub := range subjects {
		if v := versions[sub.Id]; v > 0 && v != sub.Version {
			if snap, err := model.GetSubjectVersion(sub.Id, v); err == nil {
				subjects[i] = snap.AsSubject(sub)
			}
		}
	}
	var inputs []map[string]any
	if inputsJSON != "" {
		_ = json.Unmarshal([]byte(inputsJSON), &inputs)
	}
	if inputs == nil {
		inputs = []map[string]any{}
	}
	used := []int{}
	extra := []string{}
	for _, s := range subjects {
		// 参考图角色：主体图不能占首帧（旧版视频接口把 image、以及没有首帧时的第一张 reference 当首帧），
		// 用专门的 subject 角色，视频映射成 reference、图片任务当参考图
		role := "subject"
		switch s.Kind {
		case model.SubjectKindCharacter:
			role = "character"
		case model.SubjectKindScene:
			role = "scene"
		}
		refs := s.ParseRefs()
		if len(refs) == 0 && s.CoverAssetId > 0 {
			refs = []model.SubjectRef{{AssetId: s.CoverAssetId, Url: s.CoverUrl}}
		}
		added := 0
		for _, r := range refs {
			if r.AssetId <= 0 || added >= 2 {
				continue
			}
			if _, err := model.GetUserAsset(s.UserId, r.AssetId); err != nil {
				missing++
				continue
			}
			inputs = append(inputs, map[string]any{"asset_id": r.AssetId, "role": role})
			added++
		}
		desc := strings.TrimSpace(s.Prompt)
		if desc == "" {
			desc = strings.TrimSpace(s.Look)
		}
		if desc != "" {
			extra = append(extra, fmt.Sprintf("%s：%s", s.Name, desc))
		}
		used = append(used, s.Id)
	}
	if len(extra) > 0 {
		prompt = strings.TrimSpace(prompt)
		if prompt != "" {
			prompt += "。"
		}
		prompt += strings.Join(extra, "；")
	}
	b, _ := json.Marshal(inputs)
	return prompt, string(b), used
}

// CanAccessProject 项目可见性（API.md 10.10 / 20）：作者本人，或项目在团队空间（team_id>0）且用户是该团队成员。
// 队友的个人项目不再可见。项目不存在与无权访问都返回 false，调用方统一回「项目不存在或无权访问」，不区分两者。
func CanAccessProject(userId, projectId int) (*model.Project, bool) {
	if userId <= 0 || projectId <= 0 {
		return nil, false
	}
	p, err := model.GetProjectById(projectId)
	if err != nil {
		return nil, false
	}
	if p.UserId == userId {
		return p, true
	}
	if p.TeamId <= 0 {
		return nil, false
	}
	if m := membershipOf(userId); m == nil || m.TeamId != p.TeamId {
		return nil, false
	}
	return p, true
}
