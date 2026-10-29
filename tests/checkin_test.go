package tests

import (
	"testing"
	"time"

	"easytv-api/model"
	"easytv-api/service"
)

func TestNextCheckinBonusIn(t *testing.T) {
	cases := map[int]int{0: 7, 1: 6, 6: 1, 7: 7, 13: 1, 14: 7}
	for streak, want := range cases {
		if got := service.NextCheckinBonusIn(streak); got != want {
			t.Errorf("streak %d: want %d got %d", streak, want, got)
		}
	}
}

func TestCheckinDayUsesShanghai(t *testing.T) {
	// UTC 2026-09-11 17:30 = 北京时间 09-12 01:30，应记为 09-12
	utc := time.Date(2026, 9, 11, 17, 30, 0, 0, time.UTC)
	if got := model.CheckinDay(utc); got != "2026-09-12" {
		t.Errorf("want 2026-09-12 got %s", got)
	}
}
