package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// ===== 提现 =====

// WithdrawRequest 提现申请。
type WithdrawRequest struct {
	Amount      float64 `json:"amount"`       // 提现金额（元）
	Way         string  `json:"way"`          // alipay / wechat / bank
	AccountInfo string  `json:"account_info"` // JSON 收款账户
}

// ApplyWithdraw 用户发起提现（POST /withdraw）。提现从用户余额（quota）按 QuotaPerUnit 折算扣减。
func ApplyWithdraw(c *gin.Context) {
	userId := c.GetInt("id")
	var req WithdrawRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Amount <= 0 {
		common.ApiErrorMsg(c, "提现金额必须大于 0")
		return
	}
	if req.Way == "" {
		req.Way = "alipay"
	}
	switch req.Way {
	case "alipay", "wechat", "bank":
	default:
		common.ApiErrorMsg(c, "不支持的提现方式")
		return
	}
	if req.AccountInfo == "" {
		common.ApiErrorMsg(c, "请填写收款账户信息")
		return
	}
	// 校验账户信息为合法 JSON
	var accountCheck map[string]any
	if err := json.Unmarshal([]byte(req.AccountInfo), &accountCheck); err != nil {
		common.ApiErrorMsg(c, "收款账户信息格式不正确")
		return
	}

	// 校验余额：quota 折算余额（元）需大于等于提现金额
	quota, err := model.GetUserQuota(userId)
	if err != nil {
		common.ApiErrorMsg(c, "获取余额失败")
		return
	}
	balanceYuan := decimal.NewFromFloat(float64(quota)).Div(decimal.NewFromFloat(common.QuotaPerUnit))
	if balanceYuan.LessThan(decimal.NewFromFloat(req.Amount)) {
		common.ApiErrorMsg(c, "余额不足")
		return
	}

	withdrawNo := fmt.Sprintf("WD%s%d", common.GetRandomString(8), time.Now().Unix())
	w := &model.Withdraw{
		WithdrawNo:   withdrawNo,
		OwnerType:    model.WithdrawOwnerTypePromoter, // 平台用户提现（partner 预留）
		OwnerId:      userId,
		Amount:       req.Amount,
		Fee:          0,
		ActualAmount: req.Amount,
		Way:          req.Way,
		AccountInfo:  req.AccountInfo,
		Status:       model.WithdrawStatusPending,
		CreatedAt:    time.Now().Unix(),
	}
	if err := w.Insert(); err != nil {
		common.SysError("withdraw insert failed: " + err.Error())
		common.ApiErrorMsg(c, "提现申请失败")
		return
	}

	// 冻结余额：扣减 quota，提现被驳回时退回
	quotaToFreeze := int(decimal.NewFromFloat(req.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	if err := model.DecreaseUserQuota(userId, quotaToFreeze); err != nil {
		common.ApiErrorMsg(c, "余额扣减失败")
		return
	}

	// 写资金流水（提现出账）
	bill := &model.PromoterBill{
		UserId:    userId,
		BizType:   model.PromoterBillTypeWithdraw,
		Amount:    -req.Amount,
		Balance:   balanceYuan.Sub(decimal.NewFromFloat(req.Amount)).InexactFloat64(),
		SourceNo:  withdrawNo,
		CreatedAt: time.Now().Unix(),
		Remark:    "提现申请（冻结）",
	}
	_ = bill.Insert()

	common.ApiSuccess(c, gin.H{
		"withdraw_no": withdrawNo,
		"status":      model.WithdrawStatusPending,
	})
}

// GetMyWithdraws 用户提现记录（GET /withdraw/self）。
func GetMyWithdraws(c *gin.Context) {
	userId := c.GetInt("id")
	page := parsePageInfo(c)
	list, total, err := model.GetUserWithdraws(userId, page)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": list,
		"total": total,
	})
}

// ===== 资金流水 =====

// GetMyBills 用户资金流水（GET /bills/self，biz_type 可选过滤）。
func GetMyBills(c *gin.Context) {
	userId := c.GetInt("id")
	page := parsePageInfo(c)
	// biz_type 过滤（可选，逗号分隔，如 1,2,6）
	var bizTypes []int
	if v := strings.TrimSpace(c.Query("biz_type")); v != "" {
		for _, part := range strings.Split(v, ",") {
			var bt int
			if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &bt); err == nil && bt > 0 {
				bizTypes = append(bizTypes, bt)
			}
		}
	}
	list, total, err := model.GetPromoterBills(userId, bizTypes, page)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	fromIds := make([]int, 0, len(list))
	for _, b := range list {
		if b.FromUserId > 0 {
			fromIds = append(fromIds, b.FromUserId)
		}
	}
	names := model.GetUserNicknames(fromIds)
	items := make([]gin.H, 0, len(list))
	for _, b := range list {
		nick := names[b.FromUserId]
		if nick != "" {
			nick = model.MaskNickname(nick)
		}
		items = append(items, gin.H{
			"id":            b.Id,
			"biz_type":      b.BizType,
			"amount":        b.Amount,
			"balance":       b.Balance,
			"from_user_id":  b.FromUserId,
			"from_nickname": nick,
			"source_no":     b.SourceNo,
			"source_amount": b.SourceAmount,
			"rate":          b.Rate,
			"status":        b.Status,
			"created_at":    b.CreatedAt,
			"remark":        b.Remark,
		})
	}
	common.ApiSuccess(c, gin.H{
		"items": items,
		"total": total,
	})
}

// GetMyQuota 当前余额（GET /user/quota）。
func GetMyQuota(c *gin.Context) {
	userId := c.GetInt("id")
	quota, err := model.GetUserQuota(userId)
	if err != nil {
		common.ApiErrorMsg(c, "获取余额失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"quota":        quota,
		"balance_yuan": decimal.NewFromFloat(float64(quota)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).InexactFloat64(),
	})
}
