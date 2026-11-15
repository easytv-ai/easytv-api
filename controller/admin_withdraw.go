package controller

import (
	"errors"
	"fmt"
	"strconv"
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
		s, err := strconv.Atoi(v)
		if err != nil {
			common.ApiErrorMsg(c, "无效的状态筛选")
			return
		}
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
	req.Remark = strings.TrimSpace(req.Remark)
	if utf8.RuneCountInString(req.Remark) > withdrawTextMax {
		common.ApiErrorMsg(c, "审核备注不能超过 255 字")
		return
	}

	switch req.Action {
	case "approve":
		if err := model.SetWithdrawPaying(id, adminId, req.Remark); err != nil {
			common.ApiErrorMsg(c, withdrawStatusError(err, "只有待审核的提现单可以审核"))
			return
		}
		common.SysLog(fmt.Sprintf("admin %d approved withdraw %d", adminId, id))
		common.ApiSuccess(c, nil)
	case "reject":
		if err := model.SetWithdrawRejected(id, adminId, req.Remark); err != nil {
			common.ApiErrorMsg(c, withdrawStatusError(err, "只有待审核的提现单可以审核"))
			return
		}
		common.SysLog(fmt.Sprintf("admin %d rejected withdraw %d", adminId, id))
		if msg := refundAfterStatusChange(id, "提现驳回退回", "已驳回"); msg != "" {
			common.ApiErrorMsg(c, msg)
			return
		}
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
	txNo, errMsg := withdrawTransactionNo(req.TransactionNo)
	if errMsg != "" {
		common.ApiErrorMsg(c, errMsg)
		return
	}
	if err := model.SetWithdrawPaid(id, txNo); err != nil {
		common.ApiErrorMsg(c, withdrawStatusError(err, "只有打款中的提现单可以标记已打款"))
		return
	}
	common.SysLog(fmt.Sprintf("admin %d marked withdraw %d paid, transaction_no=%s", c.GetInt("id"), id, txNo))
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
		common.ApiErrorMsg(c, withdrawStatusError(err, "只有打款中的提现单可以标记失败"))
		return
	}
	common.SysLog(fmt.Sprintf("admin %d marked withdraw %d failed", c.GetInt("id"), id))
	if msg := refundAfterStatusChange(id, "提现打款失败退回", "已标记打款失败"); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	common.ApiSuccess(c, nil)
}

// withdrawTextMax 审核备注、失败原因的字数上限（对应列宽）。
const withdrawTextMax = 255

// withdrawTransactionNoMax 第三方流水号的长度上限。
const withdrawTransactionNoMax = 64

// withdrawStatusError 状态流转失败的对外文案：状态不对给人话，其余只说失败，数据库原文进日志不外露。
func withdrawStatusError(err error, wrongStatus string) string {
	if errors.Is(err, model.ErrWithdrawStatus) {
		return wrongStatus
	}
	common.SysError("withdraw status update failed: " + err.Error())
	return "操作失败，请稍后重试"
}

// withdrawTransactionNo 标记已打款必须带第三方流水号，事后对账靠它。
func withdrawTransactionNo(raw string) (string, string) {
	no := strings.TrimSpace(raw)
	if no == "" {
		return "", "请填写打款流水号"
	}
	if utf8.RuneCountInString(no) > withdrawTransactionNoMax {
		return "", "打款流水号不能超过 64 个字符"
	}
	return no, ""
}

// refundAfterStatusChange 状态已经改完后把冻结额退回申请人。
// 状态更新与退回不在同一个事务里：退回没成功时不能回成功，要让操作人知道这一单的钱还没退，
// 并留下带单号的错误日志供人工补退。返回空串表示已退回。
func refundAfterStatusChange(id int, remark, done string) string {
	w, err := model.GetWithdrawById(id)
	if err == nil {
		err = refundWithdrawBalance(w, remark)
	}
	if err == nil {
		return ""
	}
	no := ""
	if w != nil {
		no = w.WithdrawNo
	}
	common.SysError(fmt.Sprintf("withdraw %d (%s) status changed but refund failed, needs manual refund: %v", id, no, err))
	return fmt.Sprintf("提现单%s，但余额没有退回成功，请不要重复操作并联系技术人工补退（提现单 %d %s）", done, id, no)
}

func withdrawFailureMsg(raw string) (string, string) {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "", "请填写失败原因"
	}
	if utf8.RuneCountInString(msg) > withdrawTextMax {
		return "", "失败原因不能超过 255 字"
	}
	return msg, ""
}

// refundWithdrawBalance 把冻结额退回申请人：推广员退 owner_id，伙伴退到 partner.user_id，创作者退技能积分。
// 任何一步没成功都返回错误，由调用方告诉操作人。
func refundWithdrawBalance(w *model.Withdraw, remark string) error {
	if w == nil || w.Amount <= 0 {
		return nil
	}
	if w.OwnerType == model.WithdrawOwnerTypeCreator {
		return service.RefundCreatorCashWithdraw(w, remark)
	}
	userId := w.OwnerId
	if w.OwnerType == model.WithdrawOwnerTypePartner {
		p, err := model.GetPartnerById(w.OwnerId)
		if err != nil || p.UserId <= 0 {
			return fmt.Errorf("partner %d has no user to refund", w.OwnerId)
		}
		userId = p.UserId
	}
	quotaToReturn := int(decimal.NewFromFloat(w.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	if quotaToReturn <= 0 {
		return nil
	}
	if err := model.IncreaseUserQuota(userId, quotaToReturn); err != nil {
		return fmt.Errorf("increase quota for user %d: %w", userId, err)
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
	// 余额已退回，流水没记上不算退回失败，只留日志
	if err := bill.Insert(); err != nil {
		common.SysError(fmt.Sprintf("withdraw refund bill insert failed id=%d user=%d: %v", w.Id, userId, err))
	}
	return nil
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
