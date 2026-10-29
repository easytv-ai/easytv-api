package controller

import (
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ResolveInviteCode GET /invite/:code 邀请码解析（匿名，落地页 /i/:code 用）。
func ResolveInviteCode(c *gin.Context) {
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	id := model.GetUserIdByAffCode(code)
	if id <= 0 {
		common.ApiErrorMsg(c, "邀请码不存在")
		return
	}
	u, err := model.GetUserById(id)
	if err != nil || u == nil || u.Status != constant.UserStatusEnabled {
		common.ApiErrorMsg(c, "邀请码不存在")
		return
	}
	nickname := u.Nickname
	if nickname == "" {
		nickname = model.MaskNickname(u.Username)
	}
	common.ApiSuccess(c, gin.H{
		"user_id":  u.Id,
		"nickname": nickname,
		"aff_code": u.AffCode,
	})
}

// GetInviteSummary GET /user/invite/summary
func GetInviteSummary(c *gin.Context) {
	s, err := service.GetInviteSummary(c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, "读取邀请信息失败")
		return
	}
	common.ApiSuccess(c, s)
}

// GetInviteRecords GET /user/invite/records 分页邀请记录（推广返利明细）。
func GetInviteRecords(c *gin.Context) {
	records, total, err := model.ListInviteRecords(c.GetInt("id"), c.Query("keyword"), parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "读取邀请记录失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": records,
		"total": total,
	})
}

// AdminGetUserInvites GET /admin/user/:id/invites 该用户邀请的人与返利流水。
func AdminGetUserInvites(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	records, err := model.GetInviteRecords(id, 200)
	if err != nil {
		common.ApiErrorMsg(c, "读取失败")
		return
	}
	bills, total, err := model.GetPromoterBills(id, []int{
		model.PromoterBillTypeRegisterRebate, model.PromoterBillTypeRechargeRebate, model.PromoterBillTypeRebateRevoke,
	}, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "读取失败")
		return
	}
	var blocked bool
	model.DB.Model(&model.User{}).Select("invite_blocked").Where("id = ?", id).Scan(&blocked)
	common.ApiSuccess(c, gin.H{
		"totals":         model.GetInviteTotals(id),
		"records":        records,
		"bills":          bills,
		"bills_total":    total,
		"invite_blocked": blocked,
	})
}

// AdminSetInviteBlock PUT /admin/user/:id/invite-block {"blocked": true}
func AdminSetInviteBlock(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req struct {
		Blocked bool `json:"blocked"`
	}
	if id <= 0 || common.DecodeJson(c.Request.Body, &req) != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", id).Update("invite_blocked", req.Blocked).Error; err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	common.ApiSuccess(c, nil)
}
