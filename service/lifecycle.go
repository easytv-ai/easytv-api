package service

import (
	"fmt"
	"net/mail"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 召回与生命周期触达（M13）：回归礼、会员续费提醒、积分过期提醒。每轮每类最多处理 200 人，去重靠站内信记录。

const lifecycleBatch = 200

// RunLifecycleJobs 由 main 的后台 goroutine 每 6 小时调用一次。
// 总开关 GrowthLifecycleEnabled 默认关：这套任务会真的发积分、赠会员并发邮件，
// 必须由运营在后台确认文案与参数后主动打开，避免部署即群发。
func RunLifecycleJobs() {
	cfg := setting.GetGrowthConfig()
	if !cfg.LifecycleEnabled {
		return
	}
	if n := sendRecallGifts(cfg); n > 0 {
		common.SysLog(fmt.Sprintf("lifecycle: recall gift sent to %d users", n))
	}
	if n := remindMembershipRenew(cfg); n > 0 {
		common.SysLog(fmt.Sprintf("lifecycle: renew reminder sent to %d users", n))
	}
	if n := remindPointsExpiring(cfg); n > 0 {
		common.SysLog(fmt.Sprintf("lifecycle: points expiry reminder sent to %d users", n))
	}
}

const recallLink = "/studio?from=recall"

// sendRecallGifts 长期未登录用户：发回归礼（积分 + 会员天数）并站内信 + 邮件。
func sendRecallGifts(cfg setting.GrowthConfig) int {
	if cfg.RecallInactiveDays <= 0 || cfg.RecallPoints <= 0 {
		return 0
	}
	now := common.GetTimestamp()
	idle := now - int64(cfg.RecallInactiveDays)*86400
	// 只看一年内登录过的（更早的多半已流失，避免每轮都扫全表）
	floor := now - 365*86400
	var users []*model.User
	if err := model.DB.Where("status = ? AND last_login_at > ? AND last_login_at <= ?", constant.UserStatusEnabled, floor, idle).
		Order("last_login_at desc").Limit(lifecycleBatch * 2).Find(&users).Error; err != nil {
		return 0
	}
	sent := 0
	since := now - 90*86400
	for _, u := range users {
		if sent >= lifecycleBatch {
			break
		}
		if model.HasRecentNotification(u.Id, model.NotificationTypeSystem, recallLink, since) {
			continue
		}
		if err := GiftPointsWithBiz(u.Id, cfg.RecallPoints, cfg.RecallValidDays, setting.GrowthReasonRecall,
			fmt.Sprintf("回归礼 +%d 积分", cfg.RecallPoints)); err != nil {
			continue
		}
		gift := fmt.Sprintf("%d 积分", cfg.RecallPoints)
		if cfg.RecallMemberDays > 0 {
			if err := model.DB.Transaction(func(tx *gorm.DB) error {
				return grantMemberDays(tx, u.Id, cfg.RecallMemberDays, "creator")
			}); err == nil {
				gift = fmt.Sprintf("%d 积分 + %d 天会员", cfg.RecallPoints, cfg.RecallMemberDays)
			}
		}
		title := "好久不见，回归礼已放进你的账户"
		content := fmt.Sprintf("送你 %s，%d 天内有效。这段时间我们上了成片工作室和一键同款，回来试试。", gift, cfg.RecallValidDays)
		model.PushNotification(u.Id, model.NotificationTypeSystem, title, content, recallLink)
		sendLifecycleEmail(u, title, content)
		Track(u.Id, "recall_gift", map[string]any{"points": cfg.RecallPoints, "member_days": cfg.RecallMemberDays})
		sent++
	}
	return sent
}

// remindMembershipRenew 会员到期前提醒。
func remindMembershipRenew(cfg setting.GrowthConfig) int {
	if cfg.RenewRemindDays <= 0 {
		return 0
	}
	now := common.GetTimestamp()
	deadline := now + int64(cfg.RenewRemindDays)*86400
	var list []*model.Membership
	if err := model.DB.Where("status = ? AND expires_at > ? AND expires_at <= ?", model.MembershipStatusActive, now, deadline).
		Order("expires_at asc").Limit(lifecycleBatch).Find(&list).Error; err != nil {
		return 0
	}
	sent := 0
	since := now - int64(cfg.RenewRemindDays)*86400
	for _, m := range list {
		if model.HasRecentNotification(m.UserId, model.NotificationTypeSystem, "/pricing", since) {
			continue
		}
		days := int((m.ExpiresAt - now) / 86400)
		if days < 0 {
			days = 0
		}
		model.PushNotification(m.UserId, model.NotificationTypeSystem, "会员即将到期",
			fmt.Sprintf("你的会员还有 %d 天到期。到期后每月赠送的积分停止发放，已有积分按批次有效期继续可用；现在续费可享年付折扣。", days), "/pricing")
		Track(m.UserId, "renew_remind", map[string]any{"days": days})
		sent++
	}
	return sent
}

// remindPointsExpiring 积分批次到期前提醒（按用户合并一条）。
func remindPointsExpiring(cfg setting.GrowthConfig) int {
	if cfg.PointsExpireRemindDays <= 0 {
		return 0
	}
	now := common.GetTimestamp()
	deadline := now + int64(cfg.PointsExpireRemindDays)*86400
	type row struct {
		UserId int
		Points int
	}
	var rows []row
	if err := model.DB.Model(&model.PointLot{}).Select("user_id, COALESCE(SUM(remaining),0) AS points").
		Where("remaining > 0 AND expires_at > ? AND expires_at <= ?", now, deadline).
		Group("user_id").Order("points desc").Limit(lifecycleBatch).Scan(&rows).Error; err != nil {
		return 0
	}
	sent := 0
	since := now - int64(cfg.PointsExpireRemindDays)*86400
	for _, r := range rows {
		if r.Points <= 0 {
			continue
		}
		if model.HasRecentNotification(r.UserId, model.NotificationTypeSystem, "/studio", since) {
			continue
		}
		model.PushNotification(r.UserId, model.NotificationTypeSystem, "积分即将过期",
			fmt.Sprintf("你有 %d 积分将在 %d 天内过期，用来生成一条视频正好。", r.Points, cfg.PointsExpireRemindDays), "/studio")
		Track(r.UserId, "points_expire_remind", map[string]any{"points": r.Points})
		sent++
	}
	return sent
}

// sendLifecycleEmail 只发给邮箱注册且开了邮件通知的用户，失败只记日志。
func sendLifecycleEmail(u *model.User, title, content string) {
	if u == nil {
		return
	}
	if _, err := mail.ParseAddress(u.Username); err != nil {
		return
	}
	pref, err := model.GetUserNotificationPref(u.Id)
	if err != nil || !pref.EmailEnabled {
		return
	}
	body := fmt.Sprintf(`<div style="font-family:-apple-system,BlinkMacSystemFont,'PingFang SC',sans-serif;font-size:15px;line-height:1.7;color:#171a21">
<p>%s</p><p style="color:#5c6472">%s</p>
<p><a href="%s/studio" style="display:inline-block;padding:10px 18px;background:#0f6e8c;color:#fff;border-radius:8px;text-decoration:none">回创作台看看</a></p>
<p style="color:#9aa2af;font-size:12px">不想收到这类邮件？在 EasyTV 设置 → 通知里可以关闭。</p></div>`,
		title, content, strings.TrimRight(setting.GetGrowthConfig().InviteBaseUrl, "/"))
	if err := common.SendEmail(u.Username, "EasyTV · "+title, body); err != nil {
		common.SysError(fmt.Sprintf("lifecycle email failed user=%d: %v", u.Id, err))
	}
}
