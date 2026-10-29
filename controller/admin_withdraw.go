package controller

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// AdminGetWithdraws 提现列表（GET /admin/withdraw?status=&page=&page_size=）。
func AdminGetWithdraws(c *gin.Context) {
	page := parsePageInfo(c)
	var status *int
	if v := c.Query("status"); v != "" {
		var s int
		fmt.Sscanf(v, "%d", &s)
		status = &s
	}
	list, total, err := model.GetAllWithdraws(status, page)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": list,
		"total": total,
	})
}

// AdminAuditWithdraw 审核提现（POST /admin/withdraw/:id/audit）。
// action: approve（通过进入打款中）/ reject（驳回并退回余额）。
func AdminAuditWithdraw(c *gin.Context) {
	var id int
	fmt.Sscanf(c.Param("id"), "%d", &id)
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的提现单 ID")
		return
	}
	var req struct {
		Action string `json:"action"` // approve / reject
		Remark string `json:"remark"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	adminId := c.GetInt("id")

	switch req.Action {
	case "approve":
		if err := model.SetWithdrawPaying(id, adminId, req.Remark); err != nil {
			common.ApiErrorMsg(c, "审核失败："+err.Error())
			return
		}
		common.SysLog(fmt.Sprintf("admin %d approved withdraw %d", adminId, id))
		common.ApiSuccess(c, nil)
	case "reject":
		if err := model.SetWithdrawRejected(id, adminId, req.Remark); err != nil {
			common.ApiErrorMsg(c, "审核失败："+err.Error())
			return
		}
		if w, err := model.GetWithdrawById(id); err == nil {
			refundWithdrawBalance(w, "提现驳回退回")
		}
		common.SysLog(fmt.Sprintf("admin %d rejected withdraw %d", adminId, id))
		common.ApiSuccess(c, nil)
	default:
		common.ApiErrorMsg(c, "无效的操作")
	}
}

// AdminMarkWithdrawPaid 打款完成（POST /admin/withdraw/:id/paid，需填第三方流水号）。
func AdminMarkWithdrawPaid(c *gin.Context) {
	var id int
	fmt.Sscanf(c.Param("id"), "%d", &id)
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的提现单 ID")
		return
	}
	var req struct {
		TransactionNo string `json:"transaction_no"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := model.SetWithdrawPaid(id, req.TransactionNo); err != nil {
		common.ApiErrorMsg(c, "更新失败："+err.Error())
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminMarkWithdrawFailed 打款失败（POST /admin/withdraw/:id/failed）。打款中 → 打款失败，并退回冻结额。
func AdminMarkWithdrawFailed(c *gin.Context) {
	var id int
	fmt.Sscanf(c.Param("id"), "%d", &id)
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的提现单 ID")
		return
	}
	var req struct {
		FailureMsg string `json:"failure_msg"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	msg, errMsg := withdrawFailureMsg(req.FailureMsg)
	if errMsg != "" {
		common.ApiErrorMsg(c, errMsg)
		return
	}
	if err := model.SetWithdrawFailed(id, msg); err != nil {
		if errors.Is(err, model.ErrWithdrawStatus) {
			common.ApiErrorMsg(c, "只有打款中的提现单可以标记失败")
			return
		}
		common.ApiErrorMsg(c, "更新失败："+err.Error())
		return
	}
	if w, err := model.GetWithdrawById(id); err == nil {
		refundWithdrawBalance(w, "提现打款失败退回")
	}
	adminId := c.GetInt("id")
	common.SysLog(fmt.Sprintf("admin %d marked withdraw %d failed", adminId, id))
	common.ApiSuccess(c, nil)
}

func withdrawFailureMsg(raw string) (string, string) {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "", "请填写失败原因"
	}
	if utf8.RuneCountInString(msg) > 255 {
		return "", "失败原因不能超过 255 字"
	}
	return msg, ""
}

// refundWithdrawBalance 把冻结额退回申请人：推广员退 owner_id，伙伴退到 partner.user_id，创作者退技能积分。
func refundWithdrawBalance(w *model.Withdraw, remark string) {
	if w == nil || w.Amount <= 0 {
		return
	}
	if w.OwnerType == model.WithdrawOwnerTypeCreator {
		if err := service.RefundCreatorCashWithdraw(w, remark); err != nil {
			common.SysError(fmt.Sprintf("withdraw refund creator failed id=%d user=%d: %v", w.Id, w.OwnerId, err))
		}
		return
	}
	userId := w.OwnerId
	if w.OwnerType == model.WithdrawOwnerTypePartner {
		p, err := model.GetPartnerById(w.OwnerId)
		if err != nil || p.UserId <= 0 {
			common.SysError(fmt.Sprintf("withdraw refund: partner %d has no user", w.OwnerId))
			return
		}
		userId = p.UserId
	}
	quotaToReturn := int(decimal.NewFromFloat(w.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	if quotaToReturn <= 0 {
		return
	}
	if err := model.IncreaseUserQuota(userId, quotaToReturn); err != nil {
		common.SysError(fmt.Sprintf("withdraw refund quota failed id=%d user=%d: %v", w.Id, userId, err))
		return
	}
	balance, _ := model.GetUserQuota(userId)
	bill := &model.PromoterBill{
		UserId:    userId,
		BizType:   model.PromoterBillTypeWithdrawRefund,
		Amount:    w.Amount,
		Balance:   decimal.NewFromInt(int64(balance)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).InexactFloat64(),
		SourceNo:  w.WithdrawNo,
		Remark:    remark,
		CreatedAt: common.GetTimestamp(),
	}
	_ = bill.Insert()
}

// AdminGetBills 全部资金流水（GET /admin/bills?user_id=&page=&page_size=）。
func AdminGetBills(c *gin.Context) {
	page := parsePageInfo(c)
	var userId int
	fmt.Sscanf(c.Query("user_id"), "%d", &userId)
	list, total, err := model.GetAllPromoterBills(userId, page)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": list,
		"total": total,
	})
}
