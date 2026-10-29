package router

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/setting"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// ===== 入口闸门（复核发现）：客户端自带 params.agent.free、通用重试复制旧 free 标记 =====

const (
	testFreeAgentParams = `{"agent":{"question":"做个片子","max_rounds":3,"free":true}}`
	testPaidAgentParams = `{"agent":{"question":"做个片子","max_rounds":3}}`
)

// newAuthedEngine 起真实路由，用 sqlmock 伪造一个有效 access_token 的登录态；不连库、不迁移。
func newAuthedEngine(t *testing.T) (*gin.Engine, sqlmock.Sqlmock) {
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
	mock.ExpectQuery("SELECT .* FROM `user` WHERE access_token").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "role", "status", "group", "access_token"}).
			AddRow(8, "u8", 1, 1, "default", "tok"))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetApiRouter(r)
	return r, mock
}

// doAuthedPost 以登录态发一个 POST，返回响应体。
func doAuthedPost(t *testing.T, r *gin.Engine, path, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("EasyTV-User", "8")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return strings.TrimSpace(w.Body.String())
}

// expectFailedTask 登记 times 次「按 id + user_id 读任务」的返回：失败状态、给定 params。
func expectFailedTask(mock sqlmock.Sqlmock, params string, times int) {
	for i := 0; i < times; i++ {
		mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\? AND user_id = \\?").
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status", "type", "model", "prompt", "params"}).
				AddRow(41, 8, "failed", "text", "zz-text", "x", params))
	}
}

// TestUserTaskRejectsClientChatFree 复核发现的复现体：params 由客户端原样提供，
// 自带 agent.free=true 的建任务请求必须被入口拦下，不能走到 StartTask 的免积分分支。
func TestUserTaskRejectsClientChatFree(t *testing.T) {
	r, _ := newAuthedEngine(t)
	body := `{"type":"text","model":"zz-text","prompt":"写一段 500 字的营销文案",` +
		`"params":{"agent":{"question":"写一段 500 字的营销文案","mode":"generate","max_rounds":3,"free":true}}}`
	resp := doAuthedPost(t, r, "/user/task", body)
	if !strings.Contains(resp, `"success":false`) || !strings.Contains(resp, "agent.free") {
		t.Fatalf("带 params.agent.free 的建任务请求应被入口拦下，得到 %s", resp)
	}
}

// TestUserTaskWithoutFreeStillReachesHandler 不带 free 的请求照常进 CreateTask，闸门不误伤正常流程。
func TestUserTaskWithoutFreeStillReachesHandler(t *testing.T) {
	r, _ := newAuthedEngine(t)
	body := `{"type":"text","model":"zz-text","prompt":"x",` +
		`"params":{"agent":{"question":"x","mode":"generate","max_rounds":3}}}`
	resp := doAuthedPost(t, r, "/user/task", body)
	if strings.Contains(resp, "agent.free") {
		t.Fatalf("不带 free 的请求不该被入口拦下：%s", resp)
	}
	if !strings.Contains(resp, "zz-text") {
		t.Fatalf("请求应到达 CreateTask（模型未接入的报错里带模型名），得到 %s", resp)
	}
}

// TestRetryFreeTaskReevaluatesWhenChatFreeOff 复核发现（medium）的处置：开关关闭后重试免费期创建的对话任务，
// 不能再原样复制 free=true 免单。本轮口径是「按当前开关重估」而不是「入口拒绝」——重估在 RetryMyTask
// （service.RewriteChatFreeFlag，与 NewAgentRetryTask 一致），所以这里断言请求到达 RetryMyTask、
// 不被免费闸门拦下，且来源任务只被控制器读一次（旧的 GuardRetryChatFree 挂载已从路由摘掉）。
// 「free 被重估为 false 并走付费估价路径（预扣积分、不碰 ai_chat_usage）」的完整断言见
// tests/ai_chat_free_flag_test.go 的 TestRetryAgentTaskReevaluatesFreeFlag。
func TestRetryFreeTaskReevaluatesWhenChatFreeOff(t *testing.T) {
	model.SetSettingCache(setting.ChatFreeEnabled, "false")
	t.Cleanup(func() { model.SetSettingCache(setting.ChatFreeEnabled, "") })
	r, mock := newAuthedEngine(t)
	expectFailedTask(mock, testFreeAgentParams, 1) // 只有 RetryMyTask 读来源任务

	resp := doAuthedPost(t, r, "/user/task/41/retry", "")
	if strings.Contains(resp, "对话免费已关闭") {
		t.Fatalf("重试应按当前开关重估 free，而不是被入口拒绝：%s", resp)
	}
	if !strings.Contains(resp, "zz-text") {
		t.Fatalf("请求应到达 RetryMyTask（模型未接入的报错里带模型名），得到 %s", resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("来源任务只应被控制器读一次（免费闸门已摘除）: %v", err)
	}
}

// TestRetryPaidTaskPassesWhenChatFreeOff 开关关闭只拦免费任务：非免费任务照常重试（闸门读一次 + 控制器再读一次）。
func TestRetryPaidTaskPassesWhenChatFreeOff(t *testing.T) {
	model.SetSettingCache(setting.ChatFreeEnabled, "false")
	t.Cleanup(func() { model.SetSettingCache(setting.ChatFreeEnabled, "") })
	r, mock := newAuthedEngine(t)
	expectFailedTask(mock, testPaidAgentParams, 2)

	resp := doAuthedPost(t, r, "/user/task/41/retry", "")
	if strings.Contains(resp, "对话免费已关闭") {
		t.Fatalf("非免费任务不该被闸门拦下：%s", resp)
	}
	if !strings.Contains(resp, "zz-text") {
		t.Fatalf("请求应到达 RetryMyTask（模型未接入的报错里带模型名），得到 %s", resp)
	}
}

// TestRetryFreeTaskPassesWhenChatFreeOn 开关开启时不查库放行：免费期内重试照旧（由控制器读任务）。
func TestRetryFreeTaskPassesWhenChatFreeOn(t *testing.T) {
	model.SetSettingCache(setting.ChatFreeEnabled, "true")
	t.Cleanup(func() { model.SetSettingCache(setting.ChatFreeEnabled, "") })
	r, mock := newAuthedEngine(t)
	expectFailedTask(mock, testFreeAgentParams, 1)

	resp := doAuthedPost(t, r, "/user/task/41/retry", "")
	if strings.Contains(resp, "对话免费已关闭") {
		t.Fatalf("开关开启时不该拦免费任务的重试：%s", resp)
	}
	if !strings.Contains(resp, "zz-text") {
		t.Fatalf("请求应到达 RetryMyTask（模型未接入的报错里带模型名），得到 %s", resp)
	}
}
