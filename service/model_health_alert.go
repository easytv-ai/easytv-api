package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
)

// 模型健康自动提醒（API.md 37.3）：定价页要有人去看才有用。后台每 6 小时按近 30 天口径检查一次，
// 生效中的模型「建议下架」时给管理员发一条站内通知（多个模型合成一条）；同一模型 7 天内不重复提醒，
// 恢复正常后清掉记录，再坏会马上再提醒。

const (
	ModelHealthAlertDays     = 30
	ModelHealthAlertCooldown = 7 * 24 * time.Hour
	// ModelHealthAlertNotifiedKey setting 里记「模型 → 上次提醒时间（Unix 秒）」的 JSON
	ModelHealthAlertNotifiedKey = "ModelHealthAlertNotified"
)

// pickModelHealthAlerts 纯函数：从健康统计里挑出这次要提醒的模型（按模型名排序），并给出新的提醒记录。
// active 为生效中的模型（已停用的不提醒）；notified 为上次的记录。
func pickModelHealthAlerts(stats map[string]*ModelHealthStat, active map[string]bool, notified map[string]int64, now time.Time) ([]*ModelHealthStat, map[string]int64) {
	next := map[string]int64{}
	var alerts []*ModelHealthStat
	for name, st := range stats {
		if st == nil || !st.SuggestOffline || !active[name] {
			continue
		}
		last := notified[name]
		if last > 0 && now.Sub(time.Unix(last, 0)) < ModelHealthAlertCooldown {
			next[name] = last
			continue
		}
		next[name] = now.Unix()
		alerts = append(alerts, st)
	}
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].Model < alerts[j].Model })
	return alerts, next
}

// modelHealthAlertMessage 通知标题与正文（正文封顶 500 字，由 PushNotification 截断兜底）。
func modelHealthAlertMessage(alerts []*ModelHealthStat) (string, string) {
	title := fmt.Sprintf("%d 个模型近 %d 天几乎全部失败，建议下架", len(alerts), ModelHealthAlertDays)
	parts := make([]string, 0, len(alerts))
	for _, st := range alerts {
		rate := 0.0
		if st.FailRate != nil {
			rate = *st.FailRate
		}
		line := fmt.Sprintf("%s 失败 %d/%d（%.0f%%）", st.Model, st.Failed, st.Total, rate)
		if len(st.TopErrors) > 0 {
			line += "，主要原因：" + st.TopErrors[0].Message
		}
		parts = append(parts, line)
	}
	content := strings.Join(parts, "；") + "。请到管理后台「模型定价」下架，或补全时长 / 分辨率等参数后再观察。"
	return title, content
}

// RunModelHealthAlerts 定时任务入口（main.go，联调实例不跑）。
func RunModelHealthAlerts() {
	stats, err := BuildModelHealth(ModelHealthAlertDays, time.Now())
	if err != nil {
		common.SysError("model health alert: " + err.Error())
		return
	}
	var names []string
	if err := model.DB.Model(&model.ModelPrice{}).Where("status = ?", 1).Pluck("model", &names).Error; err != nil {
		common.SysError("model health alert: " + err.Error())
		return
	}
	active := make(map[string]bool, len(names))
	for _, n := range names {
		active[n] = true
	}
	notified := map[string]int64{}
	if raw := strings.TrimSpace(model.GetSetting(ModelHealthAlertNotifiedKey)); raw != "" {
		_ = json.Unmarshal([]byte(raw), &notified)
	}
	alerts, next := pickModelHealthAlerts(stats, active, notified, time.Now())
	if len(alerts) > 0 {
		var admins []int
		if err := model.DB.Model(&model.User{}).Where("role >= ? AND status = ?", constant.RoleAdminUser, constant.UserStatusEnabled).Pluck("id", &admins).Error; err != nil {
			common.SysError("model health alert: " + err.Error())
			return
		}
		title, content := modelHealthAlertMessage(alerts)
		model.PushNotificationToUsers(admins, model.NotificationTypeSystem, title, content, "")
		common.SysLog(fmt.Sprintf("model health alert: %d 个模型建议下架，已通知 %d 位管理员", len(alerts), len(admins)))
	}
	buf, _ := json.Marshal(next)
	if string(buf) != strings.TrimSpace(model.GetSetting(ModelHealthAlertNotifiedKey)) {
		if err := model.UpdateSetting(ModelHealthAlertNotifiedKey, string(buf)); err != nil {
			common.SysError("model health alert: " + err.Error())
		}
	}
}
