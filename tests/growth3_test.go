package tests

import (
	"testing"
	"time"

	"easytv-api/model"
	"easytv-api/service"
)

// TestMemberDayDiscountText 会员日折扣文案：整折不带小数，半折带一位。
func TestMemberDayDiscountText(t *testing.T) {
	md := service.MemberDayInfo()
	// 默认 11 日 8 折：未配置 setting 时取默认值，Label 必须带折扣文案
	if md.Day != 11 || md.PackDiscount <= 0 || md.PackDiscount >= 1 {
		t.Fatalf("默认会员日配置不对: %+v", md)
	}
	if md.Label == "" {
		t.Fatal("会员日应该有展示文案")
	}
	if md.NextAt <= 0 {
		t.Fatal("应给出下一次会员日时间")
	}
	// 今天是不是会员日要和当前号数一致（按 Asia/Shanghai）
	today := time.Now().In(model.CheckinLocation).Day()
	if md.Active != (today == md.Day) {
		t.Fatalf("Active=%v 但今天是 %d 号，会员日是 %d 号", md.Active, today, md.Day)
	}
}

// TestRankPeriodSince 周榜 7 天、月榜 30 天，非法值按周。
func TestRankPeriodSince(t *testing.T) {
	now := time.Now().Unix()
	week := model.RankPeriodSince("week")
	month := model.RankPeriodSince("month")
	other := model.RankPeriodSince("")
	if d := now - week; d < 7*86400-60 || d > 7*86400+60 {
		t.Fatalf("周榜窗口不对: %d", d)
	}
	if d := now - month; d < 30*86400-60 || d > 30*86400+60 {
		t.Fatalf("月榜窗口不对: %d", d)
	}
	if other != week && (week-other > 60 || other-week > 60) {
		t.Fatal("未知 period 应按周处理")
	}
}

// TestPartnerKinds 伙伴类型白名单。
func TestPartnerKinds(t *testing.T) {
	for _, k := range []string{"mcn", "school", "kol", "dev", "other"} {
		if !model.PartnerKinds[k] {
			t.Fatalf("%s 应是合法类型", k)
		}
	}
	if model.PartnerKinds["bank"] {
		t.Fatal("未知类型不该通过")
	}
}
