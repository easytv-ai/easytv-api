package tests

import (
	"net/http"
	"regexp"
	"testing"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

// ===== 个人空间 / 团队空间（API.md 20）=====

func teamHeaders(teamId string) map[string]string {
	h := authHeaders()
	h["EasyTV-Team"] = teamId
	return h
}

// TestTeamSpaceForbidden 头里的团队不是自己所在团队：success=false + data.reason=team_forbidden，不进业务逻辑。
func TestTeamSpaceForbidden(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "team_id", "user_id", "role"}).AddRow(1, 3, 1, model.TeamRoleMember))

	code, resp := do(t, r, "GET", "/user/project", "", teamHeaders("4"))
	data, _ := resp["data"].(map[string]any)
	if code != http.StatusOK || resp["success"] != false || data["reason"] != "team_forbidden" || resp["message"] != "你已不在该团队，请切回个人空间" {
		t.Fatalf("应返回 team_forbidden: code=%d resp=%v", code, resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestTeamSpaceNotInTeam 已被移出团队（查不到成员记录）同样 team_forbidden。
func TestTeamSpaceNotInTeam(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, resp := do(t, r, "GET", "/user/asset", "", teamHeaders("3"))
	data, _ := resp["data"].(map[string]any)
	if resp["success"] != false || data["reason"] != "team_forbidden" {
		t.Fatalf("应返回 team_forbidden: %v", resp)
	}
}

// TestTeamMineExemptFromHeader 切换器接口不看空间头：带着失效的团队头也能拿到（空）列表。
func TestTeamMineExemptFromHeader(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, resp := do(t, r, "GET", "/user/team/mine", "", teamHeaders("4"))
	data, _ := resp["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if resp["success"] != true || items == nil || len(items) != 0 {
		t.Fatalf("不在团队应返回空 items: %v", resp)
	}
}

// TestTeamSpaceRoutes 新增路由已注册。
func TestTeamSpaceRoutes(t *testing.T) {
	r := initTestRouter()
	got := map[string]bool{}
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	for _, want := range []string{"GET /user/team/mine", "POST /user/project/:id/move"} {
		if !got[want] {
			t.Errorf("路由未注册: %s", want)
		}
	}
}

// expectScopedQuery 列表先 count 再取数据，两条 SQL 都要带上空间条件。
func expectScopedQuery(mock sqlmock.Sqlmock, cond string) {
	mock.ExpectQuery("SELECT count.*" + regexp.QuoteMeta(cond)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT .*" + regexp.QuoteMeta(cond)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

// TestSpaceListScoping 各列表的空间条件：个人空间只取 team_id=0 的自己的数据，团队空间按 team_id 取全队的。
func TestSpaceListScoping(t *testing.T) {
	page := &common.PageInfo{}
	cases := []struct {
		name string
		cond string
		run  func() error
	}{
		{"项目·个人", "user_id = ? AND team_id = 0", func() error { _, _, err := model.GetUserProjects(1, "", page); return err }},
		{"项目·团队", "team_id = ?", func() error { _, _, err := model.GetTeamProjects(3, "", page); return err }},
		{"资产·个人", "user_id = ? AND team_id = 0", func() error {
			_, _, err := model.GetAssets(model.AssetFilter{UserId: 1, Space: true}, page)
			return err
		}},
		{"资产·团队", "WHERE team_id = ?", func() error {
			_, _, err := model.GetAssets(model.AssetFilter{UserId: 1, Space: true, TeamId: 3}, page)
			return err
		}},
		{"任务·团队", "user_id = ? AND team_id = ?", func() error {
			_, _, err := model.GetTasks(model.TaskFilter{UserId: 1, Space: true, TeamId: 3}, page)
			return err
		}},
		{"主体·个人", "user_id = ? AND team_id = 0", func() error {
			_, _, err := model.GetSubjects(model.SubjectFilter{UserId: 1}, page)
			return err
		}},
		{"主体·团队", "WHERE team_id = ?", func() error {
			_, _, err := model.GetSubjects(model.SubjectFilter{UserId: 1, TeamId: 3}, page)
			return err
		}},
		{"积分流水·团队任务", "biz_type = ? AND biz_no IN (SELECT `task_no` FROM `generation_task` WHERE team_id = ?)", func() error {
			_, _, err := model.GetPointLogs(model.PointLogFilter{UserId: 9, TeamTasks: 3}, page)
			return err
		}},
	}
	for _, c := range cases {
		mock := initMockDB(t)
		expectScopedQuery(mock, c.cond)
		if err := c.run(); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("%s: SQL 缺少空间条件 %q: %v", c.name, c.cond, err)
		}
	}
}

// TestMoveProjectRejectsSameSpace 项目已在个人空间时移回个人空间直接拒绝，不写库。
func TestMoveProjectRejectsSameSpace(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `project` WHERE \\(id = \\? AND user_id = \\?\\)").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "team_id", "name"}).AddRow(5, 1, 0, "我的项目"))
	mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, resp := do(t, r, "POST", "/user/project/5/move", `{"team_id":0}`, authHeaders())
	if resp["success"] != false || resp["message"] != "项目已经在这个空间里了" {
		t.Fatalf("应拒绝: %v", resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestMoveProjectToTeamSQL 换空间在一个事务里改项目与作者在项目里的资产（上传到项目的 + 项目画布上生成的）。
func TestMoveProjectToTeamSQL(t *testing.T) {
	mock := initMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `project` SET `team_id`=?,`updated_at`=? WHERE (id = ? AND user_id = ?)")).
		WithArgs(3, sqlmock.AnyArg(), 5, 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `asset` SET `team_id`=? WHERE user_id = ? AND (project_id = ? OR id IN (SELECT `result_asset_id` FROM `generation_task` WHERE canvas_id IN (SELECT `id` FROM `canvas` WHERE project_id = ?) AND result_asset_id > 0))")).
		WithArgs(3, 1, 5, 5).WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectCommit()
	if err := model.MoveProjectToTeam(1, 5, 3); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
