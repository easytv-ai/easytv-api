package controller

import (
	"strings"
	"testing"
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
