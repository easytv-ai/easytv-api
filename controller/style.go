package controller

import (
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 风格库：公开 + 管理端（API.md 34）=====

// styleViews 批量转响应视图。
func styleViews(list []*model.Style, admin bool) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, item.View(admin))
	}
	return out
}

// GetPublicStyles 风格列表（GET /style?keyword=&tag=&category=&kind=）：官方已上架 + 用户已收录。
func GetPublicStyles(c *gin.Context) {
	kind := strings.TrimSpace(c.Query("kind"))
	if kind != "" && !model.IsValidStyleKind(kind) {
		common.ApiErrorMsg(c, "无效的适用类型")
		return
	}
	filter := model.StyleFilter{
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Tag:      strings.TrimSpace(c.Query("tag")),
		Category: strings.TrimSpace(c.Query("category")),
		Kind:     kind,
		Public:   true,
	}
	list, total, err := model.GetStyles(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, styleViews(list, false), total)
}

// GetPublicStyleCategories 分类页签（GET /style/categories）。
func GetPublicStyleCategories(c *gin.Context) {
	list, err := model.GetStyleCategories()
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

// UseStyle 套用风格计数（POST /style/:id/use），需登录：官方上架、已收录、或本人的私有风格。
func UseStyle(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的风格 ID")
		return
	}
	s, err := model.GetStyleById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if !model.CanUseStyle(s, c.GetInt("id")) {
		common.ApiErrorMsg(c, "风格不存在或已下架")
		return
	}
	model.IncreaseStyleUses(id)
	common.ApiSuccess(c, gin.H{"id": id, "uses": s.Uses + 1})
}

// ===== 管理端 =====

type stylePayload struct {
	Name      *string   `json:"name"`
	CoverUrl  *string   `json:"cover_url"`
	Prompt    *string   `json:"prompt"`
	Negative  *string   `json:"negative"`
	Tags      *[]string `json:"tags"`
	Category  *string   `json:"category"`
	Kinds     *[]string `json:"kinds"`
	SortOrder *int      `json:"sort_order"`
	Status    *int      `json:"status"`
	// SourceAssetId 「定风格」来源图资产 ID，只留痕（用户侧）
	SourceAssetId *int `json:"source_asset_id"`
}

// styleStatusAllowed 按行归属限定可写的状态：官方 1/2，用户风格 1/3/4。
func styleStatusAllowed(official bool, status int) bool {
	if official {
		return status == model.StyleStatusOn || status == model.StyleStatusOff
	}
	return status == model.StyleStatusOn || status == model.StyleStatusPending || status == model.StyleStatusAccepted
}

// adminUserStyleFields 管理端对用户风格只允许改的字段。
var adminUserStyleFields = map[string]bool{"category": true, "tags": true, "sort_order": true, "status": true}

// fields 校验并整理入参；status 只做四态范围检查，官方 / 用户的进一步限制由各 handler 按行判断。
func (p *stylePayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Name != nil {
		name := trimLimit(*p.Name, model.StyleNameMax)
		if name == "" {
			return nil, "请填写风格名"
		}
		f["name"] = name
	}
	if p.Prompt != nil {
		prompt := strings.TrimSpace(*p.Prompt)
		if prompt == "" {
			return nil, "请填写风格提示词"
		}
		if len([]rune(prompt)) > model.StylePromptMax {
			return nil, "风格提示词过长"
		}
		f["prompt"] = prompt
	}
	if p.Negative != nil {
		negative := strings.TrimSpace(*p.Negative)
		if len([]rune(negative)) > model.StyleNegativeMax {
			return nil, "负向提示词过长"
		}
		f["negative"] = negative
	}
	if p.CoverUrl != nil {
		f["cover_url"] = trimLimit(*p.CoverUrl, 1000)
	}
	if p.Tags != nil {
		cleaned := []string{}
		for _, tag := range *p.Tags {
			if v := trimLimit(tag, 30); v != "" {
				cleaned = append(cleaned, v)
			}
		}
		f["tags"] = trimLimit(strings.Join(cleaned, ","), 255)
	}
	if p.Category != nil {
		f["category"] = trimLimit(*p.Category, 50)
	}
	if p.Kinds != nil {
		cleaned := []string{}
		seen := map[string]bool{}
		for _, kind := range *p.Kinds {
			v := strings.ToLower(strings.TrimSpace(kind))
			if v == "" || seen[v] {
				continue
			}
			if !model.IsValidStyleKind(v) {
				return nil, "无效的适用类型"
			}
			seen[v] = true
			cleaned = append(cleaned, v)
		}
		f["kinds"] = strings.Join(cleaned, ",")
	}
	if p.SortOrder != nil {
		f["sort_order"] = *p.SortOrder
	}
	if p.Status != nil {
		if !model.IsValidStyleStatus(*p.Status) {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	if p.SourceAssetId != nil {
		if *p.SourceAssetId < 0 {
			return nil, "无效的来源图"
		}
		f["source_asset_id"] = *p.SourceAssetId
	}
	return f, ""
}

// styleFromFields 用整理后的字段拼一条新行（创建时用）。
func styleFromFields(f map[string]any) *model.Style {
	str := func(k string) string {
		if v, ok := f[k].(string); ok {
			return v
		}
		return ""
	}
	s := &model.Style{
		Name:     str("name"),
		CoverUrl: str("cover_url"),
		Prompt:   str("prompt"),
		Negative: str("negative"),
		Tags:     str("tags"),
		Category: str("category"),
		Kinds:    str("kinds"),
	}
	if v, ok := f["sort_order"].(int); ok {
		s.SortOrder = v
	}
	if v, ok := f["status"].(int); ok {
		s.Status = v
	}
	if v, ok := f["source_asset_id"].(int); ok {
		s.SourceAssetId = v
	}
	return s
}

// adminStyleViews 管理视图 + 作者昵称（用户风格批量查一次）。
func adminStyleViews(list []*model.Style) []map[string]any {
	ids := make([]int, 0, len(list))
	for _, item := range list {
		if item.UserId > 0 {
			ids = append(ids, item.UserId)
		}
	}
	names := map[int]string{}
	if len(ids) > 0 {
		names = model.GetUserNicknames(ids)
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		view := item.View(true)
		view["user_nickname"] = names[item.UserId]
		out = append(out, view)
	}
	return out
}

// AdminGetStyles 管理端列表（GET /admin/style?source=official|user&status=1..4），全部状态。
func AdminGetStyles(c *gin.Context) {
	filter := model.StyleFilter{
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Tag:      strings.TrimSpace(c.Query("tag")),
		Category: strings.TrimSpace(c.Query("category")),
	}
	switch source := strings.TrimSpace(c.Query("source")); source {
	case "", model.StyleSourceOfficial, model.StyleSourceUser:
		filter.Source = source
	default:
		common.ApiErrorMsg(c, "无效的来源")
		return
	}
	if kind := strings.TrimSpace(c.Query("kind")); kind != "" {
		if !model.IsValidStyleKind(kind) {
			common.ApiErrorMsg(c, "无效的适用类型")
			return
		}
		filter.Kind = kind
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := queryInt(c, "status", 0)
		if !model.IsValidStyleStatus(status) {
			common.ApiErrorMsg(c, "无效的状态")
			return
		}
		filter.Status = &status
	}
	list, total, err := model.GetStyles(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, adminStyleViews(list), total)
}

// AdminCreateStyle 新增风格（POST /admin/style）。
func AdminCreateStyle(c *gin.Context) {
	var req stylePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Name == nil || req.Prompt == nil {
		common.ApiErrorMsg(c, "风格名和风格提示词必填")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if v, ok := f["status"].(int); ok && !styleStatusAllowed(true, v) {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	s := styleFromFields(f)
	s.SourceAssetId = 0
	if err := s.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, s.View(true))
}

// AdminUpdateStyle 修改风格（PUT /admin/style/:id）。
func AdminUpdateStyle(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的风格 ID")
		return
	}
	var req stylePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有要更新的字段")
		return
	}
	s, err := model.GetStyleById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if msg := adminStyleUpdateCheck(s, f); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if err := model.UpdateStyleFields(id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// adminStyleUpdateCheck 管理端改行的归属限制：官方 status 只能 1/2；
// 用户风格只能改 category / tags / sort_order / status，且 status 只能 1/3/4。
func adminStyleUpdateCheck(s *model.Style, f map[string]any) string {
	official := s.IsOfficialStyle()
	if !official {
		for k := range f {
			if !adminUserStyleFields[k] {
				return "用户风格只能修改分类、标签、排序和状态"
			}
		}
	}
	if v, ok := f["status"].(int); ok && !styleStatusAllowed(official, v) {
		return "无效的状态"
	}
	return ""
}

// ----- 投稿审核 -----

type styleReviewPayload struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason"`
}

// AdminReviewStyle 审核用户投稿（POST /admin/style/:id/review）：通过 → 4，驳回 → 1；记站内通知与审计。
func AdminReviewStyle(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的风格 ID")
		return
	}
	var req styleReviewPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	req.Reason = trimLimit(req.Reason, 200)
	s, err := model.GetStyleById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	next, msg := styleReviewTransition(s, req.Approve)
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if err := model.UpdateStyleFields(id, map[string]any{"status": next}); err != nil {
		respondModelError(c, err, "审核失败")
		return
	}
	s.Status = next
	title, body := styleReviewNotice(s.Name, req.Approve, req.Reason)
	model.PushNotification(s.UserId, model.NotificationTypeTask, title, body, "")
	model.RecordSimpleAudit(c, model.AuditCategoryOperation, "style.review",
		fmt.Sprintf("风格投稿审核：%s「%s」", map[bool]string{true: "通过", false: "驳回"}[req.Approve], s.Name),
		map[string]any{"id": id, "user_id": s.UserId, "approve": req.Approve, "reason": req.Reason})
	c.Set(string(constant.ContextKeyAuditLogged), true)
	common.ApiSuccess(c, s.View(true))
}

// styleReviewTransition 审核状态机：只有用户风格且 status=3 可审；通过 → 4，驳回 → 1。
func styleReviewTransition(s *model.Style, approve bool) (int, string) {
	if s.IsOfficialStyle() {
		return 0, "官方风格无需审核"
	}
	if s.Status != model.StyleStatusPending {
		return 0, "该风格不在待审核状态"
	}
	if approve {
		return model.StyleStatusAccepted, ""
	}
	return model.StyleStatusOn, ""
}

// styleReviewNotice 审核结果站内通知的标题与正文。
func styleReviewNotice(name string, approve bool, reason string) (title, body string) {
	if approve {
		title, body = "风格投稿已收录", fmt.Sprintf("你投稿的风格「%s」已收录进官方风格库，所有人都能看到并套用了。", name)
	} else {
		title, body = "风格投稿未通过", fmt.Sprintf("你投稿的风格「%s」未通过审核，已退回为私有风格。", name)
	}
	if reason != "" {
		body += "原因：" + reason
	}
	return title, body
}

// AdminDeleteStyle 删除风格（DELETE /admin/style/:id）。
func AdminDeleteStyle(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的风格 ID")
		return
	}
	if err := model.DB.Where("id = ?", id).Delete(&model.Style{}).Error; err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}
