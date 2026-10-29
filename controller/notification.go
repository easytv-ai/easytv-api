package controller

import (
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 站内通知（API.md 1.9 / 2.1）=====

// GetMyNotifications 通知列表（GET /user/notification?unread=1&type=&page=&page_size=），data 附 unread_count。
// type 参数用于前端按类型过滤（如只保留 task 类型）；停用类型的通知后端直接不返回。
func GetMyNotifications(c *gin.Context) {
	userId := c.GetInt("id")
	list, total, unread, err := model.GetNotifications(userId, c.Query("unread") == "1", c.Query("type"), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if list == nil {
		list = []*model.Notification{}
	}
	common.ApiSuccess(c, gin.H{
		"items":        list,
		"total":        total,
		"unread_count": unread,
	})
}

// ReadMyNotifications 标记已读（POST /user/notification/read）：`{ ids?: [] }`，不传 ids 表示全部已读。
func ReadMyNotifications(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Ids []int `json:"ids"`
	}
	if c.Request.ContentLength > 0 {
		if err := common.DecodeJson(c.Request.Body, &req); err != nil {
			common.ApiErrorMsg(c, "无效的参数")
			return
		}
	}
	if err := model.MarkNotificationsRead(userId, req.Ids); err != nil {
		respondModelError(c, err, "操作失败")
		return
	}
	common.ApiSuccess(c, gin.H{"unread_count": model.CountUnreadNotifications(userId)})
}

// ===== 管理端（API.md 2.3）=====

// AdminGetNotifications 通知列表（GET /admin/notification?user_id=&type=&unread=&keyword=&page=&page_size=）。
func AdminGetNotifications(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Query("user_id"))
	filter := model.NotificationAdminFilter{
		UserId:  userId,
		Type:    c.Query("type"),
		Unread:  c.Query("unread") == "1",
		Keyword: c.Query("keyword"),
	}
	list, total, err := model.GetNotificationsAdmin(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if list == nil {
		list = []*model.Notification{}
	}
	common.ApiSuccess(c, gin.H{
		"items": list,
		"total": total,
	})
}

// AdminDeleteNotification 删除通知（DELETE /admin/notification/:id）。
func AdminDeleteNotification(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := model.DeleteNotification(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminGetNotificationTypes 通知类型候选（GET /admin/notification/types）：运营配置全局开关时的类型选项，叠加各类型当前启用状态。
func AdminGetNotificationTypes(c *gin.Context) {
	common.ApiSuccess(c, gin.H{"types": model.NotificationTypeMetas()})
}

// AdminSendNotification 站内通知推送（POST /admin/notification/send）：
// body: `{ user_id?: int（0/缺省=全员）, type, title, content, link }`。
func AdminSendNotification(c *gin.Context) {
	var req struct {
		UserId  int    `json:"user_id"`
		Type    string `json:"type"`
		Title   string `json:"title"`
		Content string `json:"content"`
		Link    string `json:"link"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		common.ApiErrorMsg(c, "标题不能为空")
		return
	}
	typ := strings.TrimSpace(req.Type)
	if typ == "" {
		typ = model.NotificationTypeSystem
	}

	if req.UserId > 0 {
		model.PushNotification(req.UserId, typ, title, req.Content, req.Link)
		common.ApiSuccess(c, gin.H{"sent": 1})
		return
	}
	ids, err := model.AllEnabledUserIds()
	if err != nil {
		respondModelError(c, err, "获取用户列表失败")
		return
	}
	model.PushNotificationToUsers(ids, typ, title, req.Content, req.Link)
	common.ApiSuccess(c, gin.H{"sent": len(ids)})
}

// ===== 用户通知偏好（API.md 1.9.1）=====

// GetMyNotificationPrefs 通知偏好（GET /user/notification/prefs）：各类型开关 + 邮件通知预留位。
func GetMyNotificationPrefs(c *gin.Context) {
	p, err := model.GetUserNotificationPref(c.GetInt("id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"types":         model.NotificationTypeView(p),
		"email_enabled": p.EmailEnabled,
	})
}

// UpdateMyNotificationPrefs 保存偏好（PUT /user/notification/prefs）：`{ disabled_types: [], email_enabled }`。
func UpdateMyNotificationPrefs(c *gin.Context) {
	var req struct {
		DisabledTypes []string `json:"disabled_types"`
		EmailEnabled  bool     `json:"email_enabled"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	p, err := model.SaveUserNotificationPref(c.GetInt("id"), req.DisabledTypes, req.EmailEnabled)
	if err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"types":         model.NotificationTypeView(p),
		"email_enabled": p.EmailEnabled,
	})
}
