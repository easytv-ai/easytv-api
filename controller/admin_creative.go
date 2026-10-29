package controller

import (
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 管理侧：用户详情/注销 + 创作域列表与操作 =====

// adminUserView 管理端用户脱敏视图。
func adminUserView(u *model.User) gin.H {
	identity := strings.TrimSpace(u.Identity)
	return gin.H{
		"id":              u.Id,
		"username":        u.Username,
		"nickname":        u.Nickname,
		"role":            u.Role,
		"status":          u.Status,
		"group":           u.Group,
		"quota":           u.Quota,
		"used_quota":      u.UsedQuota,
		"request_count":   u.RequestCount,
		"mobile":          u.Mobile,
		"identity":        identity,
		"tags":            model.UserIdentityTags(identity),
		"wechat_id":       u.WeChatId,
		"wechat_nickname": u.WeChatNickname,
		"github_id":       u.GitHubId,
		"inviter_id":      u.InviterId,
		"aff_code":        u.AffCode,
		"invite_blocked":  u.InviteBlocked,
		"source":          u.Source,
		"medium":          u.Medium,
		"campaign":        u.Campaign,
		"bonus_code":      u.BonusCode,
		"remark":          u.Remark,
		"created_at":      u.CreatedAt,
		"last_login_at":   u.LastLoginAt,
	}
}

// AdminGetUser 用户详情（GET /admin/user/:id）。
func AdminGetUser(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return
	}
	u, err := model.GetUserById(id)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	common.ApiSuccess(c, adminUserView(u))
}

// AdminDeleteUser 注销用户（DELETE /admin/user/:id，软删）。不能删自己，普通管理员不能删管理员。
func AdminDeleteUser(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return
	}
	if id == c.GetInt("id") {
		common.ApiErrorMsg(c, "不能注销当前登录账号")
		return
	}
	target, err := model.GetUserById(id)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	if target.Role >= constant.RoleAdminUser && c.GetInt("role") < constant.RoleRootUser {
		common.ApiErrorMsg(c, "无权限注销管理员账号")
		return
	}
	if target.Role >= constant.RoleRootUser {
		common.ApiErrorMsg(c, "不能注销超级管理员")
		return
	}
	if err := model.DB.Where("id = ?", id).Delete(&model.User{}).Error; err != nil {
		common.ApiErrorMsg(c, "注销失败")
		return
	}
	_ = model.InvalidateUserAccessToken(id)
	common.SysLog(fmt.Sprintf("admin %d deleted user %d", c.GetInt("id"), id))
	common.ApiSuccess(c, nil)
}

// trashedFlag 读取 status 过滤参数（1 正常 / 2 回收站 / 空 全部）。
func trashedFlag(c *gin.Context) string {
	switch strings.TrimSpace(c.Query("status")) {
	case "1":
		return "1"
	case "2":
		return "2"
	}
	return ""
}

// deletedFlag deleted_at 是否有值 → 管理端 status 2。
func trashedStatus(deleted bool) int {
	if deleted {
		return 2
	}
	return 1
}

// ----- 项目 -----

// AdminGetProjects 全站项目（GET /admin/project?user_id=&keyword=&status=）。
func AdminGetProjects(c *gin.Context) {
	list, total, err := model.GetAllProjectsAdmin(queryInt(c, "user_id", 0), strings.TrimSpace(c.Query("keyword")), trashedFlag(c), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	ids := make([]int, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.UserId)
	}
	names := model.GetUserNicknames(ids)
	items := make([]gin.H, 0, len(list))
	for _, p := range list {
		items = append(items, gin.H{
			"id":          p.Id,
			"user_id":     p.UserId,
			"owner_name":  names[p.UserId],
			"name":        p.Name,
			"description": p.Description,
			"cover_url":   p.CoverUrl,
			"status":      trashedStatus(p.DeletedAt.Valid),
			"created_at":  p.CreatedAt,
			"updated_at":  p.UpdatedAt,
		})
	}
	pagedResponse(c, items, total)
}

// AdminSetProjectStatus 移入回收站 / 恢复（PUT /admin/project/:id/status {status:1|2}）。
func AdminSetProjectStatus(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的项目 ID")
		return
	}
	var req struct {
		Status int `json:"status"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	var err error
	switch req.Status {
	case 1:
		err = model.AdminRestoreProject(id)
	case 2:
		err = model.AdminTrashProject(id)
	default:
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	if err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteProject 物理删除（DELETE /admin/project/:id）。
func AdminDeleteProject(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的项目 ID")
		return
	}
	if err := model.AdminDeleteProject(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d hard-deleted project %d", c.GetInt("id"), id))
	common.ApiSuccess(c, nil)
}

// ----- 画布 -----

// AdminGetCanvases 全站画布（GET /admin/canvas?user_id=&project_id=&keyword=&status=），附带节点数。
func AdminGetCanvases(c *gin.Context) {
	list, total, err := model.GetAllCanvasesAdmin(queryInt(c, "user_id", 0), queryInt(c, "project_id", 0), strings.TrimSpace(c.Query("keyword")), trashedFlag(c), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	ids := make([]int, 0, len(list))
	for _, cv := range list {
		ids = append(ids, cv.UserId)
	}
	names := model.GetUserNicknames(ids)
	items := make([]gin.H, 0, len(list))
	for _, cv := range list {
		items = append(items, gin.H{
			"id":            cv.Id,
			"user_id":       cv.UserId,
			"owner_name":    names[cv.UserId],
			"project_id":    cv.ProjectId,
			"name":          cv.Name,
			"version":       cv.Version,
			"node_count":    cv.NodeCount(),
			"thumbnail_url": cv.ThumbnailUrl,
			"status":        trashedStatus(cv.DeletedAt.Valid),
			"created_at":    cv.CreatedAt,
			"updated_at":    cv.UpdatedAt,
		})
	}
	pagedResponse(c, items, total)
}

// AdminSetCanvasStatus 移入回收站 / 恢复（PUT /admin/canvas/:id/status {status:1|2}）。
func AdminSetCanvasStatus(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	var req struct {
		Status int `json:"status"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	var err error
	switch req.Status {
	case 1:
		err = model.AdminRestoreCanvas(id)
	case 2:
		err = model.AdminTrashCanvas(id)
	default:
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	if err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteCanvas 物理删除（DELETE /admin/canvas/:id）。
func AdminDeleteCanvas(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	if err := model.AdminDeleteCanvas(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ----- 资产 -----

// AdminGetAssets 全站资产（GET /admin/asset?user_id=&type=&keyword=&status=）。
func AdminGetAssets(c *gin.Context) {
	filter := model.AssetFilter{
		UserId:  queryInt(c, "user_id", 0),
		Type:    strings.TrimSpace(c.Query("type")),
		Source:  strings.TrimSpace(c.Query("source")),
		Keyword: strings.TrimSpace(c.Query("keyword")),
	}
	if filter.Type != "" && !model.IsValidAssetType(filter.Type) {
		common.ApiErrorMsg(c, "无效的资产类型")
		return
	}
	list, total, err := model.GetAssetsAdmin(filter, trashedFlag(c), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	ids := make([]int, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.UserId)
	}
	names := model.GetUserNicknames(ids)
	items := make([]gin.H, 0, len(list))
	for _, a := range list {
		items = append(items, gin.H{
			"id":         a.Id,
			"user_id":    a.UserId,
			"owner_name": names[a.UserId],
			"project_id": a.ProjectId,
			"type":       a.Type,
			"name":       a.Name,
			"url":        a.Url,
			"mime":       a.Mime,
			"size":       a.Size,
			"source":     a.Source,
			"task_id":    a.TaskId,
			"status":     trashedStatus(a.DeletedAt.Valid),
			"created_at": a.CreatedAt,
		})
	}
	pagedResponse(c, items, total)
}

// AdminSetAssetStatus 移入回收站 / 恢复（PUT /admin/asset/:id/status {status:1|2}）。
func AdminSetAssetStatus(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的资产 ID")
		return
	}
	var req struct {
		Status int `json:"status"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	var err error
	switch req.Status {
	case 1:
		err = model.AdminRestoreAsset(id)
	case 2:
		err = model.AdminTrashAsset(id)
	default:
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	if err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteAsset 物理删除并清理文件（DELETE /admin/asset/:id）。
func AdminDeleteAsset(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的资产 ID")
		return
	}
	asset, err := model.GetAssetByIdUnscoped(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if err := model.AdminDeleteAsset(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	if asset.StorageKey != "" && service.DefaultStorage != nil {
		if err := service.DefaultStorage.Remove(asset.StorageKey); err != nil {
			common.SysError("asset file remove failed: " + err.Error())
		}
	}
	common.ApiSuccess(c, nil)
}

// ----- 任务 -----

// AdminGetTasks 全站任务（GET /admin/task?user_id=&status=&type=）。
func AdminGetTasks(c *gin.Context) {
	filter := model.TaskFilter{
		UserId: queryInt(c, "user_id", 0),
		Status: strings.TrimSpace(c.Query("status")),
		Type:   strings.TrimSpace(c.Query("type")),
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

// ----- 作品 -----

// AdminGetWorks 全站作品（GET /admin/work?user_id=&status=&keyword=）。
func AdminGetWorks(c *gin.Context) {
	filter := model.WorkFilter{
		UserId:  queryInt(c, "user_id", 0),
		Keyword: strings.TrimSpace(c.Query("keyword")),
		Lang:    service.NormalizeLang(c.Query("lang")), // 语种过滤（F6 出海）
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := queryInt(c, "status", 0)
		if !model.IsValidWorkStatus(status) {
			common.ApiErrorMsg(c, "无效的状态")
			return
		}
		filter.Status = &status
	}
	if raw := strings.TrimSpace(c.Query("featured")); raw != "" {
		featured := queryInt(c, "featured", 0)
		filter.Featured = &featured
	}
	if raw := strings.TrimSpace(c.Query("review_status")); raw != "" {
		review := queryInt(c, "review_status", 0)
		filter.ReviewStatus = &review
	}
	if category := strings.TrimSpace(c.Query("category")); category != "" {
		filter.Category = category
	}
	list, total, err := model.GetWorks(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminUpdateWork 编辑作品 / 精选设置（PUT /admin/work/:id）。
func AdminUpdateWork(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	var req struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Category    *string `json:"category"`
		CoverUrl    *string `json:"cover_url"`
		Featured    *int    `json:"featured"`
		SortOrder   *int    `json:"sort_order"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	fields := map[string]any{}
	if req.Title != nil {
		title := trimLimit(*req.Title, 200)
		if title == "" {
			common.ApiErrorMsg(c, "请填写作品标题")
			return
		}
		fields["title"] = title
	}
	if req.Description != nil {
		fields["description"] = trimLimit(*req.Description, 1000)
	}
	if req.Category != nil {
		fields["category"] = trimLimit(*req.Category, 50)
	}
	if req.CoverUrl != nil {
		fields["cover_url"] = trimLimit(*req.CoverUrl, 1000)
	}
	if req.Featured != nil {
		if *req.Featured != 0 && *req.Featured != 1 {
			common.ApiErrorMsg(c, "无效的精选标记")
			return
		}
		fields["featured"] = *req.Featured
	}
	if req.SortOrder != nil {
		fields["sort_order"] = *req.SortOrder
	}
	if len(fields) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.AdminUpdateWork(id, fields); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminReviewWork 发布审核（PUT /admin/work/:id/review）：{ status: 1 通过 | 2 驳回, remark? }。
func AdminReviewWork(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	var req struct {
		Status int    `json:"status"`
		Remark string `json:"remark"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Status != model.WorkReviewApproved && req.Status != model.WorkReviewRejected {
		common.ApiErrorMsg(c, "无效的审核结果")
		return
	}
	if err := model.ReviewWork(id, req.Status, trimLimit(req.Remark, 255)); err != nil {
		respondModelError(c, err, "审核失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d review work %d result=%d", c.GetInt("id"), id, req.Status))
	if req.Status == model.WorkReviewRejected {
		notifyWorkOwner(id, model.WorkStatusRejected, req.Remark)
	}
	common.ApiSuccess(c, nil)
}

// ----- 举报 -----

// AdminGetWorkReports 举报列表（GET /admin/work-report?status=&keyword=）。
func AdminGetWorkReports(c *gin.Context) {
	var status *int
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		v := queryInt(c, "status", 0)
		status = &v
	}
	list, total, err := model.GetWorkReports(status, strings.TrimSpace(c.Query("target_type")), strings.TrimSpace(c.Query("keyword")), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminSetWorkReportStatus 处理举报（PUT /admin/work-report/:id/status）：{ status: 1 成立（下架作品）| 2 驳回, remark? }。
func AdminSetWorkReportStatus(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的举报 ID")
		return
	}
	var req struct {
		Status int    `json:"status"`
		Remark string `json:"remark"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if !model.IsValidReportStatus(req.Status) {
		common.ApiErrorMsg(c, "无效的处理结果")
		return
	}
	if err := model.SetWorkReportStatus(id, req.Status, trimLimit(req.Remark, 255)); err != nil {
		respondModelError(c, err, "处理失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d handle report %d result=%d", c.GetInt("id"), id, req.Status))
	if req.Status == model.ReportStatusUpheld {
		if r, err := model.GetWorkReportById(id); err == nil {
			if r.TargetType == model.ReportTargetComment {
				if cm, err := model.GetWorkCommentById(r.CommentId); err == nil {
					notifyCommentHidden(cm, "收到举报并核实："+r.Reason)
				}
			} else {
				notifyWorkOwner(r.WorkId, model.WorkStatusUnlisted, "收到举报并核实："+r.Reason)
			}
		}
	}
	common.ApiSuccess(c, nil)
}

// AdminSetWorkStatus 审核 / 上下架（PUT /admin/work/:id/status）。
func AdminSetWorkStatus(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	var req struct {
		Status int    `json:"status"`
		Remark string `json:"remark"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if !model.IsValidWorkStatus(req.Status) {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	if err := model.SetWorkStatus(id, req.Status, trimLimit(req.Remark, 255)); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d set work %d status=%d", c.GetInt("id"), id, req.Status))
	if req.Status == model.WorkStatusUnlisted || req.Status == model.WorkStatusRejected {
		notifyWorkOwner(id, req.Status, req.Remark)
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteWork 删除作品（DELETE /admin/work/:id）。
func AdminDeleteWork(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	if err := model.AdminDeleteWork(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ----- 技能 -----

// AdminGetSkills 技能列表（GET /admin/skill?category=&keyword=&status=&source=）。
func AdminGetSkills(c *gin.Context) {
	filter := model.SkillFilter{
		Category: strings.TrimSpace(c.Query("category")),
		Keyword:  strings.TrimSpace(c.Query("keyword")),
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := queryInt(c, "status", 0)
		if status != model.SkillStatusOn && status != model.SkillStatusOff {
			common.ApiErrorMsg(c, "无效的状态")
			return
		}
		filter.Status = &status
	}
	if raw := strings.TrimSpace(c.Query("review_status")); raw != "" {
		rs := queryInt(c, "review_status", -1)
		if rs < 0 || rs > 3 {
			common.ApiErrorMsg(c, "无效的审核状态")
			return
		}
		filter.ReviewStatus = &rs
	}
	// source：缺省不过滤来源（官方 + 用户私有），official 只取官方，user 只取用户私有
	switch source := strings.TrimSpace(c.Query("source")); source {
	case "official":
		uid := model.SkillUserOfficial
		filter.UserId = &uid
	case "user":
		uid := model.SkillUserAllPrivate
		filter.UserId = &uid
	case "":
		// 全部
	default:
		common.ApiErrorMsg(c, "无效的来源")
		return
	}
	list, total, err := model.GetSkills(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.AttachSkillAuthors(list)
	pagedResponse(c, list, total)
}

type skillPayload struct {
	Title       *string `json:"title"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	AuthorName  *string `json:"author_name"`
	Category    *string `json:"category"`
	CoverUrl    *string `json:"cover_url"`
	VideoUrl    *string `json:"video_url"`
	Scene       *string `json:"scene"`
	Howto       *string `json:"howto"`
	Output      *string `json:"output"`
	Prompt      *string `json:"prompt"`
	// 执行契约（只影响文本任务）
	Guide        *string `json:"guide"`
	OutputFormat *string `json:"output_format"`
	SortOrder    *int    `json:"sort_order"`
	Status       *int    `json:"status"`
}

func (p *skillPayload) fields() (map[string]any, string) {
	fields := map[string]any{}
	if p.Title != nil {
		title := trimLimit(*p.Title, 100)
		if title == "" {
			return nil, "请填写 Skill 名称"
		}
		fields["title"] = title
	}
	if p.Slug != nil {
		fields["slug"] = trimLimit(*p.Slug, 100)
	}
	if p.Description != nil {
		fields["description"] = trimLimit(*p.Description, 500)
	}
	if p.AuthorName != nil {
		fields["author_name"] = trimLimit(*p.AuthorName, 100)
	}
	if p.Category != nil {
		fields["category"] = trimLimit(*p.Category, 50)
	}
	if p.CoverUrl != nil {
		fields["cover_url"] = trimLimit(*p.CoverUrl, 1000)
	}
	if p.VideoUrl != nil {
		fields["video_url"] = trimLimit(*p.VideoUrl, 1000)
	}
	if p.Guide != nil {
		guide := strings.TrimSpace(*p.Guide)
		if len([]rune(guide)) > service.SkillGuideMaxChars {
			return nil, "执行规则不能超过 4000 字符"
		}
		fields["guide"] = guide
	}
	if p.OutputFormat != nil {
		format := strings.TrimSpace(*p.OutputFormat)
		if len([]rune(format)) > service.SkillGuideMaxChars {
			return nil, "输出格式不能超过 4000 字符"
		}
		fields["output_format"] = format
	}
	if p.Scene != nil {
		fields["scene"] = trimLimit(*p.Scene, 200)
	}
	if p.Howto != nil {
		fields["howto"] = trimLimit(*p.Howto, 500)
	}
	if p.Output != nil {
		fields["output"] = trimLimit(*p.Output, 500)
	}
	if p.Prompt != nil {
		fields["prompt"] = strings.TrimSpace(*p.Prompt)
	}
	if p.SortOrder != nil {
		fields["sort_order"] = *p.SortOrder
	}
	if p.Status != nil {
		if *p.Status != model.SkillStatusOn && *p.Status != model.SkillStatusOff {
			return nil, "无效的状态"
		}
		fields["status"] = *p.Status
	}
	return fields, ""
}

// AdminCreateSkill 新建技能（POST /admin/skill）。
func AdminCreateSkill(c *gin.Context) {
	var req skillPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Title == nil || strings.TrimSpace(*req.Title) == "" {
		common.ApiErrorMsg(c, "请填写 Skill 名称")
		return
	}
	fields, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	s := &model.Skill{}
	str := func(k string) string {
		if v, ok := fields[k].(string); ok {
			return v
		}
		return ""
	}
	s.Title = str("title")
	s.Slug = str("slug")
	s.Description = str("description")
	s.AuthorName = str("author_name")
	s.Category = str("category")
	s.CoverUrl = str("cover_url")
	s.VideoUrl = str("video_url")
	s.Scene = str("scene")
	s.Howto = str("howto")
	s.Output = str("output")
	s.Prompt = str("prompt")
	s.Guide = str("guide")
	s.OutputFormat = str("output_format")
	if v, ok := fields["sort_order"].(int); ok {
		s.SortOrder = v
	}
	if v, ok := fields["status"].(int); ok {
		s.Status = v
	}
	if err := s.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, s)
}

// AdminUpdateSkill 修改技能（PUT /admin/skill/:id）。
func AdminUpdateSkill(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Skill ID")
		return
	}
	var req skillPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	fields, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(fields) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	s, err := model.GetSkillById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	// 私有 Skill（user_id > 0）后台只能下架：不允许编辑内容、不允许重新上架
	if s.UserId > 0 {
		if len(fields) != 1 {
			common.ApiErrorMsg(c, "私有 Skill 不允许编辑内容，只能下架")
			return
		}
		status, ok := fields["status"].(int)
		if !ok {
			common.ApiErrorMsg(c, "私有 Skill 不允许编辑内容，只能下架")
			return
		}
		if status != model.SkillStatusOff {
			common.ApiErrorMsg(c, "私有 Skill 不允许重新上架")
			return
		}
	}
	if err := model.UpdateSkillFields(id, fields); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteSkill 删除技能（DELETE /admin/skill/:id）。
func AdminDeleteSkill(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Skill ID")
		return
	}
	s, err := model.GetSkillById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	// 私有 Skill 属于用户资产，后台只能下架不能删除（与 AdminUpdateSkill 的限制一致）
	if s.UserId > 0 {
		common.ApiErrorMsg(c, "私有 Skill 不允许删除，只能下架")
		return
	}
	if err := model.DeleteSkill(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// notifyWorkOwner 作品被下架 / 驳回时通知作者（站内通知，见 API.md 1.9）。
func notifyWorkOwner(workId, status int, remark string) {
	w, err := model.GetWorkById(workId)
	if err != nil {
		return
	}
	title := "作品《" + w.Title + "》已下架"
	content := "作品已从首页信息流移除。"
	if status == model.WorkStatusRejected {
		title = "作品《" + w.Title + "》未通过审核"
		content = "作品已从首页信息流隐藏，无法自行重新上架。"
	}
	if remark = strings.TrimSpace(remark); remark != "" {
		content += "原因：" + remark
	}
	model.PushNotification(w.UserId, model.NotificationTypeWork, title, content, "/settings?tab=works")
}
