package tests

import (
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectAuthRoot(mock sqlmock.Sqlmock) {
	row := sqlmock.NewRows([]string{
		"id", "username", "password", "nickname", "role", "status", "github_id", "discord_id",
		"oidc_id", "wechat_id", "wechat_nickname", "telegram_id", "access_token", "quota",
		"used_quota", "request_count", "group", "aff_code", "aff_count", "aff_quota",
		"aff_history", "inviter_id", "deleted_at", "linux_do_id", "setting", "remark", "identity",
		"partner_id", "billing_discount", "stripe_customer", "created_at", "last_login_at",
	}).AddRow(1, "root@example.com", "$2a$10$hash", "超管", 100, 1, "", "", "", "", "", "", "roottoken", 0, 0, 0, "default", "", 0, 0, 0, 0, nil, "", "", "", "", 0, 1.0, "", 0, 0)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE .*access_token").WillReturnRows(row)
}

func rootHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer roottoken", "EasyTV-User": "1"}
}

func adminHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer admintoken", "EasyTV-User": "2"}
}

func userRow(id int, username, mobile, wechatID, identity string, role int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "username", "role", "status", "mobile", "wechat_id", "wechat_nickname", "identity"}).
		AddRow(id, username, role, 1, mobile, wechatID, "", identity)
}

func TestAdminUpdateUserIdentity(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthAdmin(mock)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").WillReturnRows(userRow(9, "u@example.com", "", "", "", 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `user` SET .*identity").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	code, resp := do(t, r, "PUT", "/admin/user/9/identity", `{"identity":"easytv_official"}`, adminHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("set identity: code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["identity"] != "easytv_official" {
		t.Fatalf("identity: %v", data)
	}
	tags, _ := data["tags"].([]any)
	if len(tags) != 1 || tags[0] != "easytv_official" {
		t.Fatalf("tags: %v", tags)
	}
}

func TestAdminUpdateUserIdentityInvalid(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthAdmin(mock)
	code, resp := do(t, r, "PUT", "/admin/user/9/identity", `{"identity":"hacker"}`, adminHeaders())
	if code != http.StatusOK || resp["success"] != false || resp["message"] != "无效的身份" {
		t.Fatalf("invalid identity: %v", resp)
	}
}

func TestAdminUpdateUserIdentityCannotEditAdmin(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthAdmin(mock)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").WillReturnRows(userRow(3, "ops2@example.com", "", "", "", 10))
	code, resp := do(t, r, "PUT", "/admin/user/3/identity", `{"identity":"enterprise"}`, adminHeaders())
	if code != http.StatusOK || resp["success"] != false || resp["message"] != "无权限修改管理员账号" {
		t.Fatalf("admin cannot edit admin: %v", resp)
	}
}

func TestAdminUpdateUserIdentityClear(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthAdmin(mock)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").WillReturnRows(userRow(9, "u@example.com", "", "", "enterprise", 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `user` SET .*identity").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	code, resp := do(t, r, "PUT", "/admin/user/9/identity", `{"identity":"__none__"}`, adminHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("clear identity: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["identity"] != "" {
		t.Fatalf("cleared identity should be empty: %v", data)
	}
}

func TestAdminRebindUserMobile(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthRoot(mock)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").WillReturnRows(userRow(9, "wx_abc", "", "union-1", "", 1))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `user`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `user` SET").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	code, resp := do(t, r, "PUT", "/admin/user/9/bind", `{"mobile":"13800138000"}`, rootHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("rebind mobile: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["mobile"] != "13800138000" || data["username"] != "13800138000" {
		t.Fatalf("wx_ username should sync: %v", data)
	}
}

func TestAdminRebindUserKeepLogin(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthRoot(mock)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").WillReturnRows(userRow(9, "wx_abc", "", "union-1", "", 1))
	code, resp := do(t, r, "PUT", "/admin/user/9/bind", `{"wechat_unbind":true}`, rootHeaders())
	if code != http.StatusOK || resp["success"] != false || resp["message"] != "至少保留一种登录方式" {
		t.Fatalf("unbind last login: %v", resp)
	}
}

func TestAdminRebindUserWeChat(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthRoot(mock)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").WillReturnRows(userRow(9, "u@example.com", "13800138000", "union-1", "", 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `user` SET").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	code, resp := do(t, r, "PUT", "/admin/user/9/bind", `{"wechat_unbind":true}`, rootHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("unbind wechat: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["wechat_id"] != "" || data["mobile"] != "13800138000" {
		t.Fatalf("wechat should clear, mobile stay: %v", data)
	}
}
