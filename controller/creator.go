package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 技能上架与创作者分成（API.md 8.3，T26）=====

// SubmitMySkill POST /user/skill/:id/submit
func SubmitMySkill(c *gin.Context) {
	userId := c.GetInt("id")
	s, err := model.GetUserSkill(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if s.ReviewStatus == model.SkillReviewPending || s.ReviewStatus == model.SkillReviewPublished {
		common.ApiErrorMsg(c, "技能已提交或已上架")
		return
	}
	if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Prompt) == "" {
		common.ApiErrorMsg(c, "上架前请填写标题和提示词")
		return
	}
	if strings.TrimSpace(s.CoverUrl) == "" {
		common.ApiErrorMsg(c, "上架前请上传封面")
		return
	}
	if err := model.UpdateSkillFields(s.Id, map[string]any{"review_status": model.SkillReviewPending, "review_remark": "", "submitted_at": common.GetTimestamp()}); err != nil {
		common.ApiErrorMsg(c, "提交失败")
		return
	}
	service.Track(userId, "skill_submit", map[string]any{"skill_id": s.Id})
	common.ApiSuccess(c, nil)
}

// WithdrawMySkill POST /user/skill/:id/withdraw 撤回上架 / 待审
func WithdrawMySkill(c *gin.Context) {
	userId := c.GetInt("id")
	s, err := model.GetUserSkill(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if err := model.UpdateSkillFields(s.Id, map[string]any{"review_status": model.SkillReviewPrivate}); err != nil {
		common.ApiErrorMsg(c, "操作失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// GetCreatorSummary GET /user/creator/summary
func GetCreatorSummary(c *gin.Context) {
	sum, err := service.GetCreatorSummary(c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, "读取失败")
		return
	}
	common.ApiSuccess(c, sum)
}

// ExchangeCreatorRevenue POST /user/creator/exchange { points }
func ExchangeCreatorRevenue(c *gin.Context) {
	var req struct {
		Points int `json:"points"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := service.ExchangeCreatorRevenue(c.GetInt("id"), req.Points); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"withdrawable":   service.CreatorWithdrawable(c.GetInt("id")),
		"cash_available": service.CreatorCashYuan(service.CreatorWithdrawable(c.GetInt("id"))),
	})
}

// ApplyCreatorWithdraw POST /user/creator/withdraw { amount, way, account_info }
func ApplyCreatorWithdraw(c *gin.Context) {
	var req service.CreatorCashWithdrawRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	userId := c.GetInt("id")
	withdrawNo, err := service.ApplyCreatorCashWithdraw(userId, req)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"withdraw_no":    withdrawNo,
		"status":         model.WithdrawStatusPending,
		"withdrawable":   service.CreatorWithdrawable(userId),
		"cash_available": service.CreatorCashYuan(service.CreatorWithdrawable(userId)),
	})
}

// GetCreatorWithdraws GET /user/creator/withdraws
func GetCreatorWithdraws(c *gin.Context) {
	list, total, err := model.GetCreatorWithdraws(c.GetInt("id"), parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": list,
		"total": total,
	})
}

// GetCreatorLeaderboard GET /creator/leaderboard?period=week|month
func GetCreatorLeaderboard(c *gin.Context) {
	period := c.Query("period")
	if period != "month" {
		period = "week"
	}
	common.ApiSuccess(c, gin.H{"period": period, "items": service.GetCreatorLeaderboard(period)})
}

// AdminReviewSkill PUT /admin/skill/:id/review { action: approve|reject|unpublish, remark?, revenue_share? }
func AdminReviewSkill(c *gin.Context) {
	id := paramInt(c, "id")
	var req struct {
		Action       string   `json:"action"`
		Remark       string   `json:"remark"`
		RevenueShare *float64 `json:"revenue_share"`
	}
	if id == 0 || common.DecodeJson(c.Request.Body, &req) != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	s, err := model.GetSkillById(id)
	if err != nil {
		common.ApiErrorMsg(c, "技能不存在")
		return
	}
	if s.UserId == model.SkillUserOfficial {
		common.ApiErrorMsg(c, "官方技能不需要审核")
		return
	}
	fields := map[string]any{"review_remark": trimLimit(req.Remark, 255)}
	if req.RevenueShare != nil {
		if *req.RevenueShare < 0 || *req.RevenueShare > 1 {
			common.ApiErrorMsg(c, "分成比例需在 0~1 之间")
			return
		}
		fields["revenue_share"] = *req.RevenueShare
	}
	title, content := "", ""
	switch req.Action {
	case "approve":
		fields["review_status"] = model.SkillReviewPublished
		fields["published_at"] = common.GetTimestamp()
		fields["status"] = model.SkillStatusOn
		title, content = "技能已上架", "你的技能「"+s.Title+"」已通过审核，现在所有人都能使用它，调用会按比例给你分成。"
	case "reject":
		if strings.TrimSpace(req.Remark) == "" {
			common.ApiErrorMsg(c, "驳回请填写原因")
			return
		}
		fields["review_status"] = model.SkillReviewRejected
		title, content = "技能未通过审核", "「"+s.Title+"」未通过："+trimLimit(req.Remark, 200)
	case "unpublish":
		fields["review_status"] = model.SkillReviewPrivate
		title, content = "技能已下架", "「"+s.Title+"」已被下架，可修改后重新提交。"
	default:
		common.ApiErrorMsg(c, "无效的操作")
		return
	}
	if err := model.UpdateSkillFields(id, fields); err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	model.PushNotification(s.UserId, model.NotificationTypeSystem, title, content, "/settings?tab=creator")
	common.ApiSuccess(c, nil)
}
