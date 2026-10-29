package service

import (
	"errors"
	"testing"
	"time"

	"easytv-api/model"
	"easytv-api/setting"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// ===== AI 对话每日次数（契约第 4 / 5 / 7 条）=====
//
// 全部走 sqlmock（写法照抄 service/agent_billing_test.go 的 sqlDB + gorm mysql SkipInitializeWithVersion）：
// 不连库、不迁移、不写真实数据。

const (
	chatQuotaDay = "2026-09-20"

	// 默认（未开 clientFoundRows）路径：一条 SQL 的条件自增，先查再写不允许；这条正则把 IF 条件本身钉住。
	chatQuotaIncrRe = "INSERT INTO ai_chat_usage .*ON DUPLICATE KEY UPDATE count = IF\\(\\? <= 0 OR count < \\?, count \\+ 1, count\\)"
	// clientFoundRows=true 时的路径：条件 UPDATE（命中即真加一）+ 没命中才 INSERT IGNORE（真插入=1、撞唯一键=0）。
	// 两种判定都与 affected-rows 的「到顶没动=0/1」语义无关。
	chatQuotaIncrUpdateRe = "UPDATE ai_chat_usage SET count = count \\+ 1, updated_at = \\? WHERE user_id = \\? AND day = \\? AND \\(\\? <= 0 OR count < \\?\\)"
	chatQuotaIncrInsertRe = "INSERT IGNORE INTO ai_chat_usage \\(user_id, day, count, created_at, updated_at\\) VALUES \\(\\?, \\?, 1, \\?, \\?\\)"
	chatQuotaReadRe       = "SELECT count FROM ai_chat_usage WHERE user_id = \\? AND day = \\?"
	chatQuotaDecrRe       = "UPDATE ai_chat_usage SET count = count - 1, updated_at = \\? WHERE user_id = \\? AND day = \\? AND count > 0"
)

func chatQuotaTestDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open failed: %v", err)
	}
	model.DB = db
	return mock
}

// chatQuotaTestDBWithFoundRows 同 chatQuotaTestDB，但 dialector 的 DSN 带 clientFoundRows=true：
// 走 CLIENT_FOUND_ROWS 判定路径。Conn 已给 sqlmock，DSN 只用于开关检测，不会真的连库。
func chatQuotaTestDBWithFoundRows(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		DSN:                       "u:p@tcp(127.0.0.1:3306)/easytv?charset=utf8mb4&clientFoundRows=true",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open failed: %v", err)
	}
	model.DB = db
	return mock
}

// seedChatQuotaSettings 用内存缓存注入设置（不落库、不查库）；用完恢复成缺省。
func seedChatQuotaSettings(t *testing.T, dailyLimit string) {
	t.Helper()
	for k, v := range map[string]string{
		setting.AgentEnabled:       "true",
		setting.ChatFreeEnabled:    "true",
		setting.ChatDailyFreeCount: dailyLimit,
	} {
		key, val := k, v
		model.SetSettingCache(key, val)
		t.Cleanup(func() { model.SetSettingCache(key, "") })
	}
}

// nonMemberRows 非会员：membership 查不到记录（GetActiveMembership → nil）。
func nonMemberRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "status", "expires_at"})
}

func memberRows(userId int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "status", "expires_at", "edition", "seats"}).
		AddRow(1, userId, model.MembershipStatusActive, time.Now().Add(24*time.Hour).Unix(), model.EditionCreator, 1)
}

// TestChatUsageDayTimezone 分日按 Asia/Shanghai：UTC 17:00 已经是次日。
func TestChatUsageDayTimezone(t *testing.T) {
	if got := model.ChatUsageDay(time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC)); got != "2026-09-21" {
		t.Fatalf("UTC 17:00 应算次日，得到 %s", got)
	}
	if got := model.ChatUsageDay(time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)); got != "2026-09-20" {
		t.Fatalf("UTC 03:00 应仍是当天，得到 %s", got)
	}
}

// TestIncrChatUsageFirstConsume 首次消费：条件自增插入行，affected=1 → allowed=true，used=1。
func TestIncrChatUsageFirstConsume(t *testing.T) {
	mock := chatQuotaTestDB(t)
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, chatQuotaDay, sqlmock.AnyArg(), sqlmock.AnyArg(), 100, 100, 100, 100, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(chatQuotaReadRe).
		WithArgs(8, chatQuotaDay).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	used, allowed, err := model.IncrChatUsage(8, chatQuotaDay, 100)
	if err != nil {
		t.Fatalf("incr: %v", err)
	}
	if !allowed || used != 1 {
		t.Fatalf("首次消费 used=%d allowed=%v，want used=1 allowed=true", used, allowed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestIncrChatUsageIncrementBelowCap 已有记录且没到顶：条件自增 affected=2 → allowed=true。
func TestIncrChatUsageIncrementBelowCap(t *testing.T) {
	mock := chatQuotaTestDB(t)
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, chatQuotaDay, sqlmock.AnyArg(), sqlmock.AnyArg(), 3, 3, 3, 3, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery(chatQuotaReadRe).
		WithArgs(8, chatQuotaDay).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	used, allowed, err := model.IncrChatUsage(8, chatQuotaDay, 3)
	if err != nil {
		t.Fatalf("incr: %v", err)
	}
	if !allowed || used != 2 {
		t.Fatalf("未到顶 used=%d allowed=%v，want used=2 allowed=true", used, allowed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestIncrChatUsageAtLimitStopsCounting 到上限（默认连接语义）：条件为假 → affected=0 → allowed=false，
// 且再消费 count 也不涨。CLIENT_FOUND_ROWS 连接走的是另一条路径，见 TestIncrChatUsageFoundRowsAtLimit。
func TestIncrChatUsageAtLimitStopsCounting(t *testing.T) {
	mock := chatQuotaTestDB(t)
	for i := 0; i < 3; i++ {
		mock.ExpectExec(chatQuotaIncrRe).
			WithArgs(8, chatQuotaDay, sqlmock.AnyArg(), sqlmock.AnyArg(), 3, 3, 3, 3, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0)) // 行没变，标准 affected rows = 0
		mock.ExpectQuery(chatQuotaReadRe).
			WithArgs(8, chatQuotaDay).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	}
	for i := 0; i < 3; i++ {
		used, allowed, err := model.IncrChatUsage(8, chatQuotaDay, 3)
		if err != nil {
			t.Fatalf("第 %d 次: %v", i+1, err)
		}
		if allowed {
			t.Fatalf("第 %d 次到顶仍 allowed=true", i+1)
		}
		if used != 3 {
			t.Fatalf("第 %d 次 used=%d，到顶后计数不许再涨（want 3）", i+1, used)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestIncrChatUsageUnlimited limit<=0 表示不限：每次条件自增都放行。
func TestIncrChatUsageUnlimited(t *testing.T) {
	for i, limit := range []int{0, -5} {
		mock := chatQuotaTestDB(t)
		mock.ExpectExec(chatQuotaIncrRe).
			WithArgs(8, chatQuotaDay, sqlmock.AnyArg(), sqlmock.AnyArg(), limit, limit, limit, limit, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, int64(i+1))) // 第一次插入=1、第二次自增=2，都是放行
		mock.ExpectQuery(chatQuotaReadRe).
			WithArgs(8, chatQuotaDay).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(41))

		used, allowed, err := model.IncrChatUsage(8, chatQuotaDay, limit)
		if err != nil {
			t.Fatalf("limit=%d incr: %v", limit, err)
		}
		if !allowed || used != 41 {
			t.Fatalf("limit=%d used=%d allowed=%v，want used=41 allowed=true（不限）", limit, used, allowed)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("limit=%d unmet: %v", limit, err)
		}
	}
}

// TestIncrChatUsageFoundRows CLIENT_FOUND_ROWS（clientFoundRows=true）连接下 allowed 判定仍准确：
// 这种连接会把「匹配到但没改」的行也算成 affected（0 → 1），不能再靠 affected-rows 判定到顶，
// 实现改走「条件 UPDATE + INSERT IGNORE」，两个 affected 都只在真的写进去时 >0。
func TestIncrChatUsageFoundRows(t *testing.T) {
	// 首次消费：条件 UPDATE 匹配不到行（0）+ INSERT IGNORE 真插入（1）→ 放行
	mock := chatQuotaTestDBWithFoundRows(t)
	mock.ExpectExec(chatQuotaIncrUpdateRe).
		WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay, 2, 2).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(chatQuotaIncrInsertRe).
		WithArgs(8, chatQuotaDay, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(chatQuotaReadRe).
		WithArgs(8, chatQuotaDay).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	used, allowed, err := model.IncrChatUsage(8, chatQuotaDay, 2)
	if err != nil || !allowed || used != 1 {
		t.Fatalf("首次消费 used=%d allowed=%v err=%v，want used=1 allowed=true", used, allowed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}

	// 到顶：条件 UPDATE 的 WHERE 不成立（0）+ INSERT IGNORE 撞唯一键被忽略（0）→ 拒绝，计数不涨
	mock = chatQuotaTestDBWithFoundRows(t)
	for i := 0; i < 2; i++ {
		mock.ExpectExec(chatQuotaIncrUpdateRe).
			WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay, 2, 2).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(chatQuotaIncrInsertRe).
			WithArgs(8, chatQuotaDay, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(chatQuotaReadRe).
			WithArgs(8, chatQuotaDay).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	}
	for i := 0; i < 2; i++ {
		used, allowed, err := model.IncrChatUsage(8, chatQuotaDay, 2)
		if err != nil {
			t.Fatalf("第 %d 次: %v", i+1, err)
		}
		if allowed || used != 2 {
			t.Fatalf("CLIENT_FOUND_ROWS 下到顶 used=%d allowed=%v，want used=2 allowed=false", used, allowed)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestConsumeChatQuotaFoundRowsExceeded CLIENT_FOUND_ROWS 连接下到顶照样返回 ErrChatQuotaExceeded（不超发）。
func TestConsumeChatQuotaFoundRowsExceeded(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDBWithFoundRows(t)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrUpdateRe).
		WithArgs(sqlmock.AnyArg(), 8, sqlmock.AnyArg(), 2, 2).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(chatQuotaIncrInsertRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(chatQuotaReadRe).
		WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	q, err := ConsumeChatQuota(8, 0)
	if !errors.Is(err, ErrChatQuotaExceeded) {
		t.Fatalf("CLIENT_FOUND_ROWS 下到顶应返回 ErrChatQuotaExceeded，得到 %v", err)
	}
	if q.Used != 2 || q.Remaining != 0 {
		t.Fatalf("到顶返回 used=%d remaining=%d，want used=2 remaining=0", q.Used, q.Remaining)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestGetChatUsageNoRecord 没有记录返回 0，不是错误。
func TestGetChatUsageNoRecord(t *testing.T) {
	mock := chatQuotaTestDB(t)
	mock.ExpectQuery(chatQuotaReadRe).
		WithArgs(8, chatQuotaDay).
		WillReturnRows(sqlmock.NewRows([]string{"count"}))

	used, err := model.GetChatUsage(8, chatQuotaDay)
	if err != nil || used != 0 {
		t.Fatalf("没有记录应返回 0：used=%d err=%v", used, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestDecrChatUsage 退还：count>0 才减（SQL 里带 count > 0，计数不会为负）；没有记录时 affected=0 也不报错。
func TestDecrChatUsage(t *testing.T) {
	mock := chatQuotaTestDB(t)
	mock.ExpectExec(chatQuotaDecrRe).
		WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := model.DecrChatUsage(8, chatQuotaDay); err != nil {
		t.Fatalf("有记录时应减一：%v", err)
	}

	mock.ExpectExec(chatQuotaDecrRe).
		WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 没有记录 / 已经是 0
	if err := model.DecrChatUsage(8, chatQuotaDay); err != nil {
		t.Fatalf("没有记录时不该报错：%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRefundChatQuotaOnRefundsGivenDay 按消费日退还（复核发现 medium）：跨零点失败要退消费日，
// 不能退成退款时刻的今天；拿不到消费日（空串）就不动账，避免把别的日子减错。
func TestRefundChatQuotaOnRefundsGivenDay(t *testing.T) {
	mock := chatQuotaTestDB(t)
	mock.ExpectExec(chatQuotaDecrRe).
		WithArgs(sqlmock.AnyArg(), 8, "2026-09-19"). // 昨天消费、今天失败：退昨天
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := RefundChatQuotaOn(8, "2026-09-19"); err != nil {
		t.Fatalf("按消费日退还失败：%v", err)
	}
	// 空消费日 / 非法用户：不动账（不查库，sqlmock 没登记任何期望）
	if err := RefundChatQuotaOn(8, ""); err != nil {
		t.Fatalf("空消费日应忽略：%v", err)
	}
	if err := RefundChatQuotaOn(0, "2026-09-19"); err != nil {
		t.Fatalf("用户为 0 应忽略：%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRefundChatQuotaUsesToday 兼容入口退今天：当天消费当天失败的路径行为不变。
func TestRefundChatQuotaUsesToday(t *testing.T) {
	mock := chatQuotaTestDB(t)
	mock.ExpectExec(chatQuotaDecrRe).
		WithArgs(sqlmock.AnyArg(), 8, model.ChatUsageDay(time.Now())).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := RefundChatQuota(8); err != nil {
		t.Fatalf("退还失败：%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestConsumeChatQuotaFirstTime 普通用户首次消费：used=1、剩余 limit-1。
func TestConsumeChatQuotaFirstTime(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(chatQuotaReadRe).
		WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	q, err := ConsumeChatQuota(8, 0)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if q.Unlimited || q.Used != 1 || q.Remaining != 1 || q.DailyLimit != 2 {
		t.Fatalf("首次消费 = %+v，want used=1 remaining=1 daily_limit=2 unlimited=false", q)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestConsumeChatQuotaExceeded 到顶：affected=0 → allowed=false → ErrChatQuotaExceeded，且返回真实用量。
func TestConsumeChatQuotaExceeded(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(chatQuotaReadRe).
		WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	q, err := ConsumeChatQuota(8, 0)
	if !errors.Is(err, ErrChatQuotaExceeded) {
		t.Fatalf("到顶应返回 ErrChatQuotaExceeded，得到 %v", err)
	}
	if q.Used != 2 || q.Remaining != 0 {
		t.Fatalf("到顶返回 used=%d remaining=%d，want used=2 remaining=0", q.Used, q.Remaining)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestConsumeChatQuotaMemberDoesNotWrite 会员不限次：完全不碰 ai_chat_usage（连读都没有）。
func TestConsumeChatQuotaMemberDoesNotWrite(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(memberRows(8))
	// 不登记任何 ai_chat_usage 的期望：一旦代码去读写计数，sqlmock 会报「call was not expected」，测试即失败。

	q, err := ConsumeChatQuota(8, 0)
	if err != nil {
		t.Fatalf("member consume: %v", err)
	}
	if !q.IsMember || !q.Unlimited || q.Remaining != -1 || q.Used != 0 {
		t.Fatalf("会员配额 = %+v，want is_member=true unlimited=true remaining=-1 used=0", q)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestGetChatQuotaMember 只读接口同样给会员不限次口径，且不读计数。
func TestGetChatQuotaMember(t *testing.T) {
	seedChatQuotaSettings(t, "100")
	mock := chatQuotaTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(memberRows(8))

	q, err := GetChatQuota(8, 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !q.IsMember || !q.Unlimited || q.Remaining != -1 {
		t.Fatalf("会员配额 = %+v，want unlimited=true remaining=-1", q)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestConsumeChatQuotaUnlimitedWhenLimitDisabled limit<=0（配置成不限）：不写计数直接放行。
func TestConsumeChatQuotaUnlimitedWhenLimitDisabled(t *testing.T) {
	seedChatQuotaSettings(t, "0")
	mock := chatQuotaTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())

	q, err := ConsumeChatQuota(9, 0)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !q.Unlimited || q.Remaining != -1 {
		t.Fatalf("limit=0 应不限：%+v", q)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestIsFreeChatTask free=true 的 agent 任务算免费对话；缺失 / false / 非 agent 任务都不算。
func TestIsFreeChatTask(t *testing.T) {
	cases := []struct {
		name string
		task *model.GenerationTask
		want bool
	}{
		{"agent+free", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"agent":{"question":"做个片子","max_rounds":6,"free":true}}`}, true},
		{"agent 无 free", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"agent":{"question":"做个片子","max_rounds":6}}`}, false},
		{"agent free=false", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"agent":{"question":"做个片子","max_rounds":6,"free":false}}`}, false},
		{"非文本任务", &model.GenerationTask{Type: model.TaskTypeImage, Params: `{"agent":{"question":"做个片子","max_rounds":6,"free":true}}`}, false},
		{"非 JSON", &model.GenerationTask{Type: model.TaskTypeText, Params: `not json`}, false},
		{"nil", nil, false},
		{"导演+free", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"chat":{"kind":"director","free":true}}`}, true},
		{"导演 free=false", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"chat":{"kind":"director","free":false}}`}, false},
		{"导演无 kind", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"chat":{"free":true}}`}, false},
	}
	for _, tc := range cases {
		if got := IsFreeChatTask(tc.task); got != tc.want {
			t.Fatalf("%s: IsFreeChatTask = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestChatQuotaExceededMessage(t *testing.T) {
	if got := ChatQuotaExceededMessage(100); got != "今日 AI 对话次数已用完（100 次），开通会员可无限次使用" {
		t.Fatalf("message = %q", got)
	}
	if ChatQuotaExceededReason != "chat_quota_exceeded" {
		t.Fatalf("reason = %q", ChatQuotaExceededReason)
	}
}
