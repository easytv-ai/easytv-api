package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/router"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// initMockDB 使用 sqlmock 初始化 GORM（无需真实 MySQL）。
// sqlmock 默认对未注册的查询返回错误 → 可覆盖 DB 错误路径。
func initMockDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open failed: %v", err)
	}
	model.DB = db
	return mock
}

func initTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	router.SetRouter(r)
	return r
}

func do(t *testing.T, r *gin.Engine, method, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// TestAnonymousEndpoints 匿名接口契约测试（无需 DB 交互的路径）。
func TestAnonymousEndpoints(t *testing.T) {
	initMockDB(t)
	r := initTestRouter()

	// 1. 用户分组（匿名可访问）
	code, resp := do(t, r, "GET", "/user/groups", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("groups: code=%d resp=%v", code, resp)
	}

	// 2. OAuth providers 列表（匿名可访问）
	code, resp = do(t, r, "GET", "/oauth/providers", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("oauth providers: code=%d resp=%v", code, resp)
	}

	// 3. 未登录访问 /user/self 应返回鉴权失败
	code, resp = do(t, r, "GET", "/user/self", "", nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("unauth self should fail: code=%d resp=%v", code, resp)
	}

	// 4. 微信 preauth 未配置 → 返回配置缺失（接口存在、契约正确）
	code, resp = do(t, r, "POST", "/wechat/oauth/preauth", `{}`, nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("wechat preauth: code=%d resp=%v", code, resp)
	}

	// 5. 微信 login-status 缺 evidence → expired
	code, resp = do(t, r, "GET", "/wechat/oauth/login-status?evidence=", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("wechat login-status: code=%d resp=%v", code, resp)
	}

	// 6. 未知 OAuth provider → 错误提示
	code, resp = do(t, r, "GET", "/oauth/unknown?state=abc", "", nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("unknown oauth: code=%d resp=%v", code, resp)
	}

	// 7. 2FA / passkey / console exchange 占位接口存在（返回 200 与错误提示）
	for _, path := range []string{
		"/user/login/2fa", "/user/passkey/login/begin", "/user/passkey/login/finish",
		"/user/console_access/exchange",
	} {
		code, resp = do(t, r, "POST", path, `{}`, nil)
		if code != http.StatusOK {
			t.Fatalf("%s should return 200: code=%d", path, code)
		}
	}

	// 8. 验证码发送（EmailVerificationEnabled=false → 走内存兜底，接口可用）
	code, resp = do(t, r, "GET", "/verification?email=new@example.com", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("verification: code=%d resp=%v", code, resp)
	}
}

// TestAuthValidation 鉴权校验逻辑测试（EasyTV-User 头 + 无效 token → 鉴权失败）。
func TestAuthValidation(t *testing.T) {
	initMockDB(t)
	r := initTestRouter()

	headers := map[string]string{
		"Authorization": "Bearer invalidtoken",
		"EasyTV-User":   "1",
	}
	// sqlmock 未注册查询 → DB 报错 → 鉴权失败（非 500，统一 JSON 错误）
	code, resp := do(t, r, "GET", "/user/self", "", headers)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("invalid token should fail: code=%d resp=%v", code, resp)
	}

	// 缺少 EasyTV-User 头 → 鉴权失败
	headersNoUser := map[string]string{
		"Authorization": "Bearer sometoken",
	}
	code, resp = do(t, r, "GET", "/user/self", "", headersNoUser)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("missing EasyTV-User should fail: code=%d resp=%v", code, resp)
	}
}

// TestSmsLogin 手机号验证码登录（验证码走内存兜底，SubMail 发送为联调日志模式）。
func TestSmsLogin(t *testing.T) {
	const mobile = "13812341234"

	// 1. 手机号格式错误（不触达 DB）
	initMockDB(t)
	r := initTestRouter()
	code, resp := do(t, r, "POST", "/user/sms_login", `{"mobile":"12345","code":"123456"}`, nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("bad mobile should fail: code=%d resp=%v", code, resp)
	}

	// 2. 验证码错误（内存兜底无记录 → 校验失败，不触达 DB）
	code, resp = do(t, r, "POST", "/user/sms_login", `{"mobile":"`+mobile+`","code":"000000"}`, nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("wrong code should fail: code=%d resp=%v", code, resp)
	}

	// 3. 成功登录：自动注册 + 签发 access_token
	mock := initMockDB(t)
	r = initTestRouter()
	common.RegisterVerificationCodeWithKey(mobile, "123456", common.SmsLoginPurpose)
	// 手机号未注册（Unscoped 查询，0 行）→ 自动注册（GORM 写操作默认带事务）
	mock.ExpectQuery("SELECT .* FROM `user` WHERE username").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	// setupLogin：记录登录时间 → 读用户（access_token 为空）→ 查重 → 写入新 token
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `user` SET .*last_login_at").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	userRow := sqlmock.NewRows([]string{
		"id", "username", "password", "nickname", "role", "status", "github_id", "discord_id",
		"oidc_id", "wechat_id", "wechat_nickname", "telegram_id", "access_token", "quota",
		"used_quota", "request_count", "group", "aff_code", "aff_count", "aff_quota",
		"aff_history", "inviter_id", "deleted_at", "linux_do_id", "setting", "remark", "identity",
		"partner_id", "billing_discount", "stripe_customer", "created_at", "last_login_at",
	}).AddRow(1, mobile, "$2a$10$hash", "138****1234", 1, 1, "", "", "", "", "", "", nil, 0, 0, 0, "default", "", 0, 0, 0, 0, nil, "", "", "", "", 0, 1.0, "", 0, 0)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").WillReturnRows(userRow)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE .*access_token").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `user` SET .*access_token").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	code, resp = do(t, r, "POST", "/user/sms_login", `{"mobile":"`+mobile+`","code":"123456"}`, nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("sms login should succeed: code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["access_token"] == nil || data["access_token"] == "" {
		t.Fatalf("sms login should return access_token: resp=%v", resp)
	}
}
