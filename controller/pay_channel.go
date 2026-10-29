package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"github.com/smartwalle/alipay/v3"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
)

// ===== 支付通道复用（订单支付用，与 recharge 充值共用微信 Native / 支付宝当面付）=====

// PayIntent 发起支付的结果：二维码内容 / 电脑网站支付跳转链接 + 支付单号 + 过期时间。
type PayIntent struct {
	PaymentMethod string `json:"payment_method"`
	CodeUrl       string `json:"code_url"`    // 二维码内容（微信 Native / 支付宝当面付），前端用 QR 库渲染
	PaymentUrl    string `json:"payment_url"` // 支付宝电脑网站支付跳转链接
	TradeNo       string `json:"trade_no"`
	ExpiresAt     int64  `json:"expires_at"`
}

// createPaymentForOrder 以订单号为支付单号下单：写 recharge（Amount=0，不加 quota）并向渠道预下单。
func createPaymentForOrder(ctx context.Context, o *model.Order, paymentMethod string) (*PayIntent, error) {
	if o.Price < 0.01 {
		return nil, errors.New("订单金额过低")
	}
	var provider string
	switch paymentMethod {
	case model.PaymentMethodWechatNative:
		if !setting.IsWechatPayEnabled() {
			return nil, errors.New("微信支付未开启")
		}
		provider = model.PaymentProviderWechatNative
	case model.PaymentMethodAlipayF2F:
		if !setting.IsAlipayF2FEnabled() {
			return nil, errors.New("支付宝当面付未开启")
		}
		provider = model.PaymentProviderAlipayF2F
	case model.PaymentMethodAlipayPCWeb:
		if !setting.IsAlipayPCWebEnabled() {
			return nil, errors.New("支付宝未开启")
		}
		provider = model.PaymentProviderAlipayPCWeb
	default:
		return nil, errors.New("不支持的支付方式")
	}

	// recharge 行：trade_no = order_no；同一订单重复发起支付时复用（渠道侧同单号可重复预下单直到过期）
	if _, err := model.GetRechargeByTradeNo(o.OrderNo); err != nil {
		topUp := &model.Recharge{
			UserId:          o.UserId,
			Amount:          0,
			Money:           o.Price,
			TradeNo:         o.OrderNo,
			Status:          common.RechargeStatusPending,
			CreateTime:      time.Now().Unix(),
			PaymentMethod:   paymentMethod,
			PaymentProvider: provider,
		}
		if err := topUp.Insert(); err != nil {
			return nil, fmt.Errorf("创建支付单失败：%w", err)
		}
	} else {
		_ = model.PatchRechargeInvoiceable(o.OrderNo, map[string]any{
			"payment_method":   paymentMethod,
			"payment_provider": provider,
		})
	}
	_ = model.SetOrderPaymentMethod(o.Id, paymentMethod)

	expire := time.Now().Add(15 * time.Minute)
	subject := fmt.Sprintf("%s %s", common.SystemName, o.Title)
	switch paymentMethod {
	case model.PaymentMethodWechatNative:
		codeUrl, err := wechatNativePrepay(ctx, o.OrderNo, o.Price, subject, expire)
		if err != nil {
			return nil, err
		}
		return &PayIntent{PaymentMethod: paymentMethod, CodeUrl: codeUrl, TradeNo: o.OrderNo, ExpiresAt: expire.Unix()}, nil
	case model.PaymentMethodAlipayPCWeb:
		payURL, err := buildAlipayPagePayURL(o.OrderNo, o.Price, subject)
		if err != nil {
			return nil, fmt.Errorf("支付宝下单失败：%w", err)
		}
		return &PayIntent{PaymentMethod: paymentMethod, PaymentUrl: payURL, TradeNo: o.OrderNo, ExpiresAt: expire.Unix()}, nil
	default:
		qr, err := alipayPrecreate(ctx, o.OrderNo, o.Price, subject)
		if err != nil {
			return nil, err
		}
		return &PayIntent{PaymentMethod: paymentMethod, CodeUrl: qr, TradeNo: o.OrderNo, ExpiresAt: expire.Unix()}, nil
	}
}

func wechatNativePrepay(ctx context.Context, tradeNo string, money float64, subject string, expire time.Time) (string, error) {
	cfg := readWechatPayConfig()
	client, err := GetWechatPayClient()
	if err != nil {
		return "", fmt.Errorf("微信支付客户端初始化失败：%w", err)
	}
	totalFen := int64(math.Round(money * 100))
	svc := native.NativeApiService{Client: client}
	resp, _, err := svc.Prepay(ctx, native.PrepayRequest{
		Appid:       core.String(cfg.AppId),
		Mchid:       core.String(cfg.MchId),
		Description: core.String(subject),
		OutTradeNo:  core.String(tradeNo),
		TimeExpire:  core.Time(expire),
		NotifyUrl:   core.String(buildWechatNotifyURL(cfg.NotifyURL)),
		Amount: &native.Amount{
			Currency: core.String("CNY"),
			Total:    core.Int64(totalFen),
		},
	})
	if err != nil {
		return "", fmt.Errorf("微信支付下单失败：%w", err)
	}
	if resp == nil || resp.CodeUrl == nil || *resp.CodeUrl == "" {
		return "", errors.New("微信支付下单失败：未返回二维码")
	}
	return *resp.CodeUrl, nil
}

func alipayPrecreate(ctx context.Context, tradeNo string, money float64, subject string) (string, error) {
	client, err := GetAlipayClient()
	if err != nil {
		return "", fmt.Errorf("支付宝客户端初始化失败：%w", err)
	}
	p := alipay.TradePreCreate{}
	p.OutTradeNo = tradeNo
	p.Subject = subject
	p.TotalAmount = fmt.Sprintf("%.2f", money)
	if notifyURL := buildAlipayNotifyURL(); notifyURL != "" {
		p.NotifyURL = notifyURL
	}
	rsp, err := client.TradePreCreate(ctx, p)
	if err != nil {
		return "", fmt.Errorf("支付宝下单失败：%w", err)
	}
	if !rsp.IsSuccess() {
		common.SysError(fmt.Sprintf("alipay precreate failed: code=%s msg=%s sub_code=%s sub_msg=%s out_trade_no=%s",
			rsp.Code, rsp.Msg, rsp.SubCode, rsp.SubMsg, tradeNo))
		return "", fmt.Errorf("支付宝下单失败：%s %s", rsp.SubCode, rsp.SubMsg)
	}
	return rsp.QRCode, nil
}

// refundViaChannel 按订单的支付渠道发起退款（全额）。
// 返回渠道结果描述；manual（后台标记收款）订单不调渠道。参考 easyapi RefundOrderService 的流程：先渠道退款，成功再改本地状态。
func refundViaChannel(ctx context.Context, o *model.Order, refundNo, reason string) (string, error) {
	if reason == "" {
		reason = "退款"
	}
	switch o.PaymentMethod {
	case model.PaymentMethodWechatNative:
		client, err := GetWechatPayClient()
		if err != nil {
			return "", fmt.Errorf("微信支付客户端初始化失败：%w", err)
		}
		fen := int64(math.Round(o.Price * 100))
		svc := refunddomestic.RefundsApiService{Client: client}
		resp, _, err := svc.Create(ctx, refunddomestic.CreateRequest{
			OutTradeNo:  core.String(o.OrderNo),
			OutRefundNo: core.String(refundNo),
			Reason:      core.String(reason),
			Amount: &refunddomestic.AmountReq{
				Refund:   core.Int64(fen),
				Total:    core.Int64(fen),
				Currency: core.String("CNY"),
			},
		})
		if err != nil {
			return "", fmt.Errorf("微信退款失败：%w", err)
		}
		status := ""
		if resp != nil && resp.Status != nil {
			status = string(*resp.Status)
		}
		switch status {
		case string(refunddomestic.STATUS_SUCCESS), string(refunddomestic.STATUS_PROCESSING):
			return "wechat:" + status, nil
		default:
			return "", fmt.Errorf("微信退款未受理：%s", status)
		}
	case model.PaymentMethodAlipayF2F, model.PaymentMethodAlipayPCWeb:
		client, err := GetAlipayClient()
		if err != nil {
			return "", fmt.Errorf("支付宝客户端初始化失败：%w", err)
		}
		rsp, err := client.TradeRefund(ctx, alipay.TradeRefund{
			OutTradeNo:   o.OrderNo,
			RefundAmount: fmt.Sprintf("%.2f", o.Price),
			RefundReason: reason,
			OutRequestNo: refundNo,
		})
		if err != nil {
			return "", fmt.Errorf("支付宝退款失败：%w", err)
		}
		if !rsp.IsSuccess() {
			return "", fmt.Errorf("支付宝退款失败：%s %s", rsp.SubCode, rsp.SubMsg)
		}
		return "alipay:fund_change=" + rsp.FundChange, nil
	case "manual", "":
		return "manual", nil
	}
	return "", fmt.Errorf("不支持的支付方式：%s", o.PaymentMethod)
}
