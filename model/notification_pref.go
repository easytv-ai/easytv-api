package model

import (
	"errors"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 用户通知偏好（API.md 1.9.1）=====

// UserNotificationPref 用户级通知开关：disabled_types 逗号分隔的已关闭类型；email_enabled 为邮件通知预留。
type UserNotificationPref struct {
	Id            int    `json:"id"`
	UserId        int    `json:"user_id" gorm:"uniqueIndex;comment:用户ID"`
	DisabledTypes string `json:"disabled_types" gorm:"type:varchar(200);default:'';comment:已关闭的通知类型，逗号分隔"`
	EmailEnabled  bool   `json:"email_enabled" gorm:"default:false;comment:邮件通知（预留）"`
	UpdatedAt     int64  `json:"updated_at" gorm:"bigint"`
}

func (UserNotificationPref) TableName() string { return "user_notification_pref" }

// NotificationTypeMeta 前端展示用的类型说明。
type NotificationTypeMeta struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Desc    string `json:"desc"`
	Enabled bool   `json:"enabled"`
	// GloballyOff 运营在后台停用了该类型，用户侧不可开启
	GloballyOff bool `json:"globally_off"`
}

var notificationTypeMetas = []NotificationTypeMeta{
	{Type: NotificationTypeWork, Label: "作品审核", Desc: "作品被驳回、下架或举报成立时通知"},
	{Type: NotificationTypeComment, Label: "评论", Desc: "有人评论你的作品、回复你，或你的评论被隐藏时通知"},
	{Type: NotificationTypeLike, Label: "喜欢", Desc: "作品收到新的喜欢时通知，每部作品每天最多一条"},
	{Type: NotificationTypeTask, Label: "生成任务", Desc: "任务超时终止、积分退回时通知"},
	{Type: NotificationTypeSystem, Label: "系统消息", Desc: "运营手动发送的站内消息"},
}

// GetUserNotificationPref 取用户偏好，没有记录时返回默认（全部开启）。
func GetUserNotificationPref(userId int) (*UserNotificationPref, error) {
	p := &UserNotificationPref{}
	err := DB.Where("user_id = ?", userId).First(p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &UserNotificationPref{UserId: userId}, nil
	}
	return p, err
}

// SaveUserNotificationPref 写入偏好（不存在则创建）。
func SaveUserNotificationPref(userId int, disabled []string, emailEnabled bool) (*UserNotificationPref, error) {
	valid := map[string]bool{}
	for _, t := range allNotificationTypes {
		valid[t] = true
	}
	clean := make([]string, 0, len(disabled))
	seen := map[string]bool{}
	for _, t := range disabled {
		t = strings.TrimSpace(t)
		if valid[t] && !seen[t] {
			seen[t] = true
			clean = append(clean, t)
		}
	}
	p, err := GetUserNotificationPref(userId)
	if err != nil {
		return nil, err
	}
	p.DisabledTypes = strings.Join(clean, ",")
	p.EmailEnabled = emailEnabled
	p.UpdatedAt = common.GetTimestamp()
	if p.Id == 0 {
		if err := DB.Create(p).Error; err != nil {
			return nil, err
		}
		return p, nil
	}
	if err := DB.Model(p).Updates(map[string]any{
		"disabled_types": p.DisabledTypes,
		"email_enabled":  p.EmailEnabled,
		"updated_at":     p.UpdatedAt,
	}).Error; err != nil {
		return nil, err
	}
	return p, nil
}

// DisabledTypeSet 已关闭类型集合。
func (p *UserNotificationPref) DisabledTypeSet() map[string]bool {
	set := map[string]bool{}
	for _, t := range strings.Split(p.DisabledTypes, ",") {
		if t = strings.TrimSpace(t); t != "" {
			set[t] = true
		}
	}
	return set
}

// NotificationTypeView 组装偏好页展示：每种类型的开关状态，叠加全局停用。
func NotificationTypeView(p *UserNotificationPref) []NotificationTypeMeta {
	disabled := p.DisabledTypeSet()
	globallyOff := map[string]bool{}
	for _, t := range disabledNotificationTypes() {
		globallyOff[t] = true
	}
	out := make([]NotificationTypeMeta, 0, len(notificationTypeMetas))
	for _, m := range notificationTypeMetas {
		m.GloballyOff = globallyOff[m.Type]
		m.Enabled = !disabled[m.Type] && !m.GloballyOff
		out = append(out, m)
	}
	return out
}

// NotificationTypeMetas 返回全部通知类型的元数据（管理端候选来源），叠加当前全局开关状态。
func NotificationTypeMetas() []NotificationTypeMeta {
	return NotificationTypeView(&UserNotificationPref{})
}

// userNotificationTypeEnabled 用户是否关闭了该类型（无记录视为开启）。
func userNotificationTypeEnabled(userId int, typ string) bool {
	p, err := GetUserNotificationPref(userId)
	if err != nil {
		return true
	}
	return !p.DisabledTypeSet()[typ]
}
