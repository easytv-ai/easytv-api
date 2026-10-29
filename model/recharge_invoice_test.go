package model

import "testing"

func TestEnsureRechargeInvoiceable(t *testing.T) {
	EnsureRechargeInvoiceable(nil)

	fresh := &Recharge{Money: 88.8}
	EnsureRechargeInvoiceable(fresh)
	if fresh.InvoiceableAmount != 88.8 {
		t.Fatalf("应写入实付：%v", fresh.InvoiceableAmount)
	}

	kept := &Recharge{Money: 100, InvoiceableAmount: 40}
	EnsureRechargeInvoiceable(kept)
	if kept.InvoiceableAmount != 40 {
		t.Fatalf("已有余额不应覆盖：%v", kept.InvoiceableAmount)
	}

	zero := &Recharge{Money: 0}
	EnsureRechargeInvoiceable(zero)
	if zero.InvoiceableAmount != 0 {
		t.Fatalf("实付 0 不应填写：%v", zero.InvoiceableAmount)
	}

	if got := RechargeOriginalInvoiceable(&Recharge{Money: 100, RefundedAmount: 30}); got != 70 {
		t.Fatalf("原始可开票: %v", got)
	}
	if got := RechargeOriginalInvoiceable(&Recharge{Money: 50, RefundedAmount: 80}); got != 0 {
		t.Fatalf("退超实付应为 0: %v", got)
	}
}
