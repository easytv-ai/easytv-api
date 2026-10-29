package model

import (
	"time"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Checkin 每日签到记录（T10）：一人一天一行，day 按 Asia/Shanghai 取 yyyy-mm-dd。
type Checkin struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"uniqueIndex:idx_checkin_user_day,priority:1"`
	Day       string `json:"day" gorm:"type:varchar(10);uniqueIndex:idx_checkin_user_day,priority:2;comment:签到日 yyyy-mm-dd"`
	Streak    int    `json:"streak" gorm:"default:1;comment:截至当天的连续天数"`
	Points    int    `json:"points" gorm:"default:0;comment:当天发放积分（含连续与满勤奖励）"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

func (Checkin) TableName() string { return "checkin" }

// CheckinLocation 签到按北京时间算日。
var CheckinLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}()

// CheckinDay 时间 → 签到日字符串。
func CheckinDay(t time.Time) string { return t.In(CheckinLocation).Format("2006-01-02") }

// GetLastCheckin 最近一次签到（不存在返回 nil）。
func GetLastCheckin(tx *gorm.DB, userId int) *Checkin {
	var c Checkin
	if err := tx.Where("user_id = ?", userId).Order("day desc").First(&c).Error; err != nil {
		return nil
	}
	return &c
}

// InsertCheckinOnce 写入当天记录；已存在返回 (false, nil)。
func InsertCheckinOnce(tx *gorm.DB, userId int, day string, streak, points int) (bool, error) {
	res := tx.Exec(
		"INSERT IGNORE INTO checkin (user_id, day, streak, points, created_at) VALUES (?, ?, ?, ?, ?)",
		userId, day, streak, points, common.GetTimestamp(),
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// UpdateCheckinPoints 补记当天最终发放积分（连续 / 满勤奖励算完后）。
func UpdateCheckinPoints(tx *gorm.DB, userId int, day string, points int) error {
	return tx.Model(&Checkin{}).Where("user_id = ? AND day = ?", userId, day).Update("points", points).Error
}

// GetMonthCheckinDays 某月（yyyy-mm）已签到的日期号列表。
func GetMonthCheckinDays(tx *gorm.DB, userId int, month string) ([]int, error) {
	var days []string
	if err := tx.Model(&Checkin{}).Where("user_id = ? AND day LIKE ?", userId, month+"-%").Order("day").Pluck("day", &days).Error; err != nil {
		return nil, err
	}
	out := make([]int, 0, len(days))
	for _, d := range days {
		if t, err := time.ParseInLocation("2006-01-02", d, CheckinLocation); err == nil {
			out = append(out, t.Day())
		}
	}
	return out, nil
}
