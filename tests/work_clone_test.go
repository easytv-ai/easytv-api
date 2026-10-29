package tests

import (
	"net/http"
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

func workRowCols() []string {
	return []string{"id", "user_id", "title", "video_url", "status", "published_at", "canvas_id", "show_process", "source_canvas_id", "source_skill_id", "open_source", "clone_count"}
}

// TestCloneWorkRequiresOpenSource 非开源作品不能做同款（T5）。
func TestCloneWorkRequiresOpenSource(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `work` WHERE").WillReturnRows(
		sqlmock.NewRows(workRowCols()).AddRow(7, 2, "星空山巅", "/upload/a.mp4", 1, 1700000000, 3, true, 3, 0, 0, 0),
	)
	mock.ExpectQuery("SELECT id, nickname, username FROM `user` WHERE id IN").WillReturnRows(
		sqlmock.NewRows([]string{"id", "nickname"}).AddRow(2, "作者"),
	)
	code, resp := do(t, r, "POST", "/user/work/7/clone", "", authHeaders())
	if code != http.StatusOK || resp["success"] != false || resp["message"] != "作者未开放做同款" {
		t.Fatalf("clone closed work: code=%d resp=%v", code, resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestCloneWorkCopiesCanvas 开源作品：来源画布复制成我的新项目 + 画布，clone_count +1，返回 canvas_id / project_id。
func TestCloneWorkCopiesCanvas(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `work` WHERE").WillReturnRows(
		sqlmock.NewRows(workRowCols()).AddRow(7, 2, "星空山巅", "/upload/a.mp4", 1, 1700000000, 0, false, 3, 11, 1, 4),
	)
	mock.ExpectQuery("SELECT id, nickname, username FROM `user` WHERE id IN").WillReturnRows(
		sqlmock.NewRows([]string{"id", "nickname"}).AddRow(2, "作者"),
	)
	// 来源画布按作者查（不是当前用户）
	mock.ExpectQuery("SELECT .* FROM `canvas` WHERE \\(id = \\? AND user_id = \\?\\)").WithArgs(3, 2).WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id", "name", "graph", "version"}).
			AddRow(3, 2, 1, "山巅工作流", `{"nodes":[{"id":"n1","data":{"kind":"text","taskId":"t1","assetId":5}}],"edges":[]}`, 1),
	)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `project`").WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `canvas`").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project` SET `updated_at`").WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `work` SET `clone_count`=clone_count \\+ 1").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	// 之后的 work_clone 事件写入不再设期望（失败只记日志，不影响响应）

	code, resp := do(t, r, "POST", "/user/work/7/clone", "", authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("clone: code=%d resp=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if data["canvas_id"].(float64) != 9 || data["project_id"].(float64) != 5 {
		t.Fatalf("clone should return new canvas/project: %v", data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestSanitizeSharedGraphStripsAssetRefs 复制别人的画布：所有资产 ID 引用（含嵌套的 refItems、导演台场景）和主体引用都要清掉，
// 否则一生成就报「输入素材不存在」；媒体地址和提示词保留。
func TestSanitizeSharedGraphStripsAssetRefs(t *testing.T) {
	src := `{"nodes":[{"id":"video-1","selected":true,"data":{"kind":"video","prompt":"雪山","assetId":11,"taskId":3,"taskStatus":"succeeded",
		"refAssetId":12,"refUrl":"https://cdn/r.png","lastAssetId":13,"styleAssetId":14,"markAssetId":15,"voiceAssetId":16,"castAssetId":17,"setAssetId":18,
		"subjectIds":[1,2],"refItems":[{"assetId":19,"url":"https://cdn/i.png"}],
		"scene":{"actors":[{"name":"阿杰","modelAssetId":20,"url":"https://cdn/m.glb"}]}}}],"edges":[]}`
	out := model.SanitizeSharedGraph(src)
	for _, id := range []string{"11", "12", "13", "14", "15", "16", "17", "18", "19", "20", "subjectIds", "taskId", "selected"} {
		if strings.Contains(out, `":`+id) || strings.Contains(out, `"`+id+`"`) {
			t.Fatalf("%s 没清掉: %s", id, out)
		}
	}
	for _, keep := range []string{"雪山", "https://cdn/r.png", "https://cdn/i.png", "https://cdn/m.glb", "阿杰"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("%s 不该被删: %s", keep, out)
		}
	}
}
