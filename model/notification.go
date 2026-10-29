package model

import (
	"fmt"
	"net/mail"
	"strings"

	"easytv-api/common"
)

// ===== 站内通知（API.md 1.9）=====

const (
	NotificationTypeWork    = "work"    // 作品审核 / 下架
	NotificationTypeTask    = "task"    // 生成任务
	NotificationTypeSystem  = "system"  // 其他系统消息
	NotificationTypeComment = "comment" // 收到评论 / 回复，或评论被隐藏
	NotificationTypeLike    = "like"    // 作品收到喜欢

	// NotificationTypesEnabledKey 启用的通知类型开关（DB setting 表，逗号分隔，如 "work,task,system"）。
	// 空值或缺省 = 全部启用；被停用的类型不再写入，用户端列表/未读数也不展示（前端隐藏依据）。
	NotificationTypesEnabledKey = "NotificationTypesEnabled"
)

// allNotificationTypes 全部内置类型（用于计算停用列表）。
var allNotificationTypes = []string{NotificationTypeWork, NotificationTypeTask, NotificationTypeSystem, NotificationTypeComment, NotificationTypeLike}

// Notification 发给单个用户的消息，与公告（notice，面向全员）区分。
type Notification struct {
	Id        int    `json:"id" gorm:"comment:通知ID"`
	UserId    int    `json:"user_id" gorm:"index:idx_notification_user,priority:1;comment:接收用户"`
	Type      string `json:"type" gorm:"type:varchar(20);default:'system';comment:work / task / system"`
	Title     string `json:"title" gorm:"type:varchar(100);comment:标题"`
	Content   string `json:"content" gorm:"type:varchar(500);default:'';comment:内容"`
	Link      string `json:"link" gorm:"type:varchar(300);default:'';comment:站内跳转路径"`
	ReadAt    int64  `json:"read_at" gorm:"bigint;default:0;index:idx_notification_user,priority:2;comment:已读时间，0 未读"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`

	// UserName 管理端列表附带
	UserName string `json:"user_name,omitempty" gorm:"-"`
}

func (Notification) TableName() string { return "notification" }

// PushNotification 写一条通知；失败只记日志，不影响主流程。停用类型的通知不写入。
func PushNotification(userId int, typ, title, content, link string) {
	if userId <= 0 {
		return
	}
	if !notificationTypeEnabled(typ) {
		return
	}
	// 用户在「设置 · 通知」里关闭的类型不再写入
	if !userNotificationTypeEnabled(userId, typ) {
		return
	}
	if len([]rune(content)) > 500 {
		content = string([]rune(content)[:500])
	}
	n := &Notification{
		UserId:    userId,
		Type:      typ,
		Title:     title,
		Content:   content,
		Link:      link,
		CreatedAt: common.GetTimestamp(),
	}
	if err := DB.Create(n).Error; err != nil {
		common.SysError("notification: push failed: " + err.Error())
		return
	}
	// 邮件同步：仅 work 类型（作品审核/下架/举报成立）且用户开启了 email_enabled 偏好时发邮件。
	// 异步发送，失败不影响主流程；未配置 SMTP 或联调模式（EMAIL_VERIFICATION_ENABLED=false）下仅打印日志。
	if typ == NotificationTypeWork {
		asyncPushEmail(userId, n.Title, n.Content, n.Link)
	}
}

// asyncPushEmail 异步发一封作品通知邮件。查询偏好与收件邮箱后协程内发送，任一环节失败即静默降级。
func asyncPushEmail(userId int, title, content, link string) {
	p, err := GetUserNotificationPref(userId)
	if err != nil || !p.EmailEnabled {
		return
	}
	u, err := GetUserById(userId)
	if err != nil || u == nil {
		return
	}
	// 只有邮箱注册的账号才有收件地址；微信 / 手机号注册的 username 不是邮箱，直接跳过
	if _, err := mail.ParseAddress(u.Username); err != nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("notification email panic: %v", r))
			}
		}()
		body := content
		if link != "" {
			body += "\n" + link
		}
		if e := common.SendEmail(u.Username, "[EasyTV] "+title, body); e != nil {
			common.SysError("notification email send failed: " + e.Error())
		}
	}()
}

// PushNotificationToUsers 给多个用户批量写通知（停用类型直接跳过）。
func PushNotificationToUsers(userIds []int, typ, title, content, link string) {
	if len(userIds) == 0 || !notificationTypeEnabled(typ) {
		return
	}
	for _, uid := range userIds {
		PushNotification(uid, typ, title, content, link)
	}
}

// notificationTypeEnabled 该类型是否处于启用状态。
func notificationTypeEnabled(typ string) bool {
	enabled := enabledNotificationTypes()
	if enabled == nil {
		return true
	}
	for _, t := range enabled {
		if t == typ {
			return true
		}
	}
	return false
}

// enabledNotificationTypes 当前启用类型列表；nil 表示全部启用。
func enabledNotificationTypes() []string {
	v := strings.TrimSpace(GetSetting(NotificationTypesEnabledKey))
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	enabled := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			enabled = append(enabled, t)
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	return enabled
}

// disabledNotificationTypes 当前停用类型列表（供查询过滤）。
func disabledNotificationTypes() []string {
	enabled := enabledNotificationTypes()
	if enabled == nil {
		return nil
	}
	m := make(map[string]bool, len(enabled))
	for _, t := range enabled {
		m[t] = true
	}
	disabled := make([]string, 0, len(allNotificationTypes))
	for _, t := range allNotificationTypes {
		if !m[t] {
			disabled = append(disabled, t)
		}
	}
	return disabled
}

// GetNotifications 用户通知列表（最新在前）与未读数；typ 非空时按类型过滤，停用类型不返回。
func GetNotifications(userId int, unreadOnly bool, typ string, pageInfo *common.PageInfo) (list []*Notification, total, unread int64, err error) {
	query := DB.Model(&Notification{}).Where("user_id = ?", userId)
	if unreadOnly {
		query = query.Where("read_at = 0")
	}
	if typ != "" {
		query = query.Where("type = ?", typ)
	}
	if disabled := disabledNotificationTypes(); len(disabled) > 0 {
		query = query.Where("type NOT IN ?", disabled)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, 0, err
	}
	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
		return nil, 0, 0, err
	}
	unreadQuery := DB.Model(&Notification{}).Where("user_id = ? AND read_at = 0", userId)
	if typ != "" {
		unreadQuery = unreadQuery.Where("type = ?", typ)
	}
	if disabled := disabledNotificationTypes(); len(disabled) > 0 {
		unreadQuery = unreadQuery.Where("type NOT IN ?", disabled)
	}
	unreadQuery.Count(&unread)
	return list, total, unread, nil
}

// CountUnreadNotifications 未读数（与列表一致，受类型开关过滤）。
func CountUnreadNotifications(userId int) int64 {
	var n int64
	query := DB.Model(&Notification{}).Where("user_id = ? AND read_at = 0", userId)
	if disabled := disabledNotificationTypes(); len(disabled) > 0 {
		query = query.Where("type NOT IN ?", disabled)
	}
	query.Count(&n)
	return n
}

// MarkNotificationsRead ids 为空表示全部已读。
func MarkNotificationsRead(userId int, ids []int) error {
	query := DB.Model(&Notification{}).Where("user_id = ? AND read_at = 0", userId)
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	}
	return query.Update("read_at", common.GetTimestamp()).Error
}

// NotificationAdminFilter 管理端通知列表过滤。
type NotificationAdminFilter struct {
	UserId  int
	Type    string
	Unread  bool
	Keyword string
}

// GetNotificationsAdmin 管理端通知列表（附用户名），最新在前。
func GetNotificationsAdmin(filter NotificationAdminFilter, pageInfo *common.PageInfo) (list []*Notification, total int64, err error) {
	query := DB.Model(&Notification{})
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.Unread {
		query = query.Where("read_at = 0")
	}
	if filter.Keyword != "" {
		query = query.Where("title LIKE ? OR content LIKE ?", "%"+filter.Keyword+"%", "%"+filter.Keyword+"%")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(list))
	for _, n := range list {
		ids = append(ids, n.UserId)
	}
	names := GetUserNicknames(ids)
	for _, n := range list {
		n.UserName = names[n.UserId]
	}
	return list, total, nil
}

// DeleteNotification 删除单条通知。
func DeleteNotification(id int) error {
	return DB.Delete(&Notification{}, id).Error
}

// HasRecentNotification 某用户在 since 之后是否已收到同类型、同链接的通知（点赞等高频事件去重用）。
func HasRecentNotification(userId int, typ, link string, since int64) bool {
	var n int64
	DB.Model(&Notification{}).Where("user_id = ? AND type = ? AND link = ? AND created_at >= ?", userId, typ, link, since).Count(&n)
	return n > 0
}
