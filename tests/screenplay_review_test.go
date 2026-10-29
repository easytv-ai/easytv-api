package tests

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"

	"github.com/DATA-DOG/go-sqlmock"
)

// 短剧复审（2026-09-15）D02 / D03 的接口级回归（API.md 24.1）。

const reviewPlanRaw = `{"title":"真相","style":"电影感","aspect":"16:9",
"characters":[{"key":"c1","name":"男主","look":"黑衣"}],
"scenes":[{"key":"s1","name":"客厅","look":"暖光"}],
"shots":[{"no":1,"scene":"s1","characters":["c1"],"prompt":"男主看着照片","motion":"推近","line":"我早就知道了","seconds":5},
{"no":2,"scene":"s1","characters":["c1"],"prompt":"男主放下照片","motion":"","line":"","seconds":5}]}`

func screenplayRow(revision, built int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "project_id", "canvas_id", "input", "target_seconds", "plan", "status", "revision", "built_revision"}).
		AddRow(7, 1, 5, 29, "一句话", 30, reviewPlanRaw, model.ScreenplayStatusBuilt, revision, built)
}

// TestScreenplayUpdateRejectsOverLimit D03：13 镜的 PUT 返回明确错误（上限与实际数量），不落库。
func TestScreenplayUpdateRejectsOverLimit(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `screenplay`").WillReturnRows(screenplayRow(1, 1))

	var plan model.Plan
	_ = json.Unmarshal([]byte(reviewPlanRaw), &plan)
	for len(plan.Shots) < 13 {
		plan.Shots = append(plan.Shots, model.PlanShot{Scene: "s1", Characters: []string{"c1"}, Prompt: "加镜", Seconds: 5})
	}
	body, _ := json.Marshal(map[string]any{"plan": plan})
	code, resp := do(t, r, "PUT", "/user/screenplay/7", string(body), authHeaders())
	msg, _ := resp["message"].(string)
	if code != http.StatusOK || resp["success"] != false || !strings.Contains(msg, "镜头最多 12 个") || !strings.Contains(msg, "当前 13 个") {
		t.Fatalf("code=%d resp=%v", code, resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("超限不应写库: %v", err)
	}
}

// TestScreenplayUpdateBumpsRevision D02：保存 plan 后 revision +1，已建画布 → stale=true。
func TestScreenplayUpdateBumpsRevision(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `screenplay`").WillReturnRows(screenplayRow(1, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `screenplay` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	code, resp := do(t, r, "PUT", "/user/screenplay/7", `{"plan":`+reviewPlanRaw+`}`, authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("code=%d resp=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if data["revision"].(float64) != 2 || data["built_revision"].(float64) != 1 || data["stale"] != true {
		t.Fatalf("data = %v", data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestScreenplayBuildSync D02：已建画布 + sync=true → 按镜号同步到既有画布，built_revision 推到 revision；不带 sync 仍返回 existing 与 stale。
func TestScreenplayBuildSync(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()

	// 不带 sync：直接返回已有画布，并带上 stale
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `screenplay`").WillReturnRows(screenplayRow(2, 1))
	mock.ExpectQuery("SELECT .* FROM `canvas`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "project_id", "name", "graph", "version"}).AddRow(29, 1, 5, "真相", "{}", 3))
	code, resp := do(t, r, "POST", "/user/screenplay/7/build", `{}`, authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("code=%d resp=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if data["existing"] != true || data["stale"] != true || data["revision"].(float64) != 2 {
		t.Fatalf("data = %v", data)
	}

	// 带 sync
	var plan model.Plan
	_ = json.Unmarshal([]byte(reviewPlanRaw), &plan)
	_ = plan.Normalize(30)
	graph := service.BuildPlanGraph(&plan, service.BuildModels{Image: "img", Video: "vid", Audio: "aud"})
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `screenplay`").WillReturnRows(screenplayRow(2, 1))
	mock.ExpectQuery("SELECT .* FROM `canvas`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "project_id", "name", "graph", "version"}).AddRow(29, 1, 5, "真相", graph, 3))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `canvas` SET").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 29, 1, 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `canvas_version`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT `id` FROM `canvas_version`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `screenplay` SET").WithArgs(2, sqlmock.AnyArg(), 7).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	code, resp = do(t, r, "POST", "/user/screenplay/7/build", `{"sync":true}`, authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("sync: code=%d resp=%v", code, resp)
	}
	data = resp["data"].(map[string]any)
	synced := data["synced_shots"].([]any)
	if data["canvas_id"].(float64) != 29 || data["project_id"].(float64) != 5 || len(synced) != 2 ||
		len(data["added_shots"].([]any)) != 0 || len(data["removed_shots"].([]any)) != 0 {
		t.Fatalf("data = %v", data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
