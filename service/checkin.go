package service

import (
	"fmt"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 每日签到（T10）：基础积分 × 会员倍数，连续 7 的倍数天加连续奖励，当月满勤加满勤奖励。
// 积分有效期 GrowthCheckinValidDays（默认 7 天），流水 biz_no 分别为 checkin / checkin_streak / checkin_month。

// CheckinResult POST /user/checkin 响应。
type CheckinResult struct {
	Already     bool `json:"already"`
	Points      int  `json:"points"`       // 基础积分（已含会员倍数）
	BonusPoints int  `json:"bonus_points"` // 连续 + 满勤奖励
	Streak      int  `json:"streak"`
}

// CheckinStatus GET /user/checkin/status 响应。
type CheckinStatus struct {
	TodayDone   bool  `json:"today_done"`
	Streak      int   `json:"streak"`        // 当前有效连续天数（昨天或今天签过才延续，否则 0）
	MonthDays   []int `json:"month_days"`    // 本月已签的日期号
	NextBonusIn int   `json:"next_bonus_in"` // 距下一次连续奖励还差几天
	PointsToday int   `json:"points_today"`  // 今天签到可得基础积分
	StreakBonus int   `json:"streak_bonus"`
	MonthBonus  int   `json:"month_bonus"`
	ValidDays   int   `json:"valid_days"`
	IsMember    bool  `json:"is_member"`
}

func checkinBasePoints(cfg setting.GrowthConfig, isMember bool) int {
	p := cfg.CheckinPoints
	if isMember && cfg.CheckinMemberMultiplier > 1 {
		p *= cfg.CheckinMemberMultiplier
	}
	return p
}

// Checkin 执行签到；同一天重复调用返回 Already=true 且不重复发放。
func Checkin(userId int) (*CheckinResult, error) {
	if userId <= 0 {
		return nil, fmt.Errorf("未登录")
	}
	cfg := setting.GetGrowthConfig()
	now := time.Now().In(model.CheckinLocation)
	today := model.CheckinDay(now)
	yesterday := model.CheckinDay(now.AddDate(0, 0, -1))
	isMember := IsMemberForPricing(userId, 0) // 签到奖励进个人钱包，只看自己的会员（API.md 20）
	base := checkinBasePoints(cfg, isMember)
	out := &CheckinResult{}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		streak := 1
		if last := model.GetLastCheckin(tx, userId); last != nil {
			if last.Day == today {
				out.Already, out.Streak, out.Points = true, last.Streak, 0
				return nil
			}
			if last.Day == yesterday {
				streak = last.Streak + 1
			}
		}
		inserted, err := model.InsertCheckinOnce(tx, userId, today, streak, base)
		if err != nil {
			return err
		}
		if !inserted {
			out.Already, out.Streak = true, streak
			return nil
		}
		out.Streak, out.Points = streak, base
		expires := common.GetTimestamp() + int64(cfg.CheckinValidDays)*86400
		if base > 0 {
			if _, err := model.GrantPoints(tx, userId, model.LotSourceGift, base, expires, 0, 0,
				model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonCheckin,
				fmt.Sprintf("每日签到 +%d 积分（连续 %d 天）", base, streak), 0); err != nil {
				return err
			}
		}
		if cfg.CheckinStreakBonus > 0 && streak%7 == 0 {
			if _, err := model.GrantPoints(tx, userId, model.LotSourceGift, cfg.CheckinStreakBonus, expires, 0, 0,
				model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonCheckinStreak,
				fmt.Sprintf("连续签到 %d 天奖励 +%d 积分", streak, cfg.CheckinStreakBonus), 0); err != nil {
				return err
			}
			out.BonusPoints += cfg.CheckinStreakBonus
		}
		if cfg.CheckinMonthBonus > 0 {
			month := today[:7]
			days, err := model.GetMonthCheckinDays(tx, userId, month)
			if err != nil {
				return err
			}
			daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, model.CheckinLocation).Day()
			if len(days) == daysInMonth {
				if _, err := model.GrantPoints(tx, userId, model.LotSourceGift, cfg.CheckinMonthBonus, expires, 0, 0,
					model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonCheckinMonth,
					fmt.Sprintf("%s 月满勤奖励 +%d 积分", month, cfg.CheckinMonthBonus), 0); err != nil {
					return err
				}
				out.BonusPoints += cfg.CheckinMonthBonus
			}
		}
		if out.BonusPoints > 0 {
			return model.UpdateCheckinPoints(tx, userId, today, base+out.BonusPoints)
		}
		return nil
	})
	if err != nil {
		common.SysError(fmt.Sprintf("checkin failed user=%d: %v", userId, err))
		return nil, fmt.Errorf("签到失败，请稍后重试")
	}
	if !out.Already {
		Track(userId, "checkin", map[string]any{"streak": out.Streak, "points": out.Points + out.BonusPoints})
	}
	return out, nil
}

// GetCheckinStatus 签到状态。
func GetCheckinStatus(userId int) (*CheckinStatus, error) {
	cfg := setting.GetGrowthConfig()
	now := time.Now().In(model.CheckinLocation)
	today := model.CheckinDay(now)
	yesterday := model.CheckinDay(now.AddDate(0, 0, -1))
	isMember := IsMemberForPricing(userId, 0) // 签到奖励进个人钱包，只看自己的会员（API.md 20）
	st := &CheckinStatus{
		PointsToday: checkinBasePoints(cfg, isMember),
		StreakBonus: cfg.CheckinStreakBonus,
		MonthBonus:  cfg.CheckinMonthBonus,
		ValidDays:   cfg.CheckinValidDays,
		IsMember:    isMember,
	}
	if last := model.GetLastCheckin(model.DB, userId); last != nil {
		switch last.Day {
		case today:
			st.TodayDone, st.Streak = true, last.Streak
		case yesterday:
			st.Streak = last.Streak
		}
	}
	days, err := model.GetMonthCheckinDays(model.DB, userId, today[:7])
	if err != nil {
		return nil, err
	}
	st.MonthDays = days
	st.NextBonusIn = NextCheckinBonusIn(st.Streak)
	return st, nil
}

// NextCheckinBonusIn 还需连续签到几天拿到连续奖励（含下一次签到）：
// streak 为当前有效连续天数，无论今天是否已签，下一次签到后的连续数都是 streak+1。
func NextCheckinBonusIn(streak int) int {
	if streak < 0 {
		streak = 0
	}
	return 7 - streak%7
}
