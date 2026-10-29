package controller

import (
	"fmt"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// RechargeRequest 充值下单请求（amount 为充值额度/金额，由 payMoney 换算实付）。
type RechargeRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	TeamId        int    `json:"team_id"` // >0 即团队充值（须为该团队 owner），仅会员类充值支持
}

// GetRechargeInfo 充值配置信息（前端充值页展示，兼容 easyapi 字段）。
func GetRechargeInfo(c *gin.Context) {
	payMethods := []gin.H{}

	// 微信 Native
	if setting.IsWechatPayEnabled() {
		payMethods = append(payMethods, gin.H{
			"name":         "微信支付",
			"type":         model.PaymentMethodWechatNative,
			"color":        "rgba(var(--semi-green-5), 1)",
			"min_recharge": setting.GetMinRecharge(),
		})
	}
	// 支付宝
	if setting.IsAlipayEnabled() {
		if setting.IsAlipayF2FEnabled() {
			payMethods = append(payMethods, gin.H{
				"name":         "支付宝当面付",
				"type":         model.PaymentMethodAlipayF2F,
				"color":        "rgba(var(--semi-blue-5), 1)",
				"min_recharge": setting.GetMinRecharge(),
			})
		}
		if setting.IsAlipayPCWebEnabled() {
			payMethods = append(payMethods, gin.H{
				"name":         "支付宝",
				"type":         model.PaymentMethodAlipayPCWeb,
				"color":        "rgba(var(--semi-blue-5), 1)",
				"min_recharge": setting.GetMinRecharge(),
			})
		}
	}

	data := gin.H{
		"enable_wechat_native_recharge": setting.IsWechatPayEnabled(),
		"enable_alipay_f2f_recharge":    setting.IsAlipayF2FEnabled(),
		"enable_alipay_pc_recharge":     setting.IsAlipayPCWebEnabled(),
		"min_recharge":                  setting.GetMinRecharge(),
		"amount_options":                getRechargeAmountOptions(),
		"pay_methods":                   payMethods,
	}
	common.ApiSuccess(c, data)
}

// getRechargeAmountOptions 充值面额（从 setting 读取，逗号分隔）。
func getRechargeAmountOptions() []int64 {
	raw := model.GetSetting(setting.KeyAmountOptions)
	if raw == "" {
		return []int64{10, 20, 50, 100, 200, 500}
	}
	var options []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var n int64
		if _, err := fmt.Sscanf(part, "%d", &n); err == nil && n > 0 {
			options = append(options, n)
		}
	}
	if len(options) == 0 {
		return []int64{10, 20, 50, 100, 200, 500}
	}
	return options
}

// getPayMoney 计算实付金额：amount（充值额度/元）× 倍率。当前 1:1，保留扩展折扣位。
func getPayMoney(amount int64) float64 {
	dAmount := decimal.NewFromInt(amount)
	return dAmount.InexactFloat64()
}

// getTradeNo 生成商户订单号。
func getTradeNo(userId int) string {
	return fmt.Sprintf("USR%dNO%s%d", userId, common.GetRandomString(6), time.Now().Unix())
}

// RequestRecharge 统一充值入口（按 payment_method 分派到微信/支付宝）。
func RequestRecharge(c *gin.Context) {
	var req RechargeRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Amount <= 0 {
		common.ApiErrorMsg(c, "充值数量必须大于 0")
		return
	}
	minRecharge := int64(setting.GetMinRecharge())
	if req.Amount < minRecharge {
		common.ApiErrorMsg(c, fmt.Sprintf("充值数量不能小于 %d", minRecharge))
		return
	}
	if req.PaymentMethod == "" {
		req.PaymentMethod = model.PaymentMethodWechatNative
	}

	switch req.PaymentMethod {
	case model.PaymentMethodWechatNative:
		requestWechatNativePay(c, req.Amount, req.TeamId)
	case model.PaymentMethodAlipayF2F, model.PaymentMethodAlipayPCWeb:
		requestAlipayPay(c, req.Amount, req.PaymentMethod, req.TeamId)
	default:
		common.ApiErrorMsg(c, "不支持的支付方式")
	}
}

// TopupStatusRequest 充值状态查询入参。
type TopupStatusRequest struct {
	TradeNo string `json:"trade_no"`
}

// RequestTopupStatus 充值订单状态（POST /user/recharge/status）。
// pending 时主动向渠道查单（支付宝 TradeQuery / 微信 Native 查单），已支付则补入账，防 webhook 漏单。
func RequestTopupStatus(c *gin.Context) {
	var req TopupStatusRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || strings.TrimSpace(req.TradeNo) == "" {
		common.ApiErrorMsg(c, "无效的单号")
		return
	}
	userId := c.GetInt("id")
	topUp, err := model.GetRechargeByTradeNo(strings.TrimSpace(req.TradeNo))
	if err != nil {
		common.ApiErrorMsg(c, "订单不存在")
		return
	}
	if topUp.UserId != userId {
		common.ApiErrorMsg(c, "无权访问")
		return
	}
	if topUp.Status == common.RechargeStatusSuccess {
		common.ApiSuccess(c, gin.H{"status": "success"})
		return
	}
	if topUp.Status == common.RechargeStatusPending {
		switch topUp.PaymentProvider {
		case model.PaymentProviderAlipayF2F, model.PaymentProviderAlipayPCWeb:
			if completeAlipayRechargeByQuery(c.Request.Context(), topUp) {
				common.ApiSuccess(c, gin.H{"status": "success"})
				return
			}
		case model.PaymentProviderWechatNative:
			if queryAndCompleteWechatNativeRecharge(c.Request.Context(), topUp) {
				common.ApiSuccess(c, gin.H{"status": "success"})
				return
			}
		}
	}
	common.ApiSuccess(c, gin.H{"status": "pending"})
}

// GetUserRecharges 用户充值记录（GET /user/recharge/self）。
func GetUserRecharges(c *gin.Context) {
	userId := c.GetInt("id")
	page := parsePageInfo(c)
	recharges, total, err := model.GetUserRecharges(userId, page)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": recharges,
		"total": total,
	})
}

// GetRechargesByPage 用户充值记录分页（兼容 /recharge/self?page=&page_size=）。
func parsePageInfo(c *gin.Context) *common.PageInfo {
	page := 1
	pageSize := 10
	if v := c.Query("page"); v != "" {
		fmt.Sscanf(v, "%d", &page)
	}
	if v := c.Query("page_size"); v != "" {
		fmt.Sscanf(v, "%d", &pageSize)
	}
	return &common.PageInfo{Page: page, PageSize: pageSize}
}

// LockOrder 订单级分布式锁（Redis SETNX），防 webhook 与主动查单并发入账。
func LockOrder(tradeNo string) bool {
	ok, err := common.RedisClient.SetNX(common.RedisCtx, "easytv:orderlock:"+tradeNo, 1, 60*time.Second).Result()
	if err != nil {
		common.SysError("lock order failed: " + err.Error())
		return true // 加锁失败时放行，靠订单状态幂等兜底
	}
	return ok
}

// UnlockOrder 释放订单锁。
func UnlockOrder(tradeNo string) {
	_ = common.RedisDel("easytv:orderlock:" + tradeNo)
}
