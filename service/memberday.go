package service

import (
	"fmt"
	"math"
	"time"

	"easytv-api/model"
	"easytv-api/setting"
)

// 会员日（M7 剩余项）：每月固定一天积分包打折，/billing/plans 下发，报价与下单自动应用。

// MemberDay /billing/plans.promo.member_day。
type MemberDay struct {
	Day          int     `json:"day"`
	PackDiscount float64 `json:"pack_discount"`
	Active       bool    `json:"active"`
	NextAt       int64   `json:"next_at"`
	Label        string  `json:"label"`
}

// MemberDayInfo 按 Asia/Shanghai 的号数判断今天是否会员日，并给出下一次的时间。
func MemberDayInfo() MemberDay {
	cfg := setting.GetGrowthConfig()
	out := MemberDay{Day: cfg.MemberDayOfMonth, PackDiscount: cfg.MemberDayPackDiscount}
	if cfg.MemberDayOfMonth <= 0 || cfg.MemberDayPackDiscount <= 0 || cfg.MemberDayPackDiscount >= 1 {
		return out
	}
	now := time.Now().In(model.CheckinLocation)
	out.Active = cfg.MemberDayActive(now.Day())
	out.Label = fmt.Sprintf("每月 %d 日会员日，积分包 %s", cfg.MemberDayOfMonth, discountText(cfg.MemberDayPackDiscount))
	next := time.Date(now.Year(), now.Month(), cfg.MemberDayOfMonth, 0, 0, 0, 0, model.CheckinLocation)
	if !next.After(now) && !out.Active {
		next = next.AddDate(0, 1, 0)
	}
	out.NextAt = next.Unix()
	return out
}

// discountText 0.8 → 「8 折」，0.85 → 「8.5 折」。
func discountText(rate float64) string {
	off := math.Round(rate*100) / 10
	if off == math.Trunc(off) {
		return fmt.Sprintf("%d 折", int(off))
	}
	return fmt.Sprintf("%.1f 折", off)
}
