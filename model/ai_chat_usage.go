package model

import (
	"time"

	"easytv-api/common"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
)

// AIChatUsage AI 对话每日次数：一人一天一行，day 按 Asia/Shanghai 取 yyyy-mm-dd（与签到同一时区口径）。
type AIChatUsage struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"uniqueIndex:idx_ai_chat_usage_user_day,priority:1;comment:用户ID"`
	Day       string `json:"day" gorm:"type:varchar(10);uniqueIndex:idx_ai_chat_usage_user_day,priority:2;comment:对话日 yyyy-mm-dd"`
	Count     int    `json:"count" gorm:"default:0;comment:当天已用次数"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}

func (AIChatUsage) TableName() string { return "ai_chat_usage" }

// ChatUsageDay 时间 → 对话日字符串（分日沿用 model.CheckinLocation，Asia/Shanghai）。
func ChatUsageDay(t time.Time) string { return t.In(CheckinLocation).Format("2006-01-02") }

// GetChatUsage 读取某天已用次数；没有记录时返回 0（不是错误）。
func GetChatUsage(userId int, day string) (int, error) {
	used := 0
	if err := DB.Raw("SELECT count FROM ai_chat_usage WHERE user_id = ? AND day = ?", userId, day).Scan(&used).Error; err != nil {
		return 0, err
	}
	return used, nil
}

// chatUsageClientFoundRows 当前连接是否开了 CLIENT_FOUND_ROWS（DSN 参数 clientFoundRows=true）。
// 开着时 MySQL 会把「匹配到但没改动」的行也算成 affected（0 → 1），不能再靠 affected-rows 的
// 「到顶没动=0」判定放行。gorm 的 mysql.Open 已把 DSN 解析进 DSNConfig；测试用的无 DSN dialector
// 以及其它自定义 dialector 按「未开」处理（标准 affected-rows 语义）。
func chatUsageClientFoundRows() bool {
	d, ok := DB.Dialector.(*mysql.Dialector)
	if !ok || d == nil || d.Config == nil {
		return false
	}
	if d.DSNConfig != nil {
		return d.DSNConfig.ClientFoundRows
	}
	if d.DSN == "" {
		return false
	}
	cfg, err := drivermysql.ParseDSN(d.DSN)
	return err == nil && cfg.ClientFoundRows
}

// IncrChatUsage 原子消费一次当日次数，limit<=0 表示不限。
//
// allowed 的判定在两种连接语义下都只认「真的写进去了」：
//   - 默认（未开 clientFoundRows）：一条 INSERT ... ON DUPLICATE KEY UPDATE 条件自增，
//     标准 affected-rows 语义 插入=1 / 自增=2 / 到顶没动=0（契约第 4 条）；
//   - 开了 clientFoundRows=true：到顶那次「匹配到但没改」的行会返回 1，与插入无法区分，改用
//     两条各自原子的写（条件 UPDATE + INSERT IGNORE）判定，见 incrChatUsageConditionalWrites。
//
// 两条路径都没有「先查再写」的窗口：自增由条件写 + 行锁保证，插入由唯一键保证；随后读回 count 作为 used。
func IncrChatUsage(userId int, day string, limit int) (used int, allowed bool, err error) {
	now := common.GetTimestamp()
	if chatUsageClientFoundRows() {
		allowed, err = incrChatUsageConditionalWrites(userId, day, limit, now)
	} else {
		allowed, err = incrChatUsageOnDuplicate(userId, day, limit, now)
	}
	if err != nil {
		return 0, false, err
	}
	used, err = GetChatUsage(userId, day)
	if err != nil {
		return 0, false, err
	}
	return used, allowed, nil
}

// incrChatUsageOnDuplicate 标准 affected-rows 语义下的条件自增：
// 到顶时 count 与 updated_at 都保持原值（affected=0），插入=1、自增=2。
func incrChatUsageOnDuplicate(userId int, day string, limit int, now int64) (bool, error) {
	res := DB.Exec(
		"INSERT INTO ai_chat_usage (user_id, day, count, created_at, updated_at) VALUES (?, ?, 1, ?, ?) "+
			"ON DUPLICATE KEY UPDATE count = IF(? <= 0 OR count < ?, count + 1, count), "+
			"updated_at = IF(? <= 0 OR count < ?, ?, updated_at)",
		userId, day, now, now, limit, limit, limit, limit, now,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// incrChatUsageConditionalWrites CLIENT_FOUND_ROWS 下的判定：拆成两条各自原子的写，两个 affected 都只在
// 「真的写进去了」时 >0 —— 条件 UPDATE 命中就是 count 真加一（行发生真实变化）；没命中（当天没记录或
// 已到顶）才 INSERT IGNORE，真插入=1、撞 (user_id, day) 唯一键被忽略=0。全程不存在「匹配到但没改」的行，
// 因此结论与 clientFoundRows 开关无关，也不会超发。
func incrChatUsageConditionalWrites(userId int, day string, limit int, now int64) (bool, error) {
	res := DB.Exec(
		"UPDATE ai_chat_usage SET count = count + 1, updated_at = ? "+
			"WHERE user_id = ? AND day = ? AND (? <= 0 OR count < ?)",
		now, userId, day, limit, limit,
	)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}
	ins := DB.Exec(
		"INSERT IGNORE INTO ai_chat_usage (user_id, day, count, created_at, updated_at) VALUES (?, ?, 1, ?, ?)",
		userId, day, now, now,
	)
	if ins.Error != nil {
		return false, ins.Error
	}
	return ins.RowsAffected > 0, nil
}

// DecrChatUsage 退还一次当日次数：只有 count>0 才减，没有记录 / 已经为 0 时直接忽略（不报错）。
// 退还路径可能被重复调用（失败与取消各一次），条件更新保证了计数不会被减成负数。
func DecrChatUsage(userId int, day string) error {
	return DB.Exec(
		"UPDATE ai_chat_usage SET count = count - 1, updated_at = ? WHERE user_id = ? AND day = ? AND count > 0",
		common.GetTimestamp(), userId, day,
	).Error
}
