package controller

import (
	"testing"
	"time"

	"easytv-api/common"
)

// TestConsoleAccessTicketOneShot Redis 不可用时走内存：ticket 只能兑换一次，过期作废。
func TestConsoleAccessTicketOneShot(t *testing.T) {
	if common.RedisClient != nil {
		t.Skip("redis 已连接，只测内存兜底")
	}
	code, err := saveConsoleAccessTicket(consoleAccessTicket{UserId: 8, AdminId: 1})
	if err != nil || len(code) != 32 {
		t.Fatalf("save: code=%q err=%v", code, err)
	}
	if got := consumeConsoleAccessTicket(code); got == nil || got.UserId != 8 || got.AdminId != 1 {
		t.Fatalf("首次兑换应成功: %+v", got)
	}
	if got := consumeConsoleAccessTicket(code); got != nil {
		t.Fatalf("第二次兑换应失败: %+v", got)
	}
	if consumeConsoleAccessTicket("") != nil || consumeConsoleAccessTicket("nope") != nil {
		t.Fatal("空 / 不存在的 ticket 应失败")
	}

	expired, _ := saveConsoleAccessTicket(consoleAccessTicket{UserId: 9})
	consoleAccessMemory.Lock()
	item := consoleAccessMemory.items[expired]
	item.expiresAt = time.Now().Add(-time.Second)
	consoleAccessMemory.items[expired] = item
	consoleAccessMemory.Unlock()
	if consumeConsoleAccessTicket(expired) != nil {
		t.Fatal("过期 ticket 应失败")
	}
}
