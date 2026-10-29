package controller

import (
	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
	"time"

	"github.com/gin-gonic/gin"
)

// AdminGetSettings 全部设置（GET /admin/setting）。
// 密钥类配置只下发占位值；短信项填实际生效值（含 env 基线）。
func AdminGetSettings(c *gin.Context) {
	settings := model.GetAllSettings()
	setting.MaskSecretSettings(settings)
	setting.SmsSettingsForAdmin(settings)
	common.ApiSuccess(c, gin.H{
		"settings": settings,
	})
}

// AdminUpdateSetting 更新单个设置（PUT /admin/setting/:key，body: {"value": "..."}）。
func AdminUpdateSetting(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	// 回传占位值说明没改密钥，不能把星号写进去覆盖真值
	if req.Value == setting.MaskedSecret && setting.IsSecretSettingKey(key) {
		common.ApiSuccess(c, nil)
		return
	}
	// AI 对话的长度受限键在这里也挡一次，避免绕过专用接口把过长的值写进去（见 controller/admin_chat.go）
	if msg := validChatSettingWrite(key, req.Value); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if err := model.UpdateSetting(key, req.Value); err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	if setting.IsSmsSettingKey(key) {
		setting.LoadSmsConfigFromDB() // 立即生效，不等下一轮 60 秒同步
	}
	common.ApiSuccess(c, nil)
}

// AdminGetSystemStats 系统概况（用户数/充值总额等，简单统计）。
func AdminGetSystemStats(c *gin.Context) {
	var userCount int64
	model.DB.Model(&model.User{}).Count(&userCount)

	var rechargeTotal int64
	var rechargeMoney float64
	model.DB.Model(&model.Recharge{}).Where("status = ?", common.RechargeStatusSuccess).Count(&rechargeTotal)
	model.DB.Model(&model.Recharge{}).Where("status = ?", common.RechargeStatusSuccess).Select("COALESCE(SUM(money), 0)").Scan(&rechargeMoney)

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()

	count := func(m any, where string, args ...any) int64 {
		var n int64
		model.DB.Model(m).Where(where, args...).Count(&n)
		return n
	}
	sumFloat := func(m any, col, where string, args ...any) float64 {
		var v struct{ Total float64 }
		model.DB.Model(m).Select("COALESCE(SUM("+col+"),0) AS total").Where(where, args...).Scan(&v)
		return v.Total
	}
	sumInt := func(m any, col, where string, args ...any) int64 {
		var v struct{ Total int64 }
		model.DB.Model(m).Select("COALESCE(SUM("+col+"),0) AS total").Where(where, args...).Scan(&v)
		return v.Total
	}

	// 渠道分组统计（订单按 payment_method，充值按 payment_provider）；缺失渠道归入 ""。
	type channelStat struct {
		Channel string
		Count   int64
		Money   float64
	}
	channelStatOf := func(m any, channelCol, moneyCol, where string, args ...any) map[string]channelStat {
		rows := make([]channelStat, 0, 4)
		model.DB.Model(m).
			Select("COALESCE("+channelCol+",'') AS channel, COUNT(*) AS count, COALESCE(SUM("+moneyCol+"),0) AS money").
			Where(where, args...).Group(channelCol).Scan(&rows)
		out := make(map[string]channelStat, len(rows))
		for _, r := range rows {
			out[r.Channel] = r
		}
		return out
	}
	orderChanToday := channelStatOf(&model.Order{}, "payment_method", "price", "status = ? AND paid_at >= ?", model.OrderStatusPaid, dayStart)
	orderChanMonth := channelStatOf(&model.Order{}, "payment_method", "price", "status = ? AND paid_at >= ?", model.OrderStatusPaid, monthStart)
	rechargeChanToday := channelStatOf(&model.Recharge{}, "payment_provider", "money", "status = ? AND complete_time >= ?", common.RechargeStatusSuccess, dayStart)
	rechargeChanMonth := channelStatOf(&model.Recharge{}, "payment_provider", "money", "status = ? AND complete_time >= ?", common.RechargeStatusSuccess, monthStart)
	refundedMoneyMonth := sumFloat(&model.Order{}, "price", "status = ? AND refunded_at >= ?", model.OrderStatusRefunded, monthStart)

	// 最近 14 天每日：新用户 / 生成任务 / 订单收入
	type dayRow struct {
		Day   string  `json:"day"`
		Users int64   `json:"users"`
		Tasks int64   `json:"tasks"`
		Money float64 `json:"money"`
	}
	days := make([]dayRow, 0, 14)
	for i := 13; i >= 0; i-- {
		d := time.Date(now.Year(), now.Month(), now.Day()-i, 0, 0, 0, 0, now.Location())
		from, to := d.Unix(), d.AddDate(0, 0, 1).Unix()
		days = append(days, dayRow{
			Day:   d.Format("01-02"),
			Users: count(&model.User{}, "created_at >= ? AND created_at < ?", from, to),
			Tasks: count(&model.GenerationTask{}, "created_at >= ? AND created_at < ?", from, to),
			Money: sumFloat(&model.Order{}, "price", "status = ? AND paid_at >= ? AND paid_at < ?", model.OrderStatusPaid, from, to),
		})
	}

	common.ApiSuccess(c, gin.H{
		// 兼容旧字段
		"user_count":     userCount,
		"recharge_count": rechargeTotal,
		"recharge_money": rechargeMoney,

		"users": gin.H{
			"total":   userCount,
			"today":   count(&model.User{}, "created_at >= ?", dayStart),
			"members": count(&model.Membership{}, "status = ? AND expires_at > ?", model.MembershipStatusActive, now.Unix()),
		},
		"content": gin.H{
			"works_published": count(&model.Work{}, "status = ?", model.WorkStatusPublished),
			"projects":        count(&model.Project{}, "1 = 1"),
			"canvases":        count(&model.Canvas{}, "1 = 1"),
			"assets":          count(&model.Asset{}, "1 = 1"),
			"skills":          count(&model.Skill{}, "status = ?", model.SkillStatusOn),
		},
		"tasks": gin.H{
			"total":     count(&model.GenerationTask{}, "1 = 1"),
			"today":     count(&model.GenerationTask{}, "created_at >= ?", dayStart),
			"running":   count(&model.GenerationTask{}, "status IN ?", []string{model.TaskStatusPending, model.TaskStatusRunning}),
			"succeeded": count(&model.GenerationTask{}, "status = ?", model.TaskStatusSucceeded),
			"failed":    count(&model.GenerationTask{}, "status = ?", model.TaskStatusFailed),
		},
		"points": gin.H{
			"consumed_today": -sumInt(&model.PointLog{}, "amount", "type = ? AND created_at >= ?", model.PointLogFreeze, dayStart),
			"consumed_month": -sumInt(&model.PointLog{}, "amount", "type = ? AND created_at >= ?", model.PointLogFreeze, monthStart),
			"granted_month":  sumInt(&model.PointLog{}, "amount", "type = ? AND created_at >= ?", model.PointLogGrant, monthStart),
		},
		"orders": gin.H{
			"paid_today":           count(&model.Order{}, "status = ? AND paid_at >= ?", model.OrderStatusPaid, dayStart),
			"money_today":          sumFloat(&model.Order{}, "price", "status = ? AND paid_at >= ?", model.OrderStatusPaid, dayStart),
			"paid_month":           count(&model.Order{}, "status = ? AND paid_at >= ?", model.OrderStatusPaid, monthStart),
			"money_month":          sumFloat(&model.Order{}, "price", "status = ? AND paid_at >= ?", model.OrderStatusPaid, monthStart),
			"pending":              count(&model.Order{}, "status = ?", model.OrderStatusPending),
			"refunded_month":       count(&model.Order{}, "status = ? AND refunded_at >= ?", model.OrderStatusRefunded, monthStart),
			"refunded_money_month": refundedMoneyMonth,
			"wechat": gin.H{
				"paid_today":  orderChanToday[model.PaymentMethodWechatNative].Count,
				"money_today": orderChanToday[model.PaymentMethodWechatNative].Money,
				"paid_month":  orderChanMonth[model.PaymentMethodWechatNative].Count,
				"money_month": orderChanMonth[model.PaymentMethodWechatNative].Money,
			},
			"alipay": gin.H{
				"paid_today":  orderChanToday[model.PaymentMethodAlipayF2F].Count,
				"money_today": orderChanToday[model.PaymentMethodAlipayF2F].Money,
				"paid_month":  orderChanMonth[model.PaymentMethodAlipayF2F].Count,
				"money_month": orderChanMonth[model.PaymentMethodAlipayF2F].Money,
			},
		},
		"recharge": gin.H{
			"wechat": gin.H{
				"count_today": rechargeChanToday[model.PaymentProviderWechatNative].Count,
				"money_today": rechargeChanToday[model.PaymentProviderWechatNative].Money,
				"count_month": rechargeChanMonth[model.PaymentProviderWechatNative].Count,
				"money_month": rechargeChanMonth[model.PaymentProviderWechatNative].Money,
			},
			"alipay": gin.H{
				"count_today": rechargeChanToday[model.PaymentProviderAlipayF2F].Count,
				"money_today": rechargeChanToday[model.PaymentProviderAlipayF2F].Money,
				"count_month": rechargeChanMonth[model.PaymentProviderAlipayF2F].Count,
				"money_month": rechargeChanMonth[model.PaymentProviderAlipayF2F].Money,
			},
		},
		"daily": days,
	})
}
