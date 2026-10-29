package controller

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/downloader"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

const wechatPayNotifyPath = "/wechat/webhook"

var wechatPayClientCache sync.Map // key: mchId, value: *core.Client
var wechatPayClientCacheMu sync.Mutex

// loadRSAPrivateKey 支持 PKCS#1 与 PKCS#8。
func loadRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("私钥不是合法的 PEM 数据")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败：%w", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("私钥不是 RSA 类型")
	}
	return rsaKey, nil
}

// pemLabelRe 匹配 PEM 块的类型标签，如 PRIVATE KEY / RSA PRIVATE KEY。
var pemLabelRe = regexp.MustCompile(`-----BEGIN ([A-Z0-9 ]+)-----`)

// normalizePrivateKeyPem 规整配置里的 PEM 私钥，使其成为可由 pem.Decode 解析的标准多行 PEM：
// 去前后空白、还原转义的字面换行符，并剥离 head/tail 之间 base64 正文里的所有空白（空格，
// 这些空白来自后台粘贴时换行被折成单行空格）。最后按 64 列重排为标准 PEM。
func normalizePrivateKeyPem(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, `\r\n`, "\n")
	raw = strings.ReplaceAll(raw, `\n`, "\n")

	m := pemLabelRe.FindStringSubmatch(raw)
	if m == nil {
		return raw
	}
	label := m[1]
	begin := "-----BEGIN " + label + "-----"
	end := "-----END " + label + "-----"
	i := strings.Index(raw, begin)
	j := strings.Index(raw, end)
	if i < 0 || j < 0 || j <= i {
		return raw
	}
	body := strings.Join(strings.Fields(raw[i+len(begin):j]), "")
	if body == "" {
		return raw
	}
	var b strings.Builder
	b.WriteString(begin)
	for off := 0; off < len(body); off += 64 {
		limit := off + 64
		if limit > len(body) {
			limit = len(body)
		}
		b.WriteByte('\n')
		b.WriteString(body[off:limit])
	}
	b.WriteByte('\n')
	b.WriteString(end)
	return b.String()
}

type wechatPayConfig struct {
	AppId     string
	MchId     string
	SerialNo  string
	ApiV3Key  string
	NotifyURL string
}

func readWechatPayConfig() wechatPayConfig {
	return wechatPayConfig{
		AppId:     strings.TrimSpace(model.GetSetting(setting.KeyWechatPayAppId)),
		MchId:     strings.TrimSpace(model.GetSetting(setting.KeyWechatPayMchId)),
		SerialNo:  strings.TrimSpace(model.GetSetting(setting.KeyWechatPayMchSerialNo)),
		ApiV3Key:  strings.TrimSpace(model.GetSetting(setting.KeyWechatPayApiV3Key)),
		NotifyURL: strings.TrimSpace(model.GetSetting(setting.KeyWechatPayNotifyURL)),
	}
}

// GetWechatPayClient 返回 wechatpay 客户端，同一商户号复用缓存。
func GetWechatPayClient() (*core.Client, error) {
	cfg := readWechatPayConfig()
	if cfg.AppId == "" || cfg.MchId == "" || cfg.SerialNo == "" || cfg.ApiV3Key == "" {
		return nil, errors.New("wechat pay config is not fully set")
	}
	if v, ok := wechatPayClientCache.Load(cfg.MchId); ok {
		return v.(*core.Client), nil
	}
	wechatPayClientCacheMu.Lock()
	defer wechatPayClientCacheMu.Unlock()
	if v, ok := wechatPayClientCache.Load(cfg.MchId); ok {
		return v.(*core.Client), nil
	}
	privKeyPem := normalizePrivateKeyPem(model.GetSetting(setting.KeyWechatPayMchPrivKey))
	privKey, err := loadRSAPrivateKey([]byte(privKeyPem))
	if err != nil {
		// 回退 SDK 内置工具（要求 PKCS#8 PEM 字符串）
		privKey, err = utils.LoadPrivateKey(privKeyPem)
		if err != nil {
			return nil, fmt.Errorf("解析商户私钥失败：%w", err)
		}
	}
	opts := []core.ClientOption{
		option.WithWechatPayAutoAuthCipher(cfg.MchId, cfg.SerialNo, privKey, cfg.ApiV3Key),
	}
	client, err := core.NewClient(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	wechatPayClientCache.Store(cfg.MchId, client)
	return client, nil
}

// buildWechatNotifyURL 拼接异步通知 URL。
func buildWechatNotifyURL(override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	return strings.TrimSuffix(model.GetSetting(setting.KeyServerAddress), "/") + wechatPayNotifyPath
}

// requestWechatNativePay 微信 Native 下单（在统一入口中分派）。
// teamId>0 为团队充值，须为团队 owner（由 ValidateTeamRechargeRequester 校验）。
func requestWechatNativePay(c *gin.Context, amount int64, teamId int) {
	if !setting.IsWechatPayEnabled() {
		common.ApiErrorMsg(c, "管理员未开启微信支付充值！")
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
	totalFen := int64(math.Round(payMoney * 100))
	if totalFen <= 0 {
		common.ApiErrorMsg(c, "充值金额过低")
		return
	}

	cfg := readWechatPayConfig()
	tradeNo := getTradeNo(userId)
	topUp := &model.Recharge{
		UserId:          userId,
		TeamId:          teamId,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		Status:          common.RechargeStatusPending,
		CreateTime:      time.Now().Unix(),
		PaymentMethod:   model.PaymentMethodWechatNative,
		PaymentProvider: model.PaymentProviderWechatNative,
	}
	if err := topUp.Insert(); err != nil {
		common.SysError("wechat topup insert failed: " + err.Error())
		common.ApiErrorMsg(c, "创建订单失败")
		return
	}
	model.RecordSimpleAudit(c, model.AuditCategoryUsage, "recharge.create",
		fmt.Sprintf("创建微信充值订单 %s", tradeNo),
		map[string]interface{}{"amount": amount, "trade_no": tradeNo, "payment_method": model.PaymentMethodWechatNative})

	client, err := GetWechatPayClient()
	if err != nil {
		common.SysError("wechat client init failed: " + err.Error())
		common.ApiErrorMsg(c, "微信支付客户端初始化失败")
		return
	}

	svc := native.NativeApiService{Client: client}
	expire := time.Now().Add(15 * time.Minute)
	resp, _, err := svc.Prepay(c.Request.Context(), native.PrepayRequest{
		Appid:       core.String(cfg.AppId),
		Mchid:       core.String(cfg.MchId),
		Description: core.String(fmt.Sprintf("充值 %s", common.SystemName)),
		OutTradeNo:  core.String(tradeNo),
		TimeExpire:  core.Time(expire),
		NotifyUrl:   core.String(buildWechatNotifyURL(cfg.NotifyURL)),
		Amount: &native.Amount{
			Currency: core.String("CNY"),
			Total:    core.Int64(totalFen),
		},
	})
	if err != nil || resp == nil || resp.CodeUrl == nil || *resp.CodeUrl == "" {
		common.SysError("wechat prepay failed: " + err.Error())
		common.ApiErrorMsg(c, "微信支付下单失败")
		return
	}

	common.ApiSuccess(c, gin.H{
		"code_url": *resp.CodeUrl, // 前端用 QR 库渲染
		"trade_no": tradeNo,
	})
}

// completeWechatNativeRecharge 置订单成功并入账（必须在订单锁内调用）。
func completeWechatNativeRecharge(c *gin.Context, tradeNo string) bool {
	if err := service.CompleteRecharge(tradeNo, model.PaymentProviderWechatNative, "微信支付充值"); err != nil {
		common.SysError(fmt.Sprintf("wechat complete recharge failed: %v", err))
		return false
	}
	// 记录充值完成审计
	if topUp, _ := model.GetRechargeByTradeNo(tradeNo); topUp != nil {
		if user, _ := model.GetUserById(topUp.UserId); user != nil {
			model.RecordOperationAudit(c, user.Id, user.Username, user.Role,
				model.AuditCategoryUsage, "recharge.complete",
				fmt.Sprintf("微信充值完成 %s", tradeNo),
				map[string]interface{}{"trade_no": tradeNo, "amount": topUp.Amount, "payment_method": "wechat"})
		}
	}
	return true
}

// queryAndCompleteWechatNativeRecharge 主动查询微信订单，已支付则补单（订单锁内幂等）。
// 供 RequestTopupStatus 复用，防 webhook 漏单。与 easyapi QueryAndCompleteWechatNativeRecharge 对齐。
func queryAndCompleteWechatNativeRecharge(ctx context.Context, topUp *model.Recharge) bool {
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderWechatNative || topUp.Status != common.RechargeStatusPending {
		return false
	}
	client, err := GetWechatPayClient()
	if err != nil {
		common.SysError("wechat query client init failed: " + err.Error())
		return false
	}
	cfg := readWechatPayConfig()
	svc := native.NativeApiService{Client: client}
	resp, _, err := svc.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: core.String(topUp.TradeNo),
		Mchid:      core.String(cfg.MchId),
	})
	if err != nil || resp == nil || resp.TradeState == nil {
		return false
	}
	if *resp.TradeState != "SUCCESS" {
		return false
	}
	LockOrder(topUp.TradeNo)
	defer UnlockOrder(topUp.TradeNo)
	// 加锁后再查一次，防与 webhook 并发重复入账
	latest, err := model.GetRechargeByTradeNo(topUp.TradeNo)
	if err != nil || latest.Status != common.RechargeStatusPending {
		return false
	}
	return completeWechatNativeRecharge(nil, latest.TradeNo)
}

// WechatNativeWebhook 微信支付异步通知（POST /wechat/webhook）。
func WechatNativeWebhook(c *gin.Context) {
	cfg := readWechatPayConfig()
	if cfg.ApiV3Key == "" {
		c.String(400, "fail")
		return
	}
	if _, err := GetWechatPayClient(); err != nil {
		common.SysError("wechat webhook client init failed: " + err.Error())
		c.String(400, "fail")
		return
	}
	certVisitor := downloader.MgrInstance().GetCertificateVisitor(cfg.MchId)
	if certVisitor == nil {
		common.SysError("wechat webhook certificate visitor not ready")
		c.String(400, "fail")
		return
	}
	handler, err := notify.NewRSANotifyHandler(cfg.ApiV3Key, verifiers.NewSHA256WithRSAVerifier(certVisitor))
	if err != nil {
		common.SysError("wechat notify handler init failed: " + err.Error())
		c.String(400, "fail")
		return
	}
	txn := new(struct {
		OutTradeNo  *string `json:"out_trade_no"`
		TradeState  *string `json:"trade_state"`
		SuccessTime *string `json:"success_time"`
	})
	if _, err := handler.ParseNotifyRequest(c.Request.Context(), c.Request, txn); err != nil {
		body, _ := io.ReadAll(c.Request.Body)
		common.SysError(fmt.Sprintf("wechat parse notify failed: %v body=%s", err, string(body)))
		c.String(400, "fail")
		return
	}
	outTradeNo := ""
	if txn.OutTradeNo != nil {
		outTradeNo = *txn.OutTradeNo
	}
	if outTradeNo == "" {
		c.String(200, "success")
		return
	}
	LockOrder(outTradeNo)
	defer UnlockOrder(outTradeNo)

	if txn.TradeState != nil && *txn.TradeState != "SUCCESS" {
		c.String(200, "success")
		return
	}
	completeWechatNativeRecharge(c, outTradeNo)
	c.String(200, "success")
}
