package controller

import (
	"encoding/json"
	"errors"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// ===== 付费域：公开 + 用户侧（API.md 6.2）=====

// GetBillingPlans 定价页数据（GET /billing/plans）。
func GetBillingPlans(c *gin.Context) {
	plans, err := model.GetMembershipPlans(true)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	packs, err := model.GetPointPacks(true)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"plans": plans,
		"packs": packs,
		"promo": billingPromoView(),
		"pay_methods": gin.H{
			"wechat_native": setting.IsWechatPayEnabled(),
			"alipay_f2f":    setting.IsAlipayEnabled(),
		},
	})
}

// billingPromoView 促销配置 + 会员日（M7 剩余项）。
func billingPromoView() map[string]any {
	promo := model.GetBillingPromo()
	b, _ := json.Marshal(promo)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	out["member_day"] = service.MemberDayInfo()
	return out
}

// GetBillingModelPrices 模型定价（GET /billing/model-prices）。
func GetBillingModelPrices(c *gin.Context) {
	list, err := model.GetModelPrices(true)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.FillModelPricesEffective(list)
	common.ApiSuccess(c, list)
}

// BillingQuote 报价（POST /billing/quote，可匿名；带登录态时积分包按会员价）。
func BillingQuote(c *gin.Context) {
	var req service.QuoteRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	q, err := service.BuildQuote(req, c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, q)
}

// GetMyWallet 钱包总览（GET /user/wallet）。
func GetMyWallet(c *gin.Context) {
	common.ApiSuccess(c, service.WalletView(c.GetInt("id"), teamIdOf(c)))
}

// GetMyMembership 当前会员（GET /user/membership）。
func GetMyMembership(c *gin.Context) {
	common.ApiSuccess(c, model.GetActiveMembership(c.GetInt("id")))
}

// GetMyPointLog 积分流水（GET /user/points/log?type=）。
// 个人空间：自己的全部流水。团队空间（API.md 20）：所有者 / 管理员看团队积分池（owner 账户）里本团队任务的流水，
// 编辑者 / 查看者返回空列表。
func GetMyPointLog(c *gin.Context) {
	filter := model.PointLogFilter{
		UserId: c.GetInt("id"),
		Type:   strings.TrimSpace(c.Query("type")),
	}
	if teamId := teamIdOf(c); teamId > 0 {
		owner := service.TeamOwnerOf(teamId)
		if teamPermissionOf(c) != model.TeamPermAdmin || owner == 0 {
			pagedResponse(c, []*model.PointLog{}, 0)
			return
		}
		filter.UserId, filter.TeamTasks = owner, teamId
	}
	list, total, err := model.GetPointLogs(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// GetMyPointLots 未过期批次（GET /user/points/lots）。
func GetMyPointLots(c *gin.Context) {
	list, err := model.GetUserLots(c.GetInt("id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

// CreateMyOrder 下单并发起支付（POST /user/order）。
func CreateMyOrder(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		service.QuoteRequest
		PaymentMethod string `json:"payment_method"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.PaymentMethod == "" {
		req.PaymentMethod = model.PaymentMethodWechatNative
	}
	o, q, err := service.CreateOrder(userId, req.QuoteRequest)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	pay, err := createPaymentForOrder(c.Request.Context(), o, req.PaymentMethod)
	if err != nil {
		// 支付渠道失败：订单保留 pending，允许换渠道重试
		common.SysError("order payment failed: " + err.Error())
		common.ApiSuccess(c, gin.H{
			"order":     o,
			"quote":     q,
			"pay":       nil,
			"pay_error": err.Error(),
		})
		return
	}
	common.ApiSuccess(c, gin.H{
		"order": o,
		"quote": q,
		"pay":   pay,
	})
}

// PayMyOrder 对已有待支付订单重新发起支付（POST /user/order/:id/pay）。
func PayMyOrder(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的订单 ID")
		return
	}
	var req struct {
		PaymentMethod string `json:"payment_method"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	if req.PaymentMethod == "" {
		req.PaymentMethod = model.PaymentMethodWechatNative
	}
	o, err := model.GetUserOrder(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if o.Status != model.OrderStatusPending {
		common.ApiErrorMsg(c, "订单不是待支付状态")
		return
	}
	pay, err := createPaymentForOrder(c.Request.Context(), o, req.PaymentMethod)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"order": o, "pay": pay})
}

// GetMyOrders 我的订单（GET /user/order?status=）。
func GetMyOrders(c *gin.Context) {
	list, total, err := model.GetOrders(model.OrderFilter{
		UserId: c.GetInt("id"),
		Status: strings.TrimSpace(c.Query("status")),
	}, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// GetMyOrder 订单详情（GET /user/order/:id，支付页轮询）。
func GetMyOrder(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的订单 ID")
		return
	}
	o, err := model.GetUserOrder(c.GetInt("id"), id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, o)
}

// CloseMyOrder 用户取消待支付订单（POST /user/order/:id/close）。
func CloseMyOrder(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的订单 ID")
		return
	}
	if err := model.CloseOrder(c.GetInt("id"), id, "用户取消"); err != nil {
		if errors.Is(err, model.ErrOrderStatus) {
			common.ApiErrorMsg(c, "订单不是待支付状态")
			return
		}
		respondModelError(c, err, "关闭失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// GetMyUsage 套餐额度与用量（GET /user/usage）：并发路数、云存储，团队版按团队共享统计。
func GetMyUsage(c *gin.Context) {
	common.ApiSuccess(c, service.UsageView(c.GetInt("id"), teamIdOf(c)))
}

// GetMyTeamUsage 团队用量（GET /user/team/usage）：成员近 30 天任务数、积分消耗、存储占用。
func GetMyTeamUsage(c *gin.Context) {
	view := service.TeamUsageView(c.GetInt("id"))
	if view == nil {
		common.ApiErrorMsg(c, "当前不在生效的团队版会员中")
		return
	}
	common.ApiSuccess(c, view)
}
