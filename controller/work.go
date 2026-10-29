package controller

import (
	"encoding/json"
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 作品 =====

// GetPublicWorks 公开信息流（GET /work?keyword=）。
func GetPublicWorks(c *gin.Context) {
	published := model.WorkStatusPublished
	filter := model.WorkFilter{
		Status:   &published,
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Category: strings.TrimSpace(c.Query("category")),
		UserId:   queryInt(c, "user_id", 0),
		Lang:     service.NormalizeLang(c.Query("lang")),
	}
	if c.Query("featured") == "1" {
		featured := 1
		filter.Featured = &featured
	}
	list, total, err := model.GetWorks(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.AttachLiked(c.GetInt("id"), list)
	pagedResponse(c, list, total)
}

// ToggleWorkLike 点赞 / 取消点赞（POST /user/work/:id/like），返回 { liked, like_count }。
func ToggleWorkLike(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	liked, count, err := model.ToggleWorkLike(c.GetInt("id"), id)
	if err != nil {
		respondModelError(c, err, "操作失败")
		return
	}
	if liked {
		notifyWorkLiked(c.GetInt("id"), id, count)
	}
	common.ApiSuccess(c, gin.H{"liked": liked, "like_count": count})
}

// notifyWorkLiked 作品收到喜欢时通知作者：每部作品 24 小时内只发一条，自己点赞不通知。
func notifyWorkLiked(likerId, workId, count int) {
	w, err := model.GetWorkById(workId)
	if err != nil || w.UserId == likerId {
		return
	}
	link := fmt.Sprintf("/watch/%d", workId)
	if model.HasRecentNotification(w.UserId, model.NotificationTypeLike, link, common.GetTimestamp()-86400) {
		return
	}
	names := model.GetUserNicknames([]int{likerId})
	who := names[likerId]
	if who == "" {
		who = "有人"
	}
	content := fmt.Sprintf("《%s》收到了新的喜欢，目前共 %d 个。", w.Title, count)
	model.PushNotification(w.UserId, model.NotificationTypeLike, who+" 喜欢了你的作品", content, link)
}

// GetAuthorProfile 作者主页（GET /author/:id，匿名）：昵称、加入时间与作品统计。
func GetAuthorProfile(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return
	}
	u, err := model.GetUserById(id)
	if err != nil || u == nil || u.Status != constant.UserStatusEnabled {
		common.ApiErrorMsg(c, "作者不存在")
		return
	}
	stats := model.GetAuthorStats(id)
	nickname := u.Nickname
	if nickname == "" {
		nickname = u.Username
	}
	viewer := c.GetInt("id")
	common.ApiSuccess(c, gin.H{
		"id":              u.Id,
		"nickname":        nickname,
		"joined_at":       u.CreatedAt,
		"work_count":      stats.WorkCount,
		"like_total":      stats.LikeTotal,
		"view_total":      stats.ViewTotal,
		"mine":            viewer == u.Id,
		"followers":       model.CountFollowers(u.Id),
		"following_count": model.CountFollowing(u.Id),
		"is_following":    model.IsFollowing(viewer, u.Id),
	})
}

// GetPublicWork 公开详情（GET /work/:id），浏览数 +1。
func GetPublicWork(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	w, err := model.GetPublishedWork(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.IncreaseWorkView(id)
	w.ViewCount++
	model.AttachLiked(c.GetInt("id"), []*model.Work{w})
	w.AttachSource()
	common.ApiSuccess(c, w)
}

// validateWorkSource 做同款来源校验（T5）：来源画布必须属于发布者；Skill 必须是官方或自己的；开源必须有来源画布。
func validateWorkSource(userId, sourceCanvasId, sourceSkillId, openSource int) string {
	if openSource != 0 && openSource != 1 {
		return "open_source 只能是 0 或 1"
	}
	if sourceCanvasId < 0 || sourceSkillId < 0 {
		return "无效的来源"
	}
	if sourceCanvasId > 0 {
		if _, err := model.GetUserCanvas(userId, sourceCanvasId); err != nil {
			return "来源画布不存在或不属于你"
		}
	}
	if sourceSkillId > 0 {
		s, err := model.GetSkillById(sourceSkillId)
		if err != nil || (s.UserId != 0 && s.UserId != userId) {
			return "Skill 不存在"
		}
	}
	if openSource == 1 && sourceCanvasId == 0 {
		return "开源作品需要指定来源画布"
	}
	return ""
}

// CloneWork 做同款（POST /user/work/:id/clone）：把开源作品的来源画布去敏后复制到我的新项目，返回 { canvas_id, project_id }。
func CloneWork(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	w, err := model.GetPublishedWork(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if !w.CanClone() {
		common.ApiErrorMsg(c, "作者未开放做同款")
		return
	}
	src, err := model.GetUserCanvas(w.UserId, w.SourceCanvasId)
	if err != nil {
		common.ApiErrorMsg(c, "来源画布已不可用")
		return
	}
	name := w.Title
	if name == "" {
		name = src.Name
	}
	cv, err := model.CopyCanvasToUser(userId, src, "同款 · "+name, fmt.Sprintf("做同款：来自作品 #%d", w.Id))
	if err != nil {
		respondModelError(c, err, "复制失败")
		return
	}
	model.IncreaseWorkClone(w.Id)
	service.RewardWorkClone(w.UserId, userId, w.Id, w.Title)
	service.Track(userId, model.TrackEventWorkClone, map[string]any{
		"work_id": w.Id, "author_id": w.UserId, "source_canvas_id": w.SourceCanvasId, "skill_id": w.SourceSkillId, "canvas_id": cv.Id,
	})
	common.ApiSuccess(c, gin.H{"canvas_id": cv.Id, "project_id": cv.ProjectId})
}

// GetWorkClonePreview GET /work/:id/clone-preview：做同款前的预览（开源作品；登录时按会员价估算）。
func GetWorkClonePreview(c *gin.Context) {
	w, err := model.GetPublishedWork(paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if !w.CanClone() {
		common.ApiErrorMsg(c, "作者未开放做同款")
		return
	}
	src, err := model.GetUserCanvas(w.UserId, w.SourceCanvasId)
	if err != nil {
		common.ApiErrorMsg(c, "来源画布已不可用")
		return
	}
	common.ApiSuccess(c, service.BuildClonePreview(w, src, c.GetInt("id")))
}

// GetMyWorks 我的作品（GET /user/work?status=&keyword=）。
func GetMyWorks(c *gin.Context) {
	filter := model.WorkFilter{
		UserId:  c.GetInt("id"),
		Keyword: strings.TrimSpace(c.Query("keyword")),
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := queryInt(c, "status", 0)
		if !model.IsValidWorkStatus(status) {
			common.ApiErrorMsg(c, "无效的状态")
			return
		}
		filter.Status = &status
	}
	list, total, err := model.GetWorks(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// CreateWork 从资产发布作品（POST /user/work）。
func CreateWork(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		AssetId     int    `json:"asset_id"`
		ProjectId   int    `json:"project_id"`
		CoverUrl    string `json:"cover_url"`
		Category    string `json:"category"`
		CanvasId    int    `json:"canvas_id"`
		ShowProcess *bool  `json:"show_process"`
		// 做同款（T5）
		SourceCanvasId int  `json:"source_canvas_id"`
		SourceSkillId  int  `json:"source_skill_id"`
		OpenSource     *int `json:"open_source"`
		// Lang 作品语种（F6）：前端按界面语言传
		Lang string `json:"lang"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	title := trimLimit(req.Title, 200)
	if title == "" {
		common.ApiErrorMsg(c, "请填写作品标题")
		return
	}
	if req.AssetId <= 0 {
		common.ApiErrorMsg(c, "请选择成片资产")
		return
	}
	asset, err := model.GetUserAsset(userId, req.AssetId)
	if err != nil {
		respondModelError(c, err, "查询资产失败")
		return
	}
	if asset.Type != model.AssetTypeVideo {
		common.ApiErrorMsg(c, "只能发布视频资产")
		return
	}
	if req.ProjectId > 0 {
		if _, err := model.GetUserProject(userId, req.ProjectId); err != nil {
			respondModelError(c, err, "查询项目失败")
			return
		}
	}
	if req.CanvasId > 0 {
		cv, err := model.GetUserCanvas(userId, req.CanvasId)
		if err != nil {
			respondModelError(c, err, "查询画布失败")
			return
		}
		if req.ProjectId == 0 {
			req.ProjectId = cv.ProjectId
		}
	}
	showProcess := true
	if req.ShowProcess != nil {
		showProcess = *req.ShowProcess
	}
	openSource := 0
	if req.OpenSource != nil {
		openSource = *req.OpenSource
	}
	if openSource == 1 && req.SourceCanvasId == 0 {
		// 开源但没单独指定来源画布：默认用制作过程所在画布
		req.SourceCanvasId = req.CanvasId
	}
	if msg := validateWorkSource(userId, req.SourceCanvasId, req.SourceSkillId, openSource); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	coverUrl := trimLimit(req.CoverUrl, 1000)
	if coverUrl == "" {
		// 没选封面就用视频抽帧（OSS 截帧约 1 秒，抽不到留空由前端用视频首帧）
		coverUrl = service.EnsureAssetCover(asset)
	}
	w := &model.Work{
		UserId:      userId,
		ProjectId:   req.ProjectId,
		AssetId:     asset.Id,
		Title:       title,
		Description: trimLimit(req.Description, 1000),
		CoverUrl:    coverUrl,
		Category:    trimLimit(req.Category, 50),
		CanvasId:    req.CanvasId,
		ShowProcess: showProcess,
		VideoUrl:    asset.Url,
		Duration:    asset.Duration,
		Status:      model.WorkStatusPublished,

		SourceCanvasId: req.SourceCanvasId,
		SourceSkillId:  req.SourceSkillId,
		OpenSource:     openSource,
		Lang:           service.NormalizeLang(req.Lang),
	}
	if err := w.Insert(); err != nil {
		respondModelError(c, err, "发布失败")
		return
	}
	if w.CanClone() {
		go service.SnapshotWorkTemplate(w.Id, w.UserId, w.SourceCanvasId)
	}
	service.TrackOnce(w.UserId, model.TrackEventFirstWork, map[string]any{"work_id": w.Id})
	// 关注的作者发新作品：给粉丝发站内信（M11）
	go NotifyFollowersNewWork(w, authorDisplayName(userId))
	common.ApiSuccess(c, w)
}

// authorDisplayName 昵称优先，回退账号名。
func authorDisplayName(userId int) string {
	if names := model.GetUserNicknames([]int{userId}); names[userId] != "" {
		return names[userId]
	}
	return "你关注的作者"
}

// UpdateMyWork 修改作品 / 上下架（PUT /user/work/:id）。
func UpdateMyWork(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	var req struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		CoverUrl    *string `json:"cover_url"`
		Category    *string `json:"category"`
		Status      *int    `json:"status"`
		ShowProcess *bool   `json:"show_process"`
		// 做同款（T5）
		SourceCanvasId *int `json:"source_canvas_id"`
		SourceSkillId  *int `json:"source_skill_id"`
		OpenSource     *int `json:"open_source"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	fields := map[string]any{}
	snapshotCanvasId := 0
	if req.SourceCanvasId != nil || req.SourceSkillId != nil || req.OpenSource != nil {
		// 校验要看合并后的状态：没传的字段取作品当前值
		w, err := model.GetUserWork(userId, id)
		if err != nil {
			respondModelError(c, err, "查询失败")
			return
		}
		sourceCanvasId, sourceSkillId, openSource := w.SourceCanvasId, w.SourceSkillId, w.OpenSource
		if req.SourceCanvasId != nil {
			sourceCanvasId = *req.SourceCanvasId
			fields["source_canvas_id"] = sourceCanvasId
		}
		if req.SourceSkillId != nil {
			sourceSkillId = *req.SourceSkillId
			fields["source_skill_id"] = sourceSkillId
		}
		if req.OpenSource != nil {
			openSource = *req.OpenSource
			fields["open_source"] = openSource
		}
		if openSource == 1 && sourceCanvasId == 0 && w.CanvasId > 0 {
			sourceCanvasId = w.CanvasId
			fields["source_canvas_id"] = sourceCanvasId
		}
		if msg := validateWorkSource(userId, sourceCanvasId, sourceSkillId, openSource); msg != "" {
			common.ApiErrorMsg(c, msg)
			return
		}
		// 开源或换了来源画布：重新拍模板快照
		if openSource == 1 && sourceCanvasId > 0 && (w.OpenSource != 1 || w.SourceCanvasId != sourceCanvasId || w.TemplateMeta == "") {
			snapshotCanvasId = sourceCanvasId
		}
	}
	if req.Title != nil {
		title := trimLimit(*req.Title, 200)
		if title == "" {
			common.ApiErrorMsg(c, "作品标题不能为空")
			return
		}
		fields["title"] = title
	}
	if req.Description != nil {
		fields["description"] = trimLimit(*req.Description, 1000)
	}
	if req.CoverUrl != nil {
		cover := trimLimit(*req.CoverUrl, 1000)
		if cover == "" {
			// 「用视频首帧」：清空封面时重新抽帧
			if w, err := model.GetUserWork(userId, id); err == nil && w.AssetId > 0 {
				if asset, err := model.GetUserAsset(userId, w.AssetId); err == nil {
					cover = service.EnsureAssetCover(asset)
				}
			}
		}
		fields["cover_url"] = cover
	}
	if req.Category != nil {
		fields["category"] = trimLimit(*req.Category, 50)
	}
	if req.ShowProcess != nil {
		fields["show_process"] = *req.ShowProcess
	}
	if req.Status != nil {
		// 用户只能在 已发布 / 已下架 之间切换；审核驳回的作品不能自行上架
		if *req.Status != model.WorkStatusPublished && *req.Status != model.WorkStatusUnlisted {
			common.ApiErrorMsg(c, "无效的状态")
			return
		}
		w, err := model.GetUserWork(userId, id)
		if err != nil {
			respondModelError(c, err, "查询失败")
			return
		}
		if w.Status == model.WorkStatusRejected {
			common.ApiErrorMsg(c, "作品已被驳回，无法上架")
			return
		}
		fields["status"] = *req.Status
		if *req.Status == model.WorkStatusPublished && w.PublishedAt == 0 {
			fields["published_at"] = common.GetTimestamp()
		}
	}
	if len(fields) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.UpdateWorkFields(userId, id, fields); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	if snapshotCanvasId > 0 {
		go service.SnapshotWorkTemplate(id, userId, snapshotCanvasId)
	}
	common.ApiSuccess(c, nil)
}

// DeleteMyWork 删除作品（DELETE /user/work/:id）。
func DeleteMyWork(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	if err := model.DeleteUserWork(c.GetInt("id"), id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ReportWork 举报已发布作品（POST /user/work/:id/report）。
func ReportWork(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	var req struct {
		Reason  string `json:"reason"`
		Content string `json:"content"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	reason := trimLimit(req.Reason, 50)
	if reason == "" {
		common.ApiErrorMsg(c, "请选择举报原因")
		return
	}
	r, err := model.CreateWorkReport(c.GetInt("id"), id, reason, trimLimit(req.Content, 500))
	if err != nil {
		respondModelError(c, err, "提交失败")
		return
	}
	common.ApiSuccess(c, r)
}

// GetWorkProcess 作品制作过程（GET /work/:id/process）：已发布且作者公开时，返回发布时所在画布的节点与连线（只读）。
func GetWorkProcess(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	w, err := model.GetPublishedWork(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	// 开源作品（T5）制作过程一律可看，没填制作画布时用来源画布；非开源保持原策略
	canvasId := w.CanvasId
	if canvasId == 0 && w.CanClone() {
		canvasId = w.SourceCanvasId
	}
	if (!w.ShowProcess && !w.CanClone()) || canvasId == 0 {
		common.ApiErrorMsg(c, "作者未公开制作过程")
		return
	}
	cv, err := model.GetUserCanvas(w.UserId, canvasId)
	if err != nil {
		common.ApiErrorMsg(c, "制作过程已不可用")
		return
	}
	var graph struct {
		Nodes []struct {
			Id         string `json:"id"`
			ParentNode string `json:"parentNode"`
			Position   struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"position"`
			Data struct {
				Kind    string `json:"kind"`
				Label   string `json:"label"`
				Prompt  string `json:"prompt"`
				Content string `json:"content"`
				Model   string `json:"model"`
				RefUrl  string `json:"refUrl"`
				AssetId int    `json:"assetId"`
			} `json:"data"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"edges"`
	}
	if cv.Graph != "" {
		if err := json.Unmarshal([]byte(cv.Graph), &graph); err != nil {
			common.ApiErrorMsg(c, "制作过程数据无法解析")
			return
		}
	}
	nodes := make([]gin.H, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if n.Data.Kind == "" || n.Data.Kind == "group" {
			continue
		}
		// 只暴露资产地址（生成产物 / 上传素材），文本节点内容直接给
		content := ""
		if n.Data.Kind == "text" || n.Data.AssetId > 0 {
			content = n.Data.Content
		}
		nodes = append(nodes, gin.H{
			"id":      n.Id,
			"kind":    n.Data.Kind,
			"label":   n.Data.Label,
			"prompt":  n.Data.Prompt,
			"content": content,
			"model":   n.Data.Model,
			"ref_url": n.Data.RefUrl,
			"x":       n.Position.X,
			"y":       n.Position.Y,
		})
	}
	edges := make([]gin.H, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		edges = append(edges, gin.H{"source": e.Source, "target": e.Target})
	}
	common.ApiSuccess(c, gin.H{
		"work":   gin.H{"id": w.Id, "title": w.Title, "user_id": w.UserId},
		"canvas": gin.H{"id": cv.Id, "name": cv.Name, "updated_at": cv.UpdatedAt},
		"nodes":  nodes,
		"edges":  edges,
	})
}
