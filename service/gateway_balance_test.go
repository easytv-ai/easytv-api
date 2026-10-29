package service

import (
	"testing"
	"time"
)

func TestGatewayBalanceShouldAlert(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	if !gatewayBalanceShouldAlert(5, 20, 0, now) {
		t.Fatal("低于阈值且没提醒过应提醒")
	}
	if gatewayBalanceShouldAlert(5, 20, now.Add(-time.Hour).Unix(), now) {
		t.Fatal("24 小时内提醒过不重复")
	}
	if !gatewayBalanceShouldAlert(5, 20, now.Add(-25*time.Hour).Unix(), now) {
		t.Fatal("冷却过了再提醒")
	}
	if gatewayBalanceShouldAlert(25, 20, 0, now) || gatewayBalanceShouldAlert(5, 0, 0, now) {
		t.Fatal("余额够或阈值关闭不提醒")
	}
	if !gatewayBalanceShouldAlert(-0.81, 20, 0, now) {
		t.Fatal("欠费必须提醒")
	}
}
