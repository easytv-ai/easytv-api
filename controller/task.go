package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// ===== 生成任务 =====

// taskInput 任务输入素材项。
type taskInput struct {
	AssetId int    `json:"asset_id"`
	Role    string `json:"role"`
}

// taskView 任务响应视图（params/inputs 以 JSON 返回）。
func taskView(t *model.GenerationTask) map[string]any {
	params := t.Params
	if params == "" {
		params = "{}"
	}
	inputs := t.Inputs
	if inputs == "" {
		inputs = "[]"
	}
	return map[string]any{
		"id":              t.Id,
		"task_no":         t.TaskNo,
		"user_id":         t.UserId,
		"project_id":      t.ProjectId,
		"canvas_id":       t.CanvasId,
		"node_id":         t.NodeId,
		"skill_id":        t.SkillId,
		"parent_task_id":  t.ParentTaskId,
		"retake_of":       t.RetakeOf,
		"lang":            t.Lang,
		"type":            t.Type,
		"provider":        t.Provider,
		"model":           t.Model,
		"prompt":          t.Prompt,
		"params":          json.RawMessage(params),
		"inputs":          json.RawMessage(inputs),
		"status":          t.Status,
		"progress":        t.Progress,
		"result_asset_id": t.ResultAssetId,
		"result_url":      t.ResultUrl,
		"result_text":     t.ResultText,
		"error_msg":       t.ErrorMsg,
		"points_frozen":   t.PointsFrozen,
		"cost_points":     t.CostPoints,
		"created_at":      t.CreatedAt,
		"started_at":      t.StartedAt,
		"finished_at":     t.FinishedAt,
	}
}

// GetGenerationModels 可用模型列表（GET /generation/models，匿名），附带积分定价。
func GetGenerationModels(c *gin.Context) {
	models := service.ListModels()
	prices, _ := model.GetModelPrices(true)
	model.FillModelPricesEffective(prices)
	priceByModel := map[string]*model.ModelPrice{}
	for _, p := range prices {
		priceByModel[p.Model] = p
	}
	health := service.CachedModelHealth()
	items := make([]gin.H, 0, len(models))
	for _, m := range models {
		item := gin.H{
			"provider":               m.Provider,
			"model":                  m.Model,
			"type":                   m.Type,
			"label":                  m.Label,
			"unit":                   "",
			"points_per_unit":        0,
			"member_points_per_unit": 0,
			"priced":                 false,
			// 促销价（T18）
			"promo_points_per_unit":            0,
			"promo_label":                      "",
			"promo_starts_at":                  0,
			"promo_ends_at":                    0,
			"promo_active":                     false,
			"effective_points_per_unit":        0,
			"effective_member_points_per_unit": 0,
			"campaign_promo_label":             "",
			// 该模型支持的时长（秒）与分辨率；空数组表示不限，前端据此限制选项
			"durations":   []int{},
			"resolutions": []string{},
			// 图片模型接收参考图 / 原图的方式（none / edits / url），前端据此决定能不能做局部编辑、带参考图
			"image_input": "",
			// 媒体后处理能力（API.md 23）：模型能做哪些素材后处理，前端据此点亮工具条上的 AI 按钮
			"abilities": []string{},
		}
		if m.Type == model.TaskTypeImage {
			item["image_input"] = model.InferImageInput(m.Model)
		}
		item["abilities"] = model.InferAbilities(m.Model)
		// 最近 7 天健康（API.md 37）：样本够且失败率过高时 recent_warn=true，创作台给一句提醒
		item["recent_total"], item["recent_fail_rate"], item["recent_warn"] = 0, 0.0, false
		if h := health[m.Model]; h != nil {
			item["recent_total"] = h.Total
			if h.FailRate != nil {
				item["recent_fail_rate"] = *h.FailRate
			}
			item["recent_warn"] = h.Warn
		}
		if p := priceByModel[m.Model]; p != nil {
			item["unit"] = p.Unit
			item["points_per_unit"] = p.PointsPerUnit
			item["member_points_per_unit"] = p.MemberPointsPerUnit
			item["priced"] = true
			item["promo_points_per_unit"] = p.PromoPointsPerUnit
			item["promo_label"] = p.PromoLabel
			item["promo_starts_at"] = p.PromoStartsAt
			item["promo_ends_at"] = p.PromoEndsAt
			item["promo_active"] = p.PromoActive
			item["effective_points_per_unit"] = p.EffectivePointsPerUnit
			item["effective_member_points_per_unit"] = p.EffectiveMemberPointsPerUnit
			item["campaign_promo_label"] = p.CampaignPromoLabel
			if d := p.AllowedDurations(); len(d) > 0 {
				item["durations"] = d
			}
			if r := p.AllowedResolutions(); len(r) > 0 {
				item["resolutions"] = r
			}
			if m.Type == model.TaskTypeImage {
				item["image_input"] = p.EffectiveImageInput()
			}
			item["abilities"] = p.EffectiveAbilities()
		}
		items = append(items, item)
	}
	common.ApiSuccess(c, items)
}

// CreateTask 创建生成任务（POST /user/task）。
func CreateTask(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Type      string          `json:"type"`
		Model     string          `json:"model"`
		Prompt    string          `json:"prompt"`
		Params    json.RawMessage `json:"params"`
		Inputs    []taskInput     `json:"inputs"`
		CanvasId  int             `json:"canvas_id"`
		NodeId    string          `json:"node_id"`
		ProjectId int             `json:"project_id"`
		SkillId   int             `json:"skill_id"`
		// 主体库引用（F2）与多语言（F5）
		SubjectIds []int `json:"subject_ids"`
		// SubjectVersions 锁定的主体版本 { "主体 id": 版本号 }（API.md 14.2），缺省用最新
		SubjectVersions map[string]int `json:"subject_versions"`
		Lang            string         `json:"lang"`
		Voice           string         `json:"voice"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	// 技能：必须是官方 / 已上架，或自己的技能（T26 分成按此记账）
	var skill *model.Skill
	if req.SkillId > 0 {
		s, err := model.GetSkillById(req.SkillId)
		if err != nil || !(s.PublicVisible() || s.UserId == userId) {
			common.ApiErrorMsg(c, "技能不存在或未上架")
			return
		}
		skill = s
	}
	req.Type = strings.TrimSpace(req.Type)
	req.Model = trimLimit(req.Model, 100)
	if !model.IsValidTaskType(req.Type) {
		common.ApiErrorMsg(c, "无效的任务类型")
		return
	}
	if req.Model == "" {
		common.ApiErrorMsg(c, "请选择模型")
		return
	}
	provider := service.FindProvider(req.Type, req.Model)
	if provider == nil {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 暂未接入", req.Model))
		return
	}

	params := "{}"
	if len(req.Params) > 0 {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(req.Params, &probe); err != nil {
			common.ApiErrorMsg(c, "params 必须是 JSON 对象")
			return
		}
		// 对话式工作流（agent）任务只能由画布对话接口创建（POST /user/canvas/:id/agent → service.NewAgentTask）。
		// params 是客户端原样透传的，而 IsFreeChatTask 只看 params.agent.free：放行的话，任何登录用户
		// 自带 free=true 就能建出免积分任务（跳过估价与预扣，ChatFreeEnabled=false 时也免单，复核发现 high）。
		// free 只能由服务端写入；同一道闸门也挂在批量编排入口（service.CreateBatch），规则见 CheckNoClientAgentParams。
		if err := service.CheckNoClientAgentParams(string(req.Params)); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		params = string(req.Params)
	}
	if service.TaskResultCount(params) > 1 {
		common.ApiErrorMsg(c, "一次只能生成 1 个结果，需要多个请分多次提交")
		return
	}

	projectId := req.ProjectId
	if req.CanvasId > 0 {
		cv, perm, err := service.CanvasAccess(userId, req.CanvasId)
		if err != nil {
			respondModelError(c, err, "查询画布失败")
			return
		}
		if !service.CanvasWritable(perm) {
			common.ApiErrorMsg(c, "你在团队里是查看者，不能在队友的画布上生成")
			return
		}
		// 画布挂在哪个项目，任务就算哪个项目（任务空间跟项目走，API.md 20）
		projectId = cv.ProjectId
	} else if projectId > 0 {
		if _, err := model.GetUserProject(userId, projectId); err != nil {
			respondModelError(c, err, "查询项目失败")
			return
		}
	}
	// 任务空间：团队项目的画布 → 该团队；否则用请求空间。输入素材与主体按这个空间校验
	teamId, err := service.ResolveTaskTeam(userId, teamIdOf(c), projectId)
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}

	// 输入素材必须属于当前用户，或是任务所在团队空间的资产
	if len(req.Inputs) > 20 {
		common.ApiErrorMsg(c, "输入素材过多")
		return
	}
	for _, in := range req.Inputs {
		if in.AssetId <= 0 {
			common.ApiErrorMsg(c, "输入素材 asset_id 无效")
			return
		}
		// 自己的素材，或团队空间的资产（在团队画布上生成时会用到队友的素材，API.md 15 / 20）
		if !service.AssetUsableBy(userId, teamId, in.AssetId) {
			service.Track(userId, model.TrackEventRefMissing, map[string]any{"source": "input", "count": 1, "type": req.Type, "canvas_id": req.CanvasId})
			common.ApiErrorMsg(c, fmt.Sprintf("输入素材 %d 不存在", in.AssetId))
			return
		}
	}
	// 审批门禁（API.md 26.1）：画布开了门禁时，视频任务的分镜图要先过审
	if req.CanvasId > 0 && req.Type == model.TaskTypeVideo {
		gi := make([]service.GateInput, 0, len(req.Inputs))
		for _, in := range req.Inputs {
			gi = append(gi, service.GateInput{AssetId: in.AssetId, Role: in.Role})
		}
		if err := service.ReviewGateCheckCanvas(req.CanvasId, req.Type, gi); err != nil {
			common.ApiErrorData(c, err.Error(), gin.H{"reason": "review_gate"})
			return
		}
	}
	if req.Type == model.TaskTypeImage && len(req.Inputs) > 0 && service.ImageInputModeOf(req.Model) == model.ImageInputNone {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 不支持参考图或图片编辑，请换 gpt-image、seedream 等支持编辑的模型", req.Model))
		return
	}
	inputsBytes, _ := json.Marshal(req.Inputs)
	if req.Inputs == nil {
		inputsBytes = []byte("[]")
	}

	prompt := strings.TrimSpace(req.Prompt)
	inputsJSON := string(inputsBytes)
	// 主体库引用（F2）：参考图并入 inputs，描述追加到提示词
	if len(req.SubjectIds) > 0 {
		if len(req.SubjectIds) > 4 {
			req.SubjectIds = req.SubjectIds[:4]
		}
		var used []int
		versions := map[int]int{}
		for k, v := range req.SubjectVersions {
			if id, err := strconv.Atoi(k); err == nil && v > 0 {
				versions[id] = v
			}
		}
		prompt, inputsJSON, used = service.ApplySubjectsToTask(userId, teamId, req.SubjectIds, prompt, inputsJSON, versions)
		model.IncreaseSubjectUses(used)
	}
	// 多语言配音（F5）：先把台词翻成目标语种再 TTS
	lang := service.NormalizeLang(req.Lang)
	if req.Type == model.TaskTypeAudio && lang != "" && lang != "zh" {
		if translated, err := service.TranslateText(prompt, lang); err == nil && translated != "" {
			prompt = translated
		}
	}
	if req.Voice != "" {
		params = service.WithVoiceParam(params, trimLimit(req.Voice, 40))
	}
	// 技能执行契约：主体描述等用户内容都拼完之后再包一层，让规则稳定排在最前面
	if req.Type == model.TaskTypeText {
		prompt = service.ApplySkillGuide(skill, prompt)
	}
	task := &model.GenerationTask{
		UserId:    userId,
		TeamId:    teamId,
		ProjectId: projectId,
		CanvasId:  req.CanvasId,
		NodeId:    trimLimit(req.NodeId, 64),
		SkillId:   req.SkillId,
		Type:      req.Type,
		Provider:  provider.Name(),
		Model:     req.Model,
		Prompt:    prompt,
		Params:    params,
		Inputs:    inputsJSON,
		Lang:      lang,
	}
	launchTask(c, task, provider)
}

// launchTask 估算并预扣积分（未定价模型不允许创建）→ 入库 → 提交网关 → 返回任务视图。CreateTask 与 RetryMyTask 共用。
// 调用前 task.TeamId 必须已确定（CreateTask 自己算，其它入口走 resolveTaskTeam）。
func launchTask(c *gin.Context, task *model.GenerationTask, provider service.GenerationProvider) {
	if _, err := service.StartTask(task, provider); err != nil {
		if respondSpaceError(c, err) {
			return
		}
		// 免积分对话次数用完：中文 message + data.reason，前端按码分流、不依赖中文原文
		if errors.Is(err, service.ErrChatQuotaExceeded) {
			respondChatQuotaExceeded(c)
			return
		}
		// 并发满了不是「任务失败」，前端要能排队重试，所以额外回一个 data.reason=concurrency 让它按原因分流。
		var full *service.ErrConcurrencyFull
		if errors.As(err, &full) {
			common.ApiErrorData(c, err.Error(), gin.H{
				"reason": "concurrency",
				"limit":  full.Limit,
				"used":   full.Used,
			})
			return
		}
		var modelErr *service.ErrTaskModel
		if errors.As(err, &modelErr) {
			respondModelError(c, modelErr.Err, modelErr.Msg)
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	// 记录模型 API 调用审计
	model.RecordSimpleAudit(c, model.AuditCategoryUsage, "model.api_call.create",
		fmt.Sprintf("创建%s任务 %s [%s]", task.Type, task.Model, task.Provider),
		map[string]interface{}{"model": task.Model, "type": task.Type, "provider": task.Provider, "task_id": task.Id})
	common.ApiSuccess(c, taskView(task))
}

// respondChatQuotaExceeded 次数用完：message 给人看，data.reason 给前端认（API.md 32.5）。
func respondChatQuotaExceeded(c *gin.Context) {
	limit := setting.GetChatDailyFreeCount()
	common.ApiErrorData(c, service.ChatQuotaExceededMessage(limit), gin.H{
		"reason":      service.ChatQuotaExceededReason,
		"daily_limit": limit,
	})
}

// RetryMyTask 重试失败 / 已取消的任务（POST /user/task/:id/retry）：按原参数新建一条任务，重新预扣积分。
func RetryMyTask(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的任务 ID")
		return
	}
	src, err := model.GetUserTask(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if src.Status != model.TaskStatusFailed && src.Status != model.TaskStatusCancelled {
		common.ApiErrorMsg(c, "只有失败或已取消的任务可以重试")
		return
	}
	provider := service.FindProvider(src.Type, src.Model)
	if provider == nil {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 暂未接入", src.Model))
		return
	}
	// 通用重试不能照抄源任务的 free 标记（复核发现 medium）：源任务带 params.agent 时，
	// free 必须按当前开关重新评估后再入库，口径与画布专用重试 NewAgentRetryTask（service/agent_loop.go）一致——
	// 否则运营关掉 ChatFreeEnabled 后，重试以前创建的免积分对话任务仍然免单（只扣一次每日次数），开关回滚不彻底；
	// 重估而不是拒绝，是为了不打断用户在任务列表里的重试。免积分标记只能由服务端写：CreateTask 已挡住客户端自带的 params.agent。
	task := &model.GenerationTask{
		UserId:    userId,
		ProjectId: src.ProjectId,
		CanvasId:  src.CanvasId,
		NodeId:    src.NodeId,
		SkillId:   src.SkillId,
		Type:      src.Type,
		Provider:  provider.Name(),
		Model:     src.Model,
		Prompt:    src.Prompt,
		Params:    service.RewriteChatFreeFlag(src.Params, setting.IsChatFreeEnabled()),
		Inputs:    src.Inputs,
	}
	if !resolveTaskTeam(c, task) {
		return
	}
	launchTask(c, task, provider)
}

// GetMyTaskSummary 任务中心概览（GET /user/task/summary）：各状态数量、今日 / 30 天消耗积分。
func GetMyTaskSummary(c *gin.Context) {
	s, err := model.GetTaskSummary(c.GetInt("id"), teamIdOf(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, s)
}

// GetMyTasks 任务列表（GET /user/task?canvas_id=&status=）。
// 按请求空间只列自己在该空间的任务（API.md 20）；带 canvas_id 时画布已经限定了范围，不再按空间过滤。
func GetMyTasks(c *gin.Context) {
	filter := model.TaskFilter{
		UserId:   c.GetInt("id"),
		Space:    queryInt(c, "canvas_id", 0) <= 0,
		TeamId:   teamIdOf(c),
		CanvasId: queryInt(c, "canvas_id", 0),
		Status:   strings.TrimSpace(c.Query("status")),
		Type:     strings.TrimSpace(c.Query("type")),
		Keyword:  strings.TrimSpace(c.Query("keyword")),
	}
	// active=1：只看进行中（pending + running），任务中心轮询用
	if c.Query("active") == "1" {
		filter.Active = true
	}
	if filter.Status != "" && !model.IsValidTaskStatus(filter.Status) {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	list, total, err := model.GetTasks(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	views := make([]map[string]any, 0, len(list))
	for _, t := range list {
		views = append(views, taskView(t))
	}
	pagedResponse(c, views, total)
}

// GetMyTask 任务详情（GET /user/task/:id）。
func GetMyTask(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的任务 ID")
		return
	}
	t, err := model.GetUserTask(c.GetInt("id"), id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, taskView(t))
}

// CancelMyTask 取消任务（POST /user/task/:id/cancel）。
func CancelMyTask(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的任务 ID")
		return
	}
	if err := service.CancelTask(c.GetInt("id"), id); err != nil {
		respondModelError(c, err, "取消失败")
		return
	}
	common.ApiSuccess(c, nil)
}
