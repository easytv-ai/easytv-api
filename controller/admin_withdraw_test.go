package controller

import (
	"errors"
	"strings"
	"testing"

	"easytv-api/model"
)

func TestWithdrawFailureMsg(t *testing.T) {
	if _, err := withdrawFailureMsg("  "); err != "请填写失败原因" {
		t.Fatalf("空原因应拒绝，得到 %q", err)
	}
	msg, err := withdrawFailureMsg("  银行退票  ")
	if err != "" || msg != "银行退票" {
		t.Fatalf("应去掉首尾空白，得到 %q %q", msg, err)
	}
	if _, err := withdrawFailureMsg(strings.Repeat("失败", 128)); err != "失败原因不能超过 255 字" {
		t.Fatalf("超长应拒绝，得到 %q", err)
	}
	if msg, err := withdrawFailureMsg(strings.Repeat("失", 255)); err != "" || len([]rune(msg)) != 255 {
		t.Fatalf("255 字应通过，得到 %q %q", msg, err)
	}
}

func TestWithdrawTransactionNo(t *testing.T) {
	if _, msg := withdrawTransactionNo("   "); msg == "" {
		t.Fatal("空流水号应拒绝")
	}
	if _, msg := withdrawTransactionNo(strings.Repeat("9", withdrawTransactionNoMax+1)); msg == "" {
		t.Fatal("超长流水号应拒绝")
	}
	if no, msg := withdrawTransactionNo("  4200001234202611140001  "); msg != "" || no != "4200001234202611140001" {
		t.Fatalf("正常流水号应去空格后通过：%q %q", no, msg)
	}
}

func TestWithdrawStatusError(t *testing.T) {
	if got := withdrawStatusError(model.ErrWithdrawStatus, "只有待审核的提现单可以审核"); got != "只有待审核的提现单可以审核" {
		t.Fatalf("状态不对应给人话：%q", got)
	}
	got := withdrawStatusError(errors.New("Error 1406: Data too long for column 'audit_remark'"), "x")
	if strings.Contains(got, "1406") || strings.Contains(got, "audit_remark") {
		t.Fatalf("数据库原文不能外露：%q", got)
	}
}

func TestRefundWithdrawBalanceNothingToRefund(t *testing.T) {
	if err := refundWithdrawBalance(nil, "x"); err != nil {
		t.Fatal(err)
	}
	if err := refundWithdrawBalance(&model.Withdraw{Amount: 0}, "x"); err != nil {
		t.Fatal(err)
	}
}
