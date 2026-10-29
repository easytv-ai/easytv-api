package service

import (
	"strings"
	"testing"
)

func TestCreatorCashConversion(t *testing.T) {
	const rate = 20
	if got := CreatorCashYuanAt(0, rate); got != 0 {
		t.Fatalf("0 积分: %v", got)
	}
	if got := CreatorCashYuanAt(25, rate); got != 1.25 {
		t.Fatalf("25 积分应兑 1.25 元: %v", got)
	}
	if got := CreatorCashYuanAt(19, rate); got != 0.95 {
		t.Fatalf("19 积分应兑 0.95 元: %v", got)
	}
	if got := CreatorPointsForCashAt(0, rate); got != 0 {
		t.Fatalf("0 元: %v", got)
	}
	if got := CreatorPointsForCashAt(1.25, rate); got != 25 {
		t.Fatalf("1.25 元应扣 25 积分: %v", got)
	}
	if got := CreatorPointsForCashAt(0.01, rate); got != 0 {
		t.Fatalf("0.01 元按 20 汇率四舍五入应为 0：%v", got)
	}
	if got := CreatorPointsForCashAt(0.03, rate); got != 1 {
		t.Fatalf("0.03 元应扣 1 积分: %v", got)
	}
}

func TestNormalizeCreatorCashWithdraw(t *testing.T) {
	req := CreatorCashWithdrawRequest{Amount: 12.345, Way: "", AccountInfo: `{"name":"张三","account":"123"}`}
	if err := NormalizeCreatorCashWithdraw(&req); err != nil {
		t.Fatalf("合法申请: %v", err)
	}
	if req.Amount != 12.35 || req.Way != "alipay" {
		t.Fatalf("应四舍五入金额并缺省支付宝: %+v", req)
	}
	if err := NormalizeCreatorCashWithdraw(&CreatorCashWithdrawRequest{Amount: 0, AccountInfo: `{}`}); err == nil || !strings.Contains(err.Error(), "大于 0") {
		t.Fatalf("金额 0: %v", err)
	}
	if err := NormalizeCreatorCashWithdraw(&CreatorCashWithdrawRequest{Amount: 1, Way: "paypal", AccountInfo: `{}`}); err == nil {
		t.Fatal("不支持的方式应失败")
	}
	if err := NormalizeCreatorCashWithdraw(&CreatorCashWithdrawRequest{Amount: 1, Way: "alipay", AccountInfo: "not-json"}); err == nil {
		t.Fatal("非法 JSON 应失败")
	}
}

func TestRefundCreatorCashWithdrawSkip(t *testing.T) {
	if err := RefundCreatorCashWithdraw(nil, ""); err != nil {
		t.Fatalf("nil: %v", err)
	}
}
