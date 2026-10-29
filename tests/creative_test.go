package tests

import (
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// authHeaders 一个合法登录态：sqlmock 返回 id=1 的启用用户。
func authHeaders() map[string]string {
	return map[string]string{
		"Authorization": "Bearer validtoken",
		"EasyTV-User":   "1",
	}
}

// expectAuthUser 注册 access_token 校验查询（每次带鉴权的请求都会触发一次）。
func expectAuthUser(mock sqlmock.Sqlmock) {
	row := sqlmock.NewRows([]string{
		"id", "username", "password", "nickname", "role", "status", "github_id", "discord_id",
		"oidc_id", "wechat_id", "wechat_nickname", "telegram_id", "access_token", "quota",
		"used_quota", "request_count", "group", "aff_code", "aff_count", "aff_quota",
		"aff_history", "inviter_id", "deleted_at", "linux_do_id", "setting", "remark", "identity",
		"partner_id", "billing_discount", "stripe_customer", "created_at", "last_login_at",
	}).AddRow(1, "u@example.com", "$2a$10$hash", "测试用户", 1, 1, "", "", "", "", "", "", "validtoken", 0, 0, 0, "default", "", 0, 0, 0, 0, nil, "", "", "", "", 0, 1.0, "", 0, 0)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE .*access_token").WillReturnRows(row)
}

// TestCreativeRoutesRequireAuth 创作域用户侧路由未登录一律拒绝（契约：success=false）。
func TestCreativeRoutesRequireAuth(t *testing.T) {
	initMockDB(t)
	r := initTestRouter()
	cases := []struct{ method, path string }{
		{"GET", "/user/project"},
		{"POST", "/user/project"},
		{"GET", "/user/canvas"},
		{"POST", "/user/canvas"},
		{"PUT", "/user/canvas/1"},
		{"GET", "/user/asset"},
		{"POST", "/user/asset/upload"},
		{"POST", "/user/task"},
		{"POST", "/user/task/1/retake"},
		{"POST", "/user/task/1/extend"},
		{"POST", "/user/task/1/upscale"},
		{"GET", "/user/media/actions"},
		{"POST", "/user/media/jobs"},
		{"GET", "/user/media/jobs"},
		{"GET", "/user/media/jobs/1"},
		{"GET", "/user/work"},
		{"GET", "/user/qiniu/upload-token"},
		{"GET", "/user/qiniu/key"},
		{"GET", "/admin/project"},
		{"DELETE", "/admin/user/2"},
		{"POST", "/user/work/1/like"},
		{"POST", "/user/work/1/clone"},
	}
	for _, tc := range cases {
		code, resp := do(t, r, tc.method, tc.path, `{}`, nil)
		if code != http.StatusOK || resp["success"] != false {
			t.Fatalf("%s %s should require auth: code=%d resp=%v", tc.method, tc.path, code, resp)
		}
	}
}

// TestGenerationModelsEmpty 未注册任何厂商时模型列表为空数组（接口存在、契约正确）。
func TestGenerationModelsEmpty(t *testing.T) {
	initMockDB(t)
	r := initTestRouter()
	code, resp := do(t, r, "GET", "/generation/models", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("models: code=%d resp=%v", code, resp)
	}
	if list, ok := resp["data"].([]any); !ok || len(list) != 0 {
		t.Fatalf("models should be empty list: %v", resp["data"])
	}
}

// TestPublicWorksFeed 公开信息流只查已发布作品，并附带作者。
func TestPublicWorksFeed(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `work` WHERE status = \\?").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT .* FROM `work` WHERE status = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "title", "video_url", "status", "published_at"}).AddRow(7, 1, "星空山巅", "/upload/a.mp4", 1, 1700000000),
	)
	mock.ExpectQuery("SELECT id, nickname, username FROM `user` WHERE id IN").WillReturnRows(
		sqlmock.NewRows([]string{"id", "nickname"}).AddRow(1, "测试用户"),
	)

	code, resp := do(t, r, "GET", "/work?page=1&page_size=10", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("feed: code=%d resp=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 || data["total"].(float64) != 1 {
		t.Fatalf("feed should return 1 item: %v", data)
	}
	author := items[0].(map[string]any)["author"].(map[string]any)
	if author["nickname"] != "测试用户" {
		t.Fatalf("author should be attached: %v", items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestCanvasCreateAutoProject 新建画布不传 project_id → 自动建同名项目并返回 graph。
func TestCanvasCreateAutoProject(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `project`").WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `canvas`").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project` SET `updated_at`").WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectCommit()

	body := `{"name":"我的第一个草稿","graph":{"nodes":[{"id":"text-1"}],"edges":[],"viewport":{"x":0,"y":0,"zoom":1}}}`
	code, resp := do(t, r, "POST", "/user/canvas", body, authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("create canvas: code=%d resp=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if data["id"].(float64) != 9 || data["project_id"].(float64) != 5 || data["version"].(float64) != 1 {
		t.Fatalf("unexpected canvas view: %v", data)
	}
	graph := data["graph"].(map[string]any)
	if nodes := graph["nodes"].([]any); len(nodes) != 1 {
		t.Fatalf("graph should round-trip: %v", graph)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestCanvasSaveVersionConflict 乐观锁：version 不匹配时返回冲突提示。
func TestCanvasSaveVersionConflict(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	// 先读画布
	mock.ExpectQuery("SELECT .* FROM `canvas` WHERE \\(id = \\? AND user_id = \\?\\)").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id", "name", "graph", "version"}).AddRow(9, 1, 5, "草稿", "{}", 3),
	)
	// 带 version=2 更新 → 0 行
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `canvas` SET").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	// 区分不存在 vs 冲突：count = 1 → 冲突
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `canvas` WHERE \\(id = \\? AND user_id = \\?\\)").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	body := `{"version":2,"graph":{"nodes":[],"edges":[]}}`
	code, resp := do(t, r, "PUT", "/user/canvas/9", body, authHeaders())
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("stale version should fail: code=%d resp=%v", code, resp)
	}
	if msg, _ := resp["message"].(string); msg == "" || msg == "保存失败" {
		t.Fatalf("should return conflict message, got %q", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestCreateTaskUnknownModel 未接入的模型直接拒绝，不落库。
func TestCreateTaskUnknownModel(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	body := `{"type":"video","model":"not-a-model","prompt":"一只猫"}`
	code, resp := do(t, r, "POST", "/user/task", body, authHeaders())
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("unknown model should fail: code=%d resp=%v", code, resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("task creation must not touch DB: %v", err)
	}
}

// expectAuthAdmin 注册一个 role=10 的普通管理员登录态。
func expectAuthAdmin(mock sqlmock.Sqlmock) {
	row := sqlmock.NewRows([]string{
		"id", "username", "password", "nickname", "role", "status", "github_id", "discord_id",
		"oidc_id", "wechat_id", "wechat_nickname", "telegram_id", "access_token", "quota",
		"used_quota", "request_count", "group", "aff_code", "aff_count", "aff_quota",
		"aff_history", "inviter_id", "deleted_at", "linux_do_id", "setting", "remark", "identity",
		"partner_id", "billing_discount", "stripe_customer", "created_at", "last_login_at",
	}).AddRow(2, "ops@example.com", "$2a$10$hash", "运营", 10, 1, "", "", "", "", "", "", "admintoken", 0, 0, 0, "default", "", 0, 0, 0, 0, nil, "", "", "", "", 0, 1.0, "", 0, 0)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE .*access_token").WillReturnRows(row)
}

// TestRootOnlyRoutes 普通管理员（role 10）不能碰资金、设置与定价写操作。
func TestRootOnlyRoutes(t *testing.T) {
	headers := map[string]string{"Authorization": "Bearer admintoken", "EasyTV-User": "2"}
	for _, tc := range []struct{ method, path string }{
		{"PUT", "/admin/setting/SystemName"},
		{"DELETE", "/admin/user/9"},
		{"PUT", "/admin/user/9/bind"},
		{"POST", "/admin/withdraw/1/paid"},
		{"POST", "/admin/order/1/refund"},
		{"POST", "/admin/order/1/mark-paid"},
		{"POST", "/admin/wallet/9/adjust"},
		{"PUT", "/admin/billing/promo"},
		{"POST", "/admin/model-price"},
		{"DELETE", "/admin/plan/1"},
		{"PUT", "/admin/gateway/config"},
		{"GET", "/admin/gateway/fetch-models"},
		{"POST", "/admin/gateway/models/import"},
	} {
		mock := initMockDB(t)
		r := initTestRouter()
		expectAuthAdmin(mock)
		code, resp := do(t, r, tc.method, tc.path, `{"delta":1,"status":1}`, headers)
		if code != http.StatusOK || resp["success"] != false || resp["message"] != "权限不足" {
			t.Fatalf("%s %s should be root-only, got %v", tc.method, tc.path, resp)
		}
	}
	// 普通管理员仍可读列表
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthAdmin(mock)
	mock.ExpectQuery("SELECT .* FROM `membership_plan`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	code, resp := do(t, r, "GET", "/admin/plan", "", headers)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("admin should read plans: %v", resp)
	}
}

// TestPublicCategories 公开分类只返回启用项，且默认 scope=work。
func TestPublicCategories(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	mock.ExpectQuery("SELECT .* FROM `category` WHERE scope = \\? AND status = \\?").WithArgs("work", 1).WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "scope", "sort_order", "status"}).AddRow(1, "电影美学", "work", 1, 1),
	)
	code, resp := do(t, r, "GET", "/category", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("category: code=%d resp=%v", code, resp)
	}
	items := resp["data"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "电影美学" {
		t.Fatalf("unexpected categories: %v", items)
	}
	code, resp = do(t, r, "GET", "/category?scope=bad", "", nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("invalid scope should fail: %v", resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestReportWorkValidation 举报必须带原因；缺原因不应触库。
func TestReportWorkValidation(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	code, resp := do(t, r, "POST", "/user/work/7/report", `{"content":"x"}`, authHeaders())
	if code != http.StatusOK || resp["success"] != false || resp["message"] != "请选择举报原因" {
		t.Fatalf("report without reason: code=%d resp=%v", code, resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestCommentRoutes 评论：公开列表可匿名访问；发表需要登录；空内容不触库。
func TestCommentRoutes(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	code, resp := do(t, r, "POST", "/user/work/1/comment", `{"content":"好看"}`, nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("comment should require auth: %v", resp)
	}
	expectAuthUser(mock)
	code, resp = do(t, r, "POST", "/user/work/1/comment", `{"content":"   "}`, authHeaders())
	if code != http.StatusOK || resp["success"] != false || resp["message"] != "评论内容不能为空" {
		t.Fatalf("empty comment: %v", resp)
	}
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `work_comment` WHERE \\(work_id = \\? AND parent_id = 0 AND status = \\?\\)").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT .* FROM `work_comment` WHERE \\(work_id = \\? AND parent_id = 0 AND status = \\?\\)").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	code, resp = do(t, r, "GET", "/work/1/comments", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("public comments: %v", resp)
	}
	data := resp["data"].(map[string]any)
	if data["total"].(float64) != 0 || len(data["items"].([]any)) != 0 {
		t.Fatalf("empty comments expected: %v", data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestGetMyWorksKeyword 我的作品列表把 keyword 传给标题模糊搜（活动投稿选择器依赖这条）。
func TestGetMyWorksKeyword(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `work` WHERE user_id = \\? AND status = \\? AND title LIKE \\?").
		WithArgs(1, 1, "%星空%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT .* FROM `work` WHERE user_id = \\? AND status = \\? AND title LIKE \\?").
		WithArgs(1, 1, "%星空%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "title", "status"}).AddRow(7, 1, "星空山巅", 1))
	mock.ExpectQuery("SELECT id, nickname, username FROM `user` WHERE id IN").WillReturnRows(
		sqlmock.NewRows([]string{"id", "nickname"}).AddRow(1, "测试用户"),
	)

	code, resp := do(t, r, "GET", "/user/work?status=1&keyword=星空&page=1&page_size=24", "", authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("mine keyword: code=%d resp=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 || data["total"].(float64) != 1 {
		t.Fatalf("mine keyword should return 1 item: %v", data)
	}
	if items[0].(map[string]any)["title"] != "星空山巅" {
		t.Fatalf("unexpected work: %v", items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
