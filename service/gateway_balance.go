package service

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/setting"
)

// 网关余额（API.md 38）：seedance 曾因网关账户欠费（402「余额 $-0.81」）成批失败，没人知道。
// 网关是 one-api 一系，sk 令牌可查 OpenAI 兼容的账单接口；这里缓存 5 分钟给定价页展示，
// 后台每 6 小时巡检一次，低于阈值给管理员发 system 通知，24 小时内不重复。

type GatewayBalance struct {
	// RemainUSD 剩余额度（美元）；Unlimited 时无意义
	RemainUSD float64 `json:"remain_usd"`
	// UsedUSD 累计用量（美元）；网关不支持时为 0
	UsedUSD float64 `json:"used_usd"`
	// Unlimited sk 令牌不限额：账单接口只回占位值，看不到账户余额（要配系统访问令牌走账户接口）
	Unlimited bool `json:"unlimited"`
	// Source account = 网关账户（/api/user/self）；token = sk 令牌账单接口
	Source    string `json:"source"`
	FetchedAt int64  `json:"fetched_at"`
}

// GatewayBalanceFetcher 由 providers 包在 init 里注入（service 不能反向 import providers）。
var GatewayBalanceFetcher func() (GatewayBalance, error)

const (
	gatewayBalanceCacheTTL = 5 * time.Minute
	// GatewayBalanceAlertCooldown 同一次「余额低」24 小时只提醒一次
	GatewayBalanceAlertCooldown = 24 * time.Hour
	// GatewayBalanceAlertNotifiedKey setting 里记上次提醒的 Unix 秒
	GatewayBalanceAlertNotifiedKey = "GatewayBalanceAlertNotifiedAt"
)

var (
	gatewayBalanceMu     sync.Mutex
	gatewayBalanceCache  *GatewayBalance
	gatewayBalanceCached time.Time
)

// InvalidateGatewayBalanceCache 网关配置改动后强制下次重取。
func InvalidateGatewayBalanceCache() {
	gatewayBalanceMu.Lock()
	gatewayBalanceCache, gatewayBalanceCached = nil, time.Time{}
	gatewayBalanceMu.Unlock()
}

// CachedGatewayBalance 取余额，5 分钟内复用；force 强制刷新。没接网关 / 网关不支持时返回错误。
func CachedGatewayBalance(force bool) (GatewayBalance, error) {
	gatewayBalanceMu.Lock()
	defer gatewayBalanceMu.Unlock()
	if !force && gatewayBalanceCache != nil && time.Since(gatewayBalanceCached) < gatewayBalanceCacheTTL {
		return *gatewayBalanceCache, nil
	}
	if GatewayBalanceFetcher == nil {
		return GatewayBalance{}, errors.New("网关未启用")
	}
	b, err := GatewayBalanceFetcher()
	if err != nil {
		return GatewayBalance{}, err
	}
	b.FetchedAt = time.Now().Unix()
	gatewayBalanceCache, gatewayBalanceCached = &b, time.Now()
	return b, nil
}

// gatewayBalanceShouldAlert 纯函数：余额低于阈值且 24 小时内没提醒过才提醒；阈值 ≤0 关闭。
func gatewayBalanceShouldAlert(remain, threshold float64, lastNotified int64, now time.Time) bool {
	if threshold <= 0 || remain >= threshold {
		return false
	}
	return lastNotified <= 0 || now.Sub(time.Unix(lastNotified, 0)) >= GatewayBalanceAlertCooldown
}

// RunGatewayBalanceAlert 定时任务入口（main.go，联调实例不跑）。
func RunGatewayBalanceAlert() {
	threshold := setting.GetGatewayBalanceAlertUSD()
	if threshold <= 0 || GatewayBalanceFetcher == nil {
		return
	}
	b, err := CachedGatewayBalance(true)
	if err != nil {
		common.SysError("gateway balance alert: " + err.Error())
		return
	}
	if b.Unlimited {
		// 令牌不限额、又没配账户访问令牌：看不到余额，没法巡检（定价页会提示去配）
		return
	}
	last, _ := strconv.ParseInt(model.GetSetting(GatewayBalanceAlertNotifiedKey), 10, 64)
	now := time.Now()
	if !gatewayBalanceShouldAlert(b.RemainUSD, threshold, last, now) {
		// 余额回到阈值以上就清掉记录，下次再低会马上提醒
		if b.RemainUSD >= threshold && last > 0 {
			_ = model.UpdateSetting(GatewayBalanceAlertNotifiedKey, "")
		}
		return
	}
	var admins []int
	if err := model.DB.Model(&model.User{}).Where("role >= ? AND status = ?", constant.RoleAdminUser, constant.UserStatusEnabled).Pluck("id", &admins).Error; err != nil {
		common.SysError("gateway balance alert: " + err.Error())
		return
	}
	title := fmt.Sprintf("网关余额只剩 $%.2f，低于 $%.0f", b.RemainUSD, threshold)
	content := fmt.Sprintf("EasyAPI 网关剩余额度 $%.2f（累计用量 $%.2f）。余额用尽后所有生成任务都会失败并退积分，请尽快充值。", b.RemainUSD, b.UsedUSD)
	if b.RemainUSD < 0 {
		title = fmt.Sprintf("网关已欠费 $%.2f，生成任务正在失败", -b.RemainUSD)
	}
	model.PushNotificationToUsers(admins, model.NotificationTypeSystem, title, content, "")
	if err := model.UpdateSetting(GatewayBalanceAlertNotifiedKey, strconv.FormatInt(now.Unix(), 10)); err != nil {
		common.SysError("gateway balance alert: " + err.Error())
	}
	common.SysLog(fmt.Sprintf("gateway balance alert: $%.2f < $%.0f，已通知 %d 位管理员", b.RemainUSD, threshold, len(admins)))
}
