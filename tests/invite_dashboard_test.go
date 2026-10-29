package tests

import (
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestInviteRecordsRequireAuth(t *testing.T) {
	initMockDB(t)
	r := initTestRouter()
	code, resp := do(t, r, "GET", "/user/invite/records", "", nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("records should require auth: %v", resp)
	}
}

func TestInviteRecordsList(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `user`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT id, nickname, created_at FROM `user`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "nickname", "created_at"}).AddRow(9, "小明", int64(1700000000)),
	)
	mock.ExpectQuery("SELECT from_user_id, biz_type").WillReturnRows(
		sqlmock.NewRows([]string{"from_user_id", "biz_type", "total"}).AddRow(9, 1, 20),
	)

	code, resp := do(t, r, "GET", "/user/invite/records?page=1&page_size=20", "", authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("records: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items: %v", data)
	}
	row, _ := items[0].(map[string]any)
	if row["status"] != "effective" || row["points"] != float64(20) {
		t.Fatalf("row: %v", row)
	}
}

func TestMyBillsInviteTypes(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `promoter_bill`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT .* FROM `promoter_bill`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "biz_type", "amount", "from_user_id", "source_no", "source_amount", "rate", "status", "created_at", "remark"}).
			AddRow(3, 1, 2, 50, 9, "ORD1", 100, 0.1, 1, int64(1700000000), ""),
	)
	mock.ExpectQuery("SELECT id, nickname, username FROM `user`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "nickname", "username"}).AddRow(9, "小明", "m@x.com"),
	)

	code, resp := do(t, r, "GET", "/user/bills/self?biz_type=1,2,6", "", authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("bills: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items: %v", data)
	}
	row, _ := items[0].(map[string]any)
	if row["from_nickname"] != "小*" || row["biz_type"] != float64(2) {
		t.Fatalf("row: %v", row)
	}
}
