package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

// UserPreference 用户偏好（API.md 33）：一人一键一行，值是前端自定义的 JSON 文本。
// 第一批用户是运镜库的「收藏 / 最近」——原来只存 localStorage，换台电脑就没了。
type UserPreference struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"uniqueIndex:idx_user_preference_user_key,priority:1;comment:用户ID"`
	Key       string `json:"key" gorm:"type:varchar(64);uniqueIndex:idx_user_preference_user_key,priority:2;comment:偏好键"`
	Value     string `json:"value" gorm:"type:text;comment:偏好值（JSON 文本，后端不解释）"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}

func (UserPreference) TableName() string { return "user_preference" }

const (
	// UserPreferenceKeyMax 键长上限；UserPreferenceValueMax 值长上限（字节），够存几百个 id
	UserPreferenceKeyMax   = 64
	UserPreferenceValueMax = 16 * 1024
)

// NormalizeUserPreferenceKey 键只收字母数字、点、下划线、横线（前端用 camera.fav 这类点分命名）。
func NormalizeUserPreferenceKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > UserPreferenceKeyMax {
		return "", errors.New("偏好键不合法")
	}
	for _, r := range key {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if !ok {
			return "", errors.New("偏好键不合法")
		}
	}
	return key, nil
}

// ListUserPreferences 一次取一个用户的全部偏好（键值对，几条到几十条）。
func ListUserPreferences(userId int) (map[string]string, error) {
	var rows []UserPreference
	if err := DB.Where("user_id = ?", userId).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

// SetUserPreference 写一个键：存在即覆盖（upsert，并发写最后一次胜出）。
func SetUserPreference(userId int, key, value string) error {
	row := UserPreference{UserId: userId, Key: key, Value: value, UpdatedAt: time.Now().Unix()}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&row).Error
}

// DeleteUserPreference 删一个键；不存在也不报错。
func DeleteUserPreference(userId int, key string) error {
	return DB.Where("user_id = ? AND `key` = ?", userId, key).Delete(&UserPreference{}).Error
}
