package model

import (
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestAuditMaskHelpers 手机号 / 邮箱 / 普通用户名脱敏，超长输入截断。
func TestAuditMaskHelpers(t *testing.T) {
	cases := []struct {
		fn       func(string) string
		in, want string
	}{
		{MaskMobile, "13800000000", "138****0000"},
		{MaskMobile, "12345", "12345"},
		{MaskEmail, "alice@example.com", "a***@example.com"},
		{MaskEmail, "张三@例子.cn", "张***@例子.cn"},
		{MaskEmail, "@example.com", "***@example.com"},
		{MaskAccount, " 13812341234 ", "138****1234"},
		{MaskAccount, "Bob@Mail.com", "B***@Mail.com"},
		{MaskAccount, "admin", "a***n"},
		{MaskAccount, "ab", "***"},
		{MaskAccount, "", ""},
	}
	for _, tc := range cases {
		if got := tc.fn(tc.in); got != tc.want {
			t.Errorf("mask(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	if got := MaskAccount(long); got != "x***x" {
		t.Errorf("超长账号应截断后脱敏: %q", got)
	}
}

// TestAuditThrottleDedupe 同 key 一分钟内只放行一次；窗口过后、不同 key 各自放行；key 过多时内存有界。
func TestAuditThrottleDedupe(t *testing.T) {
	th := newAuditThrottle(time.Minute, 3)
	now := time.Unix(1_000_000, 0)
	if !th.allow("1.1.1.1|GET|/admin/user", now) {
		t.Fatal("首次应放行")
	}
	if th.allow("1.1.1.1|GET|/admin/user", now.Add(59*time.Second)) {
		t.Fatal("窗口内重复应拦截")
	}
	if !th.allow("1.1.1.1|GET|/admin/setting", now.Add(time.Second)) {
		t.Fatal("不同路由应放行")
	}
	if !th.allow("1.1.1.1|GET|/admin/user", now.Add(61*time.Second)) {
		t.Fatal("窗口过后应再次放行")
	}
	// 塞满后：过期 key 先被清掉，仍超限则整表重置
	th.allow("a", now.Add(62*time.Second))
	th.allow("b", now.Add(62*time.Second))
	if !th.allow("c", now.Add(62*time.Second)) || len(th.last) > 3 {
		t.Fatalf("key 数应有界: %d", len(th.last))
	}
}

// TestAuditBufferDropAndFlush 队列满丢弃并计数；写协程启动后 flush 等到已入队事件全部写完。
func TestAuditBufferDropAndFlush(t *testing.T) {
	var mu sync.Mutex
	var written []string
	b := newAuditBuffer(2, func(entries []*AuditLog) error {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range entries {
			written = append(written, e.Action)
		}
		return nil
	})
	// 未启动写协程：前两条入队，第三条丢弃
	if !b.enqueue(&AuditLog{Action: "a"}) || !b.enqueue(&AuditLog{Action: "b"}) {
		t.Fatal("队列未满应入队成功")
	}
	if b.enqueue(&AuditLog{Action: "c"}) {
		t.Fatal("队列满应丢弃")
	}
	if b.dropped.Load() != 1 {
		t.Fatalf("dropped = %d", b.dropped.Load())
	}
	// 未启动时 flush 排不进满队列，超时返回 false
	if b.flush(20 * time.Millisecond) {
		t.Fatal("写协程未启动时 flush 应超时")
	}
	b.start()
	if !b.flush(time.Second) {
		t.Fatal("flush 超时")
	}
	mu.Lock()
	if len(written) != 2 || written[0] != "a" || written[1] != "b" {
		t.Fatalf("written = %v", written)
	}
	mu.Unlock()
	b.enqueue(&AuditLog{Action: "d"})
	if !b.flush(time.Second) {
		t.Fatal("第二次 flush 超时")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(written) != 3 || written[2] != "d" {
		t.Fatalf("written = %v", written)
	}
}

// TestAuditBufferBatchFallback 整批写失败时逐条重试，坏数据不拖累同批其他事件；写入 panic 不带走写协程。
func TestAuditBufferBatchFallback(t *testing.T) {
	var mu sync.Mutex
	var ok []string
	b := newAuditBuffer(10, func(entries []*AuditLog) error {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range entries {
			if e.Action == "panic" {
				panic("boom")
			}
			if e.Action == "bad" {
				return errors.New("bad row")
			}
		}
		for _, e := range entries {
			ok = append(ok, e.Action)
		}
		return nil
	})
	b.enqueue(&AuditLog{Action: "x"})
	b.enqueue(&AuditLog{Action: "bad"})
	b.enqueue(&AuditLog{Action: "y"})
	b.start()
	if !b.flush(time.Second) {
		t.Fatal("flush 超时")
	}
	b.enqueue(&AuditLog{Action: "panic"})
	b.flush(time.Second)
	b.enqueue(&AuditLog{Action: "z"})
	if !b.flush(time.Second) {
		t.Fatal("panic 后写协程应仍在运行")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ok) != 3 || ok[0] != "x" || ok[1] != "y" || ok[2] != "z" {
		t.Fatalf("ok = %v", ok)
	}
}

// TestRecordAuditEventAsync 业务埋点经全局缓冲异步写入：匿名安全事件角色为 0，无上下文失败事件状态码 500。
func TestRecordAuditEventAsync(t *testing.T) {
	var mu sync.Mutex
	var got []*AuditLog
	restore := ReplaceAuditLogWriter(func(entries []*AuditLog) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, entries...)
		return nil
	})
	defer restore()

	RecordAuditEvent(nil, AuditEvent{
		Username: MaskAccount("13800000000"), Category: AuditCategorySecurity,
		Action: AuditActionSmsLoginFailed, Content: "手机验证码登录失败", Failed: true,
		Params: map[string]any{"account": MaskAccount("13800000000"), "reason": "wrong_code"},
	})
	RecordSimpleAudit(nil, AuditCategoryUsage, "noop", "无上下文应跳过", nil)
	if !FlushAuditLogs(time.Second) {
		t.Fatal("flush 超时")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d entries", len(got))
	}
	e := got[0]
	if e.ActorRole != 0 || e.Success || e.Status != 500 || e.Username != "138****0000" || e.EventId == "" || e.CreatedAt == 0 {
		t.Fatalf("entry = %+v", e)
	}
	if e.Other.Op == nil || e.Other.Op.Params["account"] != "138****0000" {
		t.Fatalf("params = %+v", e.Other.Op)
	}
}

func mockAuditDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	old := DB
	DB = db
	t.Cleanup(func() { DB = old })
	return mock
}

// TestPurgeAuditLogsBefore 按 created_at 分批取 id 删除，最后一批不足一批即结束。
func TestPurgeAuditLogsBefore(t *testing.T) {
	mock := mockAuditDB(t)
	oldPause := auditPurgePause
	auditPurgePause = 0
	defer func() { auditPurgePause = oldPause }()

	selectIds := regexp.QuoteMeta("SELECT `id` FROM `audit_log` WHERE created_at < ? LIMIT 2")
	deleteIds := regexp.QuoteMeta("DELETE FROM `audit_log` WHERE id IN")
	mock.ExpectQuery(selectIds).WithArgs(int64(100)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
	mock.ExpectBegin()
	mock.ExpectExec(deleteIds).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	mock.ExpectQuery(selectIds).WithArgs(int64(100)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	mock.ExpectBegin()
	mock.ExpectExec(deleteIds).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	n, err := PurgeAuditLogsBefore(100, 2)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if n, err := PurgeAuditLogsBefore(0, 2); n != 0 || err != nil {
		t.Fatalf("cutoff=0 不应删除: %d %v", n, err)
	}
}

// TestGetAuditLogsFilters 非 root 排除 root 事件；关键词转义通配符并匹配 route / content / action。
func TestGetAuditLogsFilters(t *testing.T) {
	mock := mockAuditDB(t)
	success := false
	filter := AuditLogFilter{Category: AuditCategorySecurity, Operator: "adm", Keyword: "50%_off", Success: &success, StartTimestamp: 10}
	where := regexp.QuoteMeta("WHERE actor_role <> ? AND username LIKE ? AND category = ? AND ((route LIKE ? OR content LIKE ? OR action LIKE ?)) AND created_at >= ? AND success = ?")
	kw := `%50\%\_off%`
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `audit_log` "+where).
		WithArgs(100, "adm%", AuditCategorySecurity, kw, kw, kw, int64(10), false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT \\* FROM `audit_log` "+where+regexp.QuoteMeta(" ORDER BY created_at DESC,id DESC LIMIT 20 OFFSET 40")).
		WithArgs(100, "adm%", AuditCategorySecurity, kw, kw, kw, int64(10), false).
		WillReturnRows(sqlmock.NewRows([]string{"id", "category", "other"}).AddRow(7, AuditCategorySecurity, `{"op":{"action":"admin.access_denied","params":{"reason":"not_logged_in"}}}`))

	logs, total, err := GetAuditLogs(filter, 40, 20, 10)
	if err != nil || total != 1 || len(logs) != 1 || logs[0].Other.Op == nil {
		t.Fatalf("logs=%v total=%d err=%v", logs, total, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
