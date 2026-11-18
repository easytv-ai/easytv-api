package tests

import (
	"net/http"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestRecordWorkFollowSkipsUnpublished 不是这位作者的已发布作品，不记关注来源，也不插入。
func TestRecordWorkFollowSkipsUnpublished(t *testing.T) {
	mock := initMockDB(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `work`").
		WithArgs(7, 2, model.WorkStatusPublished).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if err := model.RecordWorkFollow(1, 2, 7); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestFollowFromWatchRecordsWork 播放页关注带上作品号：确认是作者的已发布作品后记一笔。
func TestFollowFromWatchRecordsWork(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "nickname"}).AddRow(2, "author@example.com", "作者"))
	mock.ExpectQuery("SELECT .* FROM `user_follow`").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user_follow`").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `user_follow`").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `work`").
		WithArgs(7, 2, model.WorkStatusPublished).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `work_follow`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	code, resp := do(t, r, "POST", "/user/author/2/follow", `{"work_id":7}`, authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("follow from watch: code=%d resp=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if data["following"] != true {
		t.Fatalf("should be following: %v", data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
