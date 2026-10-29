package controller

import (
	"encoding/json"
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// ===== 片段重拍 / 续写 / 超分（API.md 10.3，F4）=====

// loadFinishedVideoTask 取当前用户已成功的视频任务。
func loadFinishedVideoTask(c *gin.Context) (*model.GenerationTask, bool) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的任务 ID")
		return nil, false
	}
	t, err := model.GetUserTask(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return nil, false
	}
	if t.Status != model.TaskStatusSucceeded {
		common.ApiErrorMsg(c, "只有已完成的任务可以做这个操作")
		return nil, false
	}
	if t.Type != model.TaskTypeVideo {
		common.ApiErrorMsg(c, "只有视频任务支持重拍 / 续写 / 超分")
		return nil, false
	}
	return t, true
}

// RetakeMyTask POST /user/task/:id/retake 同参数重跑，结果替换原节点。
// 支持面板改提示词 / 参数 / 模型（不传就沿用原任务，等价于「原样重来一次」）。
func RetakeMyTask(c *gin.Context) {
	src, ok := loadFinishedVideoTask(c)
	if !ok {
		return
	}
	var req struct {
		Prompt string          `json:"prompt"`
		Params json.RawMessage `json:"params"`
		Model  string          `json:"model"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		modelName = src.Model
	}
	provider := service.FindProvider(src.Type, modelName)
	if provider == nil {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 暂未接入", modelName))
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = src.Prompt
	}
	task := &model.GenerationTask{
		UserId: src.UserId, ProjectId: src.ProjectId, CanvasId: src.CanvasId, NodeId: src.NodeId,
		SkillId: src.SkillId, Type: src.Type, Provider: provider.Name(), Model: modelName,
		Prompt: prompt, Params: mergeTaskParams(src.Params, req.Params), Inputs: src.Inputs, Lang: src.Lang,
		RetakeOf: src.Id,
	}
	service.Track(src.UserId, "node_retake", map[string]any{"task_id": src.Id, "model": modelName})
	if !resolveTaskTeam(c, task) {
		return
	}
	launchTask(c, task, provider)
}

// mergeTaskParams 面板传的新参数覆盖原任务参数（只覆盖传来的键，其余沿用）。
// 传空或解析失败时原样返回，保证「不改参数就重跑」永远是安全的。
func mergeTaskParams(base string, overlay json.RawMessage) string {
	if len(overlay) == 0 {
		return base
	}
	add := map[string]any{}
	if err := json.Unmarshal(overlay, &add); err != nil || len(add) == 0 {
		return base
	}
	out := map[string]any{}
	if base != "" {
		_ = json.Unmarshal([]byte(base), &out)
	}
	for k, v := range add {
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return base
	}
	return string(b)
}

// ExtendMyTask POST /user/task/:id/extend 取末帧作首帧，续写下一段。
func ExtendMyTask(c *gin.Context) {
	src, ok := loadFinishedVideoTask(c)
	if !ok {
		return
	}
	var req struct {
		Prompt  string `json:"prompt"`
		Seconds int    `json:"seconds"`
		NodeId  string `json:"node_id"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	if src.ResultAssetId <= 0 {
		common.ApiErrorMsg(c, "原视频资产不可用")
		return
	}
	video, err := model.GetUserAsset(src.UserId, src.ResultAssetId)
	if err != nil {
		common.ApiErrorMsg(c, "原视频资产不可用")
		return
	}
	frame, err := service.ExtractLastFrame(src.UserId, video)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	provider := service.FindProvider(src.Type, src.Model)
	if provider == nil {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 暂未接入", src.Model))
		return
	}
	// 参数沿用原任务，替换时长；输入换成末帧作首帧
	params := map[string]any{}
	if src.Params != "" {
		_ = json.Unmarshal([]byte(src.Params), &params)
	}
	params["mode"] = "fl2v"
	if req.Seconds >= 3 && req.Seconds <= 15 {
		params["duration"] = req.Seconds
	}
	paramsJSON, _ := json.Marshal(params)
	inputs, _ := json.Marshal([]map[string]any{{"asset_id": frame.Id, "role": "first_frame"}})
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = src.Prompt
	}
	nodeId := trimLimit(strings.TrimSpace(req.NodeId), 64)
	if nodeId == "" {
		nodeId = src.NodeId
	}
	task := &model.GenerationTask{
		UserId: src.UserId, ProjectId: src.ProjectId, CanvasId: src.CanvasId, NodeId: nodeId,
		Type: src.Type, Provider: provider.Name(), Model: src.Model, Prompt: prompt,
		Params: string(paramsJSON), Inputs: string(inputs), ParentTaskId: src.Id,
	}
	service.Track(src.UserId, "node_extend", map[string]any{"task_id": src.Id, "frame_asset": frame.Id})
	if !resolveTaskTeam(c, task) {
		return
	}
	launchTask(c, task, provider)
}

// ExtendMyAsset POST /user/asset/:id/extend 对上传 / 处理得到的视频资产续写（没有来源任务）：
// 取末帧作首帧建新视频任务。模型按请求 → 运营配的成片默认视频模型 → 清单第一个视频模型。
func ExtendMyAsset(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的素材 ID")
		return
	}
	var req struct {
		Model     string `json:"model"`
		Prompt    string `json:"prompt"`
		Seconds   int    `json:"seconds"`
		NodeId    string `json:"node_id"`
		CanvasId  int    `json:"canvas_id"`
		ProjectId int    `json:"project_id"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	video, err := service.ResolveOwnedAsset(userId, teamIdOf(c), id)
	if err != nil {
		common.ApiErrorMsg(c, "原视频资产不可用")
		return
	}
	if video.Type != model.AssetTypeVideo {
		common.ApiErrorMsg(c, "只能对视频续写")
		return
	}
	modelName := pickExtendModel(req.Model)
	if modelName == "" {
		common.ApiErrorMsg(c, "暂无可用的视频模型，请联系运营开通")
		return
	}
	provider := service.FindProvider(model.TaskTypeVideo, modelName)
	if provider == nil {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 暂未接入", modelName))
		return
	}
	frame, err := service.ExtractLastFrame(userId, video)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	// 模型是服务端挑的，用户在胶片条上选的秒数未必在它的档位里：落到最接近的档，别直接拒
	params := map[string]any{"mode": "fl2v"}
	if req.Seconds >= 3 && req.Seconds <= 15 {
		seconds := req.Seconds
		if price, err := model.GetModelPrice(modelName); err == nil {
			seconds = snapDuration(seconds, price.AllowedDurations())
		}
		params["duration"] = seconds
	}
	paramsJSON, _ := json.Marshal(params)
	inputs, _ := json.Marshal([]map[string]any{{"asset_id": frame.Id, "role": "first_frame"}})
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "承接上一段画面，自然延续镜头与动作"
	}
	task := &model.GenerationTask{
		UserId: userId, ProjectId: req.ProjectId, CanvasId: req.CanvasId, NodeId: trimLimit(strings.TrimSpace(req.NodeId), 64),
		Type: model.TaskTypeVideo, Provider: provider.Name(), Model: modelName, Prompt: prompt,
		Params: string(paramsJSON), Inputs: string(inputs),
	}
	service.Track(userId, "node_extend", map[string]any{"asset_id": video.Id, "frame_asset": frame.Id, "model": modelName})
	if !resolveTaskTeam(c, task) {
		return
	}
	launchTask(c, task, provider)
}

// snapDuration 把秒数落到模型允许的档位里最接近的一档（距离相同取更长的）；模型没声明档位原样返回。
func snapDuration(seconds int, allowed []int) int {
	if len(allowed) == 0 {
		return seconds
	}
	best := allowed[0]
	for _, tier := range allowed {
		gap, bestGap := tier-seconds, best-seconds
		if gap < 0 {
			gap = -gap
		}
		if bestGap < 0 {
			bestGap = -bestGap
		}
		if gap < bestGap || (gap == bestGap && tier > best) {
			best = tier
		}
	}
	return best
}

// pickExtendModel 续写用的视频模型：请求指定 → ComposeVideoModel → 清单第一个视频模型；都不在清单里返回空。
func pickExtendModel(requested string) string {
	for _, name := range []string{strings.TrimSpace(requested), strings.TrimSpace(setting.GetSettingString(setting.ComposeVideoModel))} {
		if name != "" && service.FindProvider(model.TaskTypeVideo, name) != nil {
			return name
		}
	}
	for _, m := range service.ListModels() {
		if m.Type == model.TaskTypeVideo {
			return m.Model
		}
	}
	return ""
}

// UpscaleMyTask POST /user/task/:id/upscale 超分（工具条「高清」）。
// 源可以是任务（取产物）也可以是直接的资产 ID；模型按 upscale 能力位挑，也可由面板指定。
func UpscaleMyTask(c *gin.Context) {
	userId := c.GetInt("id")
	teamId := teamIdOf(c)
	var req struct {
		AssetId    int     `json:"asset_id"`
		Model      string  `json:"model"`
		Resolution string  `json:"resolution"`
		Scale      float64 `json:"scale"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	// 别名路由 POST /user/asset/:id/upscale：路径里的 id 就是资产（上传 / 处理得到的视频没有来源任务）
	if req.AssetId <= 0 && strings.Contains(c.FullPath(), "/asset/") {
		req.AssetId = paramInt(c, "id")
	}

	var src *model.GenerationTask
	videoAssetId := req.AssetId
	if videoAssetId <= 0 {
		t, ok := loadFinishedVideoTask(c)
		if !ok {
			return
		}
		src = t
		if src.ResultAssetId <= 0 {
			common.ApiErrorMsg(c, "原视频资产不可用")
			return
		}
		videoAssetId = src.ResultAssetId
	}

	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		modelName = service.PickUpscaleModel()
	}
	if modelName == "" {
		common.ApiErrorMsg(c, "暂未接入超分模型，请联系运营")
		return
	}
	if !service.ModelHasAbility(modelName, model.AbilityUpscale) {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 不支持超分", modelName))
		return
	}
	provider := service.FindProvider(model.TaskTypeVideo, modelName)
	if provider == nil {
		common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 暂未接入", modelName))
		return
	}
	if _, err := service.ResolveOwnedAsset(userId, teamId, videoAssetId); err != nil {
		common.ApiErrorMsg(c, "原视频资产不可用")
		return
	}
	// 面板选的档位透传给网关（选了不支持的档位会被 model_price.resolutions 挡下来）
	params := map[string]any{"count": 1}
	if v := strings.TrimSpace(req.Resolution); v != "" {
		params["resolution"] = v
	}
	if req.Scale > 0 {
		params["scale"] = req.Scale
	}
	paramsJSON, _ := json.Marshal(params)
	inputs, _ := json.Marshal([]map[string]any{{"asset_id": videoAssetId, "role": "reference_video"}})
	task := &model.GenerationTask{
		UserId: userId, Type: model.TaskTypeVideo, Provider: provider.Name(), Model: modelName,
		Prompt: "超分", Params: string(paramsJSON), Inputs: string(inputs),
	}
	if src != nil {
		task.ProjectId, task.CanvasId, task.NodeId, task.Prompt = src.ProjectId, src.CanvasId, src.NodeId, src.Prompt
		task.RetakeOf = src.Id
	}
	service.Track(userId, "node_upscale", map[string]any{"asset_id": videoAssetId, "model": modelName})
	if !resolveTaskTeam(c, task) {
		return
	}
	launchTask(c, task, provider)
}
