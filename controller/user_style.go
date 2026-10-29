package controller

import (
	"errors"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 我的风格（API.md 34.5，L18 提示词版自定义风格）=====
//
// 状态机（用户视角）：
//   1 私有可用 --submit--> 3 待审核 --review(approve)--> 4 已收录
//   3 --withdraw--> 1 ；4 --withdraw--> 1 ；4 --edit--> 3（重审）；3 --review(reject)--> 1

// ownedStyle 取本人的风格，不是本人或不存在都报「风格不存在」。
func ownedStyle(c *gin.Context, id int) (*model.Style, bool) {
	if id == 0 {
		common.ApiErrorMsg(c, "无效的风格 ID")
		return nil, false
	}
	s, err := model.GetStyleById(id)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		respondModelError(c, err, "查询失败")
		return nil, false
	}
	if err != nil || s.UserId != c.GetInt("id") {
		common.ApiErrorMsg(c, "风格不存在")
		return nil, false
	}
	return s, true
}

// GetMyStyles 我的风格分页（GET /user/style?keyword=&kind=&page=），全部状态。
func GetMyStyles(c *gin.Context) {
	userId := c.GetInt("id")
	kind := strings.TrimSpace(c.Query("kind"))
	if kind != "" && !model.IsValidStyleKind(kind) {
		common.ApiErrorMsg(c, "无效的适用类型")
		return
	}
	list, total, err := model.GetStyles(model.StyleFilter{
		Keyword: strings.TrimSpace(c.Query("keyword")),
		Kind:    kind,
		UserId:  &userId,
	}, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, item := range list {
		items = append(items, item.OwnerView())
	}
	pagedResponse(c, items, total)
}

// CreateMyStyle 新建私有风格（POST /user/style），status=1，每人上限 100 条。
func CreateMyStyle(c *gin.Context) {
	userId := c.GetInt("id")
	var req stylePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Name == nil || req.Prompt == nil {
		common.ApiErrorMsg(c, "风格名和风格提示词必填")
		return
	}
	// 用户侧不接受 status / sort_order
	req.Status, req.SortOrder = nil, nil
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	count, err := model.CountUserStyles(userId)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if count >= model.UserStyleMax {
		common.ApiErrorMsg(c, "自定义风格最多 100 个")
		return
	}
	s := styleFromFields(f)
	s.UserId = userId
	s.Status = model.StyleStatusOn
	if err := s.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, s.OwnerView())
}

// UpdateMyStyle 修改本人风格（PUT /user/style/:id）；已收录的行改完自动回到待审核。
func UpdateMyStyle(c *gin.Context) {
	s, ok := ownedStyle(c, paramInt(c, "id"))
	if !ok {
		return
	}
	var req stylePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	req.Status, req.SortOrder = nil, nil
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有要更新的字段")
		return
	}
	if next := userStyleStatusAfterEdit(s.Status); next != s.Status {
		f["status"] = next
	}
	if err := model.UpdateStyleFields(s.Id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// DeleteMyStyle 删除本人风格（DELETE /user/style/:id），软删除。
func DeleteMyStyle(c *gin.Context) {
	s, ok := ownedStyle(c, paramInt(c, "id"))
	if !ok {
		return
	}
	if err := model.DB.Where("id = ?", s.Id).Delete(&model.Style{}).Error; err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// SubmitMyStyle 投稿（POST /user/style/:id/submit）：1 → 3，需要封面。
func SubmitMyStyle(c *gin.Context) {
	s, ok := ownedStyle(c, paramInt(c, "id"))
	if !ok {
		return
	}
	next, msg := userStyleSubmitTransition(s)
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if err := model.UpdateStyleFields(s.Id, map[string]any{"status": next}); err != nil {
		respondModelError(c, err, "投稿失败")
		return
	}
	s.Status = next
	common.ApiSuccess(c, s.OwnerView())
}

// WithdrawMyStyle 撤回投稿 / 收录（POST /user/style/:id/withdraw）：3 或 4 → 1。
func WithdrawMyStyle(c *gin.Context) {
	s, ok := ownedStyle(c, paramInt(c, "id"))
	if !ok {
		return
	}
	next, msg := userStyleWithdrawTransition(s)
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if err := model.UpdateStyleFields(s.Id, map[string]any{"status": next}); err != nil {
		respondModelError(c, err, "撤回失败")
		return
	}
	s.Status = next
	common.ApiSuccess(c, s.OwnerView())
}

// ----- 纯函数状态机（便于单测）-----

// userStyleStatusAfterEdit 用户编辑后的状态：已收录回到待审核，其它不变。
func userStyleStatusAfterEdit(status int) int {
	if status == model.StyleStatusAccepted {
		return model.StyleStatusPending
	}
	return status
}

// userStyleSubmitTransition 投稿：仅 status=1 且有封面 → 3。
func userStyleSubmitTransition(s *model.Style) (int, string) {
	switch s.Status {
	case model.StyleStatusPending:
		return 0, "已投稿，请等待审核"
	case model.StyleStatusAccepted:
		return 0, "已收录"
	case model.StyleStatusOn:
		if strings.TrimSpace(s.CoverUrl) == "" {
			return 0, "投稿需要一张封面"
		}
		return model.StyleStatusPending, ""
	default:
		return 0, "当前状态不能投稿"
	}
}

// userStyleWithdrawTransition 撤回：3 / 4 → 1。
func userStyleWithdrawTransition(s *model.Style) (int, string) {
	switch s.Status {
	case model.StyleStatusPending, model.StyleStatusAccepted:
		return model.StyleStatusOn, ""
	default:
		return 0, "该风格没有投稿"
	}
}
