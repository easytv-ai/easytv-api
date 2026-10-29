package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/smartwalle/alipay/v3"
)

var (
	alipayClientCache     *alipay.Client
	alipayClientCacheProd bool // 缓存对应的环境：true=正式，false=沙箱
)

// GetAlipayClient 返回证书模式的支付宝客户端（AppId + 应用私钥 + 支付宝公钥证书 +
// 应用公钥证书 + 根证书），供收款（alipay.trade.precreate / alipay.trade.page.pay）、
// 查单（alipay.trade.query）与退款等场景复用。与 easyapi-ai-api 对齐，保留
// AlipayIsProduction 沙箱开关与实例缓存作为优化。
// 需配置 AlipayAppId、AlipayAppPrivateKey、AlipayPublicKey（支付宝公钥证书）、
// AlipayAppCertPublicKey、AlipayRootCert。
func GetAlipayClient() (*alipay.Client, error) {
	isProd := setting.IsAlipayProduction()
	if alipayClientCache != nil && alipayClientCacheProd == isProd {
		return alipayClientCache, nil
	}
	appId := strings.TrimSpace(model.GetSetting(setting.KeyAlipayAppId))
	privateKey := strings.TrimSpace(model.GetSetting(setting.KeyAlipayAppPrivateKey))
	publicKey := strings.TrimSpace(model.GetSetting(setting.KeyAlipayPublicKey))
	appCert := strings.TrimSpace(model.GetSetting(setting.KeyAlipayAppCertPublicKey))
	rootCert := strings.TrimSpace(model.GetSetting(setting.KeyAlipayRootCert))
	if appId == "" || privateKey == "" || publicKey == "" {
		return nil, errors.New("alipay config is not fully set")
	}
	if appCert == "" || rootCert == "" {
		return nil, errors.New("alipay certificate config is not fully set")
	}
	client, err := alipay.New(appId, privateKey, isProd)
	if err != nil {
		return nil, err
	}
	if err := client.LoadAlipayCertPublicKey(publicKey); err != nil {
		return nil, fmt.Errorf("load alipay cert failed: %w", err)
	}
	if err := client.LoadAppCertPublicKey(appCert); err != nil {
		return nil, fmt.Errorf("load app cert failed: %w", err)
	}
	if err := client.LoadAliPayRootCert(rootCert); err != nil {
		return nil, fmt.Errorf("load alipay root cert failed: %w", err)
	}
	alipayClientCache = client
	alipayClientCacheProd = isProd
	return client, nil
}

// buildAlipayNotifyURL 拼接支付宝异步通知 URL：优先自定义配置，fallback ServerAddress + /alipay/webhook。
func buildAlipayNotifyURL() string {
	notifyURL := strings.TrimSpace(model.GetSetting(setting.KeyAlipayNotifyURL))
	if notifyURL == "" {
		notifyURL = strings.TrimSuffix(model.GetSetting(setting.KeyServerAddress), "/") + "/alipay/webhook"
	}
	return notifyURL
}

// buildAlipayReturnURL 拼接电脑网站支付回跳 URL：优先自定义配置，fallback ServerAddress + /console/recharge。
func buildAlipayReturnURL() string {
	returnURL := strings.TrimSpace(model.GetSetting(setting.KeyAlipayReturnUrl))
	if returnURL == "" {
		returnURL = strings.TrimSuffix(model.GetSetting(setting.KeyServerAddress), "/") + "/console/recharge"
	}
	return returnURL
}

// buildAlipayPagePayURL 构建电脑网站支付（alipay.trade.page.pay）跳转链接，供充值/订单支付复用。
func buildAlipayPagePayURL(tradeNo string, money float64, subject string) (string, error) {
	client, err := GetAlipayClient()
	if err != nil {
		return "", err
	}
	p := alipay.TradePagePay{}
	p.OutTradeNo = tradeNo
	p.Subject = subject
	p.TotalAmount = fmt.Sprintf("%.2f", money)
	p.ProductCode = "FAST_INSTANT_TRADE_PAY"
	if notifyURL := buildAlipayNotifyURL(); notifyURL != "" {
		p.NotifyURL = notifyURL
	}
	if returnURL := buildAlipayReturnURL(); returnURL != "" {
		p.ReturnURL = returnURL
	}
	u, err := client.TradePagePay(p)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// requestAlipayPay 支付宝下单（当面付/电脑网站支付，在统一入口中分派）。
// teamId>0 为团队充值，须为团队 owner（由 ValidateTeamRechargeRequester 校验）。
func requestAlipayPay(c *gin.Context, amount int64, paymentMethod string, teamId int) {
	switch paymentMethod {
	case model.PaymentMethodAlipayF2F:
		if !setting.IsAlipayF2FEnabled() {
			common.ApiErrorMsg(c, "管理员未开启支付宝当面付充值！")
			return
		}
	case model.PaymentMethodAlipayPCWeb:
		if !setting.IsAlipayPCWebEnabled() {
			common.ApiErrorMsg(c, "管理员未开启支付宝充值！")
			return
		}
	default:
		common.ApiErrorMsg(c, "不支持的支付方式")
		return
	}

	userId := c.GetInt("id")
	if teamId > 0 {
		if msg := ValidateTeamRechargeRequester(userId, teamId); msg != "" {
			common.ApiErrorMsg(c, msg)
			return
		}
	}

	payMoney := getPayMoney(amount)
	if payMoney < 0.01 {
		common.ApiErrorMsg(c, "充值金额过低")
		return
	}
	totalFen := int64(math.Round(payMoney * 100))
	if totalFen <= 0 {
		common.ApiErrorMsg(c, "充值金额过低")
		return
	}

	tradeNo := getTradeNo(userId)
	topUp := &model.Recharge{
		UserId:          userId,
		TeamId:          teamId,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		Status:          common.RechargeStatusPending,
		CreateTime:      time.Now().Unix(),
		PaymentMethod:   paymentMethod,
		PaymentProvider: paymentMethod, // 与 easyapi 一致：Method 与 Provider 同值
	}
	if err := topUp.Insert(); err != nil {
		common.SysError("alipay topup insert failed: " + err.Error())
		common.ApiErrorMsg(c, "创建订单失败")
		return
	}
	model.RecordSimpleAudit(c, model.AuditCategoryUsage, "recharge.create",
		fmt.Sprintf("创建支付宝充值订单 %s", tradeNo),
		map[string]interface{}{"amount": amount, "trade_no": tradeNo, "payment_method": paymentMethod})

	client, err := GetAlipayClient()
	if err != nil {
		common.SysError("alipay client init failed: " + err.Error())
		common.ApiErrorMsg(c, "支付宝客户端初始化失败")
		return
	}

	if paymentMethod == model.PaymentMethodAlipayF2F {
		// 当面付：返回二维码内容，前端用 QR 库渲染
		p := alipay.TradePreCreate{}
		p.OutTradeNo = tradeNo
		p.Subject = fmt.Sprintf("充值 %s", common.SystemName)
		p.TotalAmount = fmt.Sprintf("%.2f", payMoney)
		if notifyURL := buildAlipayNotifyURL(); notifyURL != "" {
			p.NotifyURL = notifyURL
		}
		rsp, err := client.TradePreCreate(context.Background(), p)
		if err != nil {
			common.SysError("alipay precreate failed: " + err.Error())
			common.ApiErrorMsg(c, "获取支付二维码失败")
			return
		}
		if !rsp.IsSuccess() {
			common.SysError(fmt.Sprintf("alipay precreate failed: code=%s msg=%s", rsp.Code, rsp.Msg))
			common.ApiErrorMsg(c, "获取支付二维码失败: "+rsp.Msg)
			return
		}
		common.ApiSuccess(c, gin.H{
			"qr_code":  rsp.QRCode,
			"trade_no": tradeNo,
		})
		return
	}

	// 电脑网站支付：返回跳转链接
	payURL, err := buildAlipayPagePayURL(tradeNo, payMoney, fmt.Sprintf("充值 %s", common.SystemName))
	if err != nil {
		common.SysError("alipay page pay failed: " + err.Error())
		common.ApiErrorMsg(c, "获取支付链接失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"payment_url": payURL,
		"trade_no":    tradeNo,
	})
}

// completeAlipayRechargeByQuery 主动查询支付宝订单，已支付则入账（订单锁内幂等）。
// 供 RequestTopupStatus 复用，防 webhook 漏单。
func completeAlipayRechargeByQuery(ctx context.Context, topUp *model.Recharge) bool {
	client, err := GetAlipayClient()
	if err != nil {
		return false
	}
	p := alipay.TradeQuery{}
	p.OutTradeNo = topUp.TradeNo
	rsp, err := client.TradeQuery(ctx, p)
	if err != nil || !rsp.IsSuccess() {
		return false
	}
	if rsp.TradeStatus != alipay.TradeStatusSuccess && rsp.TradeStatus != alipay.TradeStatusFinished {
		return false
	}
	LockOrder(topUp.TradeNo)
	defer UnlockOrder(topUp.TradeNo)
	// 加锁后再查一次，防与 webhook 并发重复入账
	latest, err := model.GetRechargeByTradeNo(topUp.TradeNo)
	if err != nil || latest.Status != common.RechargeStatusPending {
		return false
	}
	if err := service.CompleteRecharge(latest.TradeNo, latest.PaymentProvider, "支付宝充值"); err != nil {
		common.SysError(fmt.Sprintf("alipay query complete failed: %v", err))
		return false
	}
	// 记录充值完成审计
	if user, _ := model.GetUserById(latest.UserId); user != nil {
		model.RecordOperationAudit(nil, user.Id, user.Username, user.Role,
			model.AuditCategoryUsage, "recharge.complete",
			fmt.Sprintf("支付宝充值完成(查单) %s", latest.TradeNo),
			map[string]interface{}{"trade_no": latest.TradeNo, "amount": latest.Amount, "payment_method": "alipay"})
	}
	return true
}

// AlipayWebhook 支付宝异步通知（POST /alipay/webhook，当面付与电脑网站支付共用）。
func AlipayWebhook(c *gin.Context) {
	client, err := GetAlipayClient()
	if err != nil {
		common.SysError("alipay webhook client init failed: " + err.Error())
		c.String(400, "fail")
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		c.String(400, "fail")
		return
	}
	// DecodeNotification 内部完成签名验证（RSA2，使用支付宝公钥证书）
	notification, err := client.DecodeNotification(c.Request.Context(), c.Request.Form)
	if err != nil {
		common.SysError("alipay webhook verify failed: " + err.Error())
		c.String(400, "fail")
		return
	}

	tradeStatus := notification.TradeStatus
	tradeNo := notification.OutTradeNo
	if tradeStatus != alipay.TradeStatusSuccess && tradeStatus != alipay.TradeStatusFinished {
		c.String(200, "success")
		return
	}
	if tradeNo == "" {
		c.String(200, "success")
		return
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	// 按订单实际渠道入账（f2f/pc 共用回调，需先确认是本项目的支付宝订单）
	topUp, err := model.GetRechargeByTradeNo(tradeNo)
	if err != nil ||
		(topUp.PaymentProvider != model.PaymentProviderAlipayF2F && topUp.PaymentProvider != model.PaymentProviderAlipayPCWeb) {
		c.String(200, "success")
		return
	}
	if err := service.CompleteRecharge(tradeNo, topUp.PaymentProvider, "支付宝充值"); err != nil {
		common.SysError(fmt.Sprintf("alipay complete recharge failed: %v", err))
	} else {
		// 充值成功，记录审计
		if user, _ := model.GetUserById(topUp.UserId); user != nil {
			model.RecordOperationAudit(c, user.Id, user.Username, user.Role,
				model.AuditCategoryUsage, "recharge.complete",
				fmt.Sprintf("支付宝充值完成 %s", tradeNo),
				map[string]interface{}{"trade_no": tradeNo, "amount": topUp.Amount, "payment_method": "alipay"})
		}
	}
	c.String(200, "success")
}
