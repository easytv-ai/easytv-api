package tests

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestSkillTeamShareQuery 我的技能列表带 team_id 时，SQL 要同时取自己的和团队共享的（F7，API.md 10.10）。
func TestSkillTeamShareQuery(t *testing.T) {
	mock := initMockDB(t)
	userId := 8
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("shared = 1 AND team_id = ?")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, _, err := model.GetSkills(model.SkillFilter{UserId: &userId, TeamId: 3}, &common.PageInfo{})
	if err != nil {
		t.Fatalf("带团队的技能列表查询失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 不含团队共享条件: %v", err)
	}
}

// TestSkillOwnOnlyQuery 不在团队时只查自己的，不该带 shared 条件。
func TestSkillOwnOnlyQuery(t *testing.T) {
	mock := initMockDB(t)
	userId := 8
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("user_id = ?")).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, _, err := model.GetSkills(model.SkillFilter{UserId: &userId}, &common.PageInfo{}); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL 不该变形: %v", err)
	}
}

// TestProjectCommentSorting 列表排序必须是「未解决在前，再按 id 倒序」。
func TestProjectCommentSorting(t *testing.T) {
	mock := initMockDB(t)
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY resolved asc, id desc")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, _, err := model.GetProjectComments(model.ProjectCommentFilter{ProjectId: 1}, &common.PageInfo{})
	if err != nil {
		t.Fatalf("批注列表查询失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("排序不对: %v", err)
	}
}

// TestProjectCommentOnlyOpen only_open=1 时只取未解决。
func TestProjectCommentOnlyOpen(t *testing.T) {
	mock := initMockDB(t)
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("resolved = 0")).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, _, err := model.GetProjectComments(model.ProjectCommentFilter{ProjectId: 1, OnlyOpen: true}, &common.PageInfo{})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("only_open 未生效: %v", err)
	}
}

// TestProjectCommentRoutes 四个批注路由都要注册，且都在登录态后面。
func TestProjectCommentRoutes(t *testing.T) {
	r := initTestRouter()
	want := map[string]string{
		"GET":    "/user/project/:id/comment",
		"POST":   "/user/project/:id/comment",
		"PUT":    "/user/project/comment/:id",
		"DELETE": "/user/project/comment/:id",
	}
	got := map[string]bool{}
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	for method, path := range want {
		if !got[method+" "+path] {
			t.Errorf("路由未注册: %s %s", method, path)
		}
	}
}

// TestProjectCommentEmptyContentRejected 空正文必须被拒（截断后为空也算空）。
func TestProjectCommentEmptyContentRejected(t *testing.T) {
	initMockDB(t)
	r := initTestRouter()
	code, resp := do(t, r, "POST", "/user/project/1/comment", `{"content":"   "}`, nil)
	if code != 200 {
		t.Fatalf("失败也应是 HTTP 200，得到 %d", code)
	}
	if ok, _ := resp["success"].(bool); ok {
		t.Fatal("空正文不该成功")
	}
}

// TestConcurrencyErrorIsTyped 并发满必须是可识别的类型，否则前端只能靠文案猜。
func TestConcurrencyErrorIsTyped(t *testing.T) {
	var err error = &service.ErrConcurrencyFull{Limit: 1, Used: 1}
	var full *service.ErrConcurrencyFull
	if !errors.As(err, &full) {
		t.Fatal("应该能用 errors.As 取出 *ErrConcurrencyFull")
	}
	if full.Limit != 1 {
		t.Fatalf("Limit 丢了: %+v", full)
	}
	if !strings.Contains(err.Error(), "并发已达上限") {
		t.Fatalf("个人版文案不对: %s", err.Error())
	}
	team := &service.ErrConcurrencyFull{Limit: 4, Team: true}
	if !strings.Contains(team.Error(), "团队并发已达上限") {
		t.Fatalf("团队版文案不对: %s", team.Error())
	}
}

// TestModelDurations 时长白名单：空=不限，声明了就只认列出的秒数。
func TestModelDurations(t *testing.T) {
	free := &model.ModelPrice{Model: "x", Durations: ""}
	if free.AllowedDurations() != nil {
		t.Fatal("空 durations 应表示不限")
	}
	for _, sec := range []int{1, 5, 10, 999} {
		if !free.DurationAllowed(sec) {
			t.Fatalf("不限时 %d 秒应放过", sec)
		}
	}
	sora := &model.ModelPrice{Model: "sora-2", Durations: "4,8,12"}
	for _, sec := range []int{4, 8, 12} {
		if !sora.DurationAllowed(sec) {
			t.Fatalf("%d 秒应被允许", sec)
		}
	}
	for _, sec := range []int{5, 10, 15} {
		if sora.DurationAllowed(sec) {
			t.Fatalf("%d 秒不该被允许", sec)
		}
	}
	// duration 缺省（0）不拦：图片等没有时长的任务照常走
	if !sora.DurationAllowed(0) {
		t.Fatal("未指定时长不该拦")
	}
}

// TestNormalizeDurations 管理端输入清洗：去空白、去非法、去重、升序。
func TestNormalizeDurations(t *testing.T) {
	cases := map[string]string{
		" 8, 4 ,12 ": "4,8,12",
		"10,10,5":    "5,10",
		"abc":        "",
		"":           "",
		"0,-3,5":     "5",
		"5,700,12":   "5,12",
	}
	for in, want := range cases {
		if got := model.NormalizeDurations(in); got != want {
			t.Errorf("NormalizeDurations(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCheckModelDuration 提交前拦截的报错要说清支持哪些秒数。
func TestCheckModelDuration(t *testing.T) {
	price := &model.ModelPrice{Model: "sora-2", Unit: model.PriceUnitSecond, Durations: "4,8,12"}
	if err := service.CheckModelDuration(price, `{"duration":8}`); err != nil {
		t.Fatalf("8 秒应通过: %v", err)
	}
	err := service.CheckModelDuration(price, `{"duration":10}`)
	if err == nil {
		t.Fatal("10 秒应被拦下")
	}
	for _, want := range []string{"sora-2", "4 / 8 / 12", "10 秒"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错里应带 %q，实际是 %q", want, err.Error())
		}
	}
	// 没声明时长的模型一律放过
	if err := service.CheckModelDuration(&model.ModelPrice{Model: "y"}, `{"duration":10}`); err != nil {
		t.Fatalf("未声明的模型不该拦: %v", err)
	}
	// 没有 duration 参数（图片任务）也不拦
	if err := service.CheckModelDuration(price, `{"count":1}`); err != nil {
		t.Fatalf("无 duration 参数不该拦: %v", err)
	}
}

// TestModelResolutions 分辨率白名单：空=不限，大小写不敏感。
func TestModelResolutions(t *testing.T) {
	free := &model.ModelPrice{Model: "x"}
	if !free.ResolutionAllowed("720P") || free.AllowedResolutions() != nil {
		t.Fatal("未声明分辨率时应不限")
	}
	hailuo := &model.ModelPrice{Model: "hailuo-02", Resolutions: "512P,768P,1080P"}
	for _, r := range []string{"512P", "768p", " 1080P "} {
		if !hailuo.ResolutionAllowed(r) {
			t.Fatalf("%q 应被允许（大小写与空白不敏感）", r)
		}
	}
	if hailuo.ResolutionAllowed("720P") {
		t.Fatal("720P 不在列表里，不该通过")
	}
	if hailuo.ResolutionAllowed("") != true {
		t.Fatal("没传分辨率不该拦")
	}
	if got := model.NormalizeResolutions(" 768p , 720P ,768P"); got != "768P,720P" {
		t.Fatalf("NormalizeResolutions 结果不对: %q", got)
	}
}

// TestCheckModelParams 时长与分辨率一起校验，报错要指明是哪一项。
func TestCheckModelParams(t *testing.T) {
	price := &model.ModelPrice{Model: "hailuo-02", Unit: model.PriceUnitSecond, Durations: "6,10", Resolutions: "512P,768P,1080P"}
	if err := service.CheckModelParams(price, `{"duration":10,"resolution":"768P"}`); err != nil {
		t.Fatalf("合法组合应通过: %v", err)
	}
	if err := service.CheckModelParams(price, `{"duration":5,"resolution":"768P"}`); err == nil || !strings.Contains(err.Error(), "秒") {
		t.Fatalf("时长不合法应报时长: %v", err)
	}
	err := service.CheckModelParams(price, `{"duration":10,"resolution":"720P"}`)
	if err == nil || !strings.Contains(err.Error(), "512P / 768P / 1080P") {
		t.Fatalf("分辨率不合法应列出支持值: %v", err)
	}
}

// TestTargetVideoSize 输出尺寸推导：显式 size 优先，其次按分辨率短边 + 比例定方向。
func TestTargetVideoSize(t *testing.T) {
	cases := []struct {
		ratio, res, size string
		w, h             int
	}{
		{"16:9", "720P", "", 1280, 720},
		{"16:9", "1080P", "", 1920, 1080},
		{"9:16", "720P", "", 720, 1280},
		{"1:1", "768P", "", 768, 768},
		{"16:9", "720P", "1024x576", 1024, 576}, // 显式 size 优先
		{"", "720P", "", 1280, 720},             // 比例缺省按 16:9
		{"16:9", "", "", 0, 0},                  // 推不出来
		{"16:9", "abc", "", 0, 0},
	}
	for _, c := range cases {
		w, h := service.TargetVideoSize(c.ratio, c.res, c.size)
		if w != c.w || h != c.h {
			t.Errorf("TargetVideoSize(%q,%q,%q) = %dx%d, want %dx%d", c.ratio, c.res, c.size, w, h, c.w, c.h)
		}
	}
}

// TestTargetVideoSizeEven 推导出的边长必须是偶数，编码器要求。
func TestTargetVideoSizeEven(t *testing.T) {
	for _, res := range []string{"480P", "512P", "720P", "768P", "1080P"} {
		for _, ratio := range []string{"16:9", "9:16", "4:3", "21:9", "3:2"} {
			w, h := service.TargetVideoSize(ratio, res, "")
			if w%2 != 0 || h%2 != 0 {
				t.Errorf("%s %s 推出奇数边长 %dx%d", ratio, res, w, h)
			}
		}
	}
}

// TestFitTaskFirstFrameSkips 没开开关 / 推不出尺寸 / 没有首帧素材时，inputs 必须原样返回。
func TestFitTaskFirstFrameSkips(t *testing.T) {
	inputs := `[{"asset_id":1,"role":"first_frame"}]`
	params := `{"ratio":"16:9","resolution":"720P"}`
	// 开关关着
	if got := service.FitTaskFirstFrame(8, &model.ModelPrice{Model: "x"}, inputs, params); got != inputs {
		t.Fatalf("未开开关不该改写: %s", got)
	}
	on := &model.ModelPrice{Model: "sora-2", FrameMatch: 1}
	// 推不出尺寸
	if got := service.FitTaskFirstFrame(8, on, inputs, `{}`); got != inputs {
		t.Fatalf("推不出尺寸不该改写: %s", got)
	}
	// 没有首帧角色
	other := `[{"asset_id":1,"role":"reference"}]`
	if got := service.FitTaskFirstFrame(8, on, other, params); got != other {
		t.Fatalf("非首帧素材不该改写: %s", got)
	}
	// inputs 不是合法 JSON
	if got := service.FitTaskFirstFrame(8, on, "not-json", params); got != "not-json" {
		t.Fatalf("非法 inputs 应原样返回: %s", got)
	}
}

// teammateCanvasExpect 读别人的画布：本人查不到 → 按 id 取到画布（所有者 9、项目 5）→ 项目（team_id=projectTeam）。
// 团队项目再查自己的成员记录（团队 3，权限 perm）；个人项目不再查团队（API.md 20）。
func teammateCanvasExpect(mock sqlmock.Sqlmock, projectTeam int, perm string) {
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `canvas` WHERE \\(id = \\? AND user_id = \\?\\)").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT .* FROM `canvas` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id", "name", "graph", "version", "share_enabled", "share_token"}).
			AddRow(9, 9, 5, "队友的画布", `{"nodes":[],"edges":[]}`, 4, true, "tok12345"))
	mock.ExpectQuery("SELECT .* FROM `project` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "team_id", "name"}).AddRow(5, 9, projectTeam, "项目"))
	if projectTeam > 0 {
		mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(
			sqlmock.NewRows([]string{"id", "team_id", "user_id", "role", "permission"}).AddRow(1, 3, 1, "member", perm))
	}
}

// TestTeammateCanvasReadonly 团队空间项目的画布：查看者只读打开（F09），不带分享 token、不外露邮箱。
func TestTeammateCanvasReadonly(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	teammateCanvasExpect(mock, 3, "viewer")
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "username", "nickname"}).AddRow(9, "owner@example.com", "阿杰"))

	code, resp := do(t, r, "GET", "/user/canvas/9", "", authHeaders())
	data, _ := resp["data"].(map[string]any)
	if code != http.StatusOK || resp["success"] != true || data["readonly"] != true || data["permission"] != "viewer" || data["owner_nickname"] != "阿杰" {
		t.Fatalf("teammate canvas: code=%d resp=%v", code, resp)
	}
	if data["share_token"] != "" {
		t.Fatalf("share_token 不该给队友: %v", data["share_token"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestNonTeammateCanvasNotFound 项目属于别的团队（或已被移出团队）读不到别人的画布。
func TestNonTeammateCanvasNotFound(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	teammateCanvasExpect(mock, 4, "editor")

	_, resp := do(t, r, "GET", "/user/canvas/9", "", authHeaders())
	if resp["success"] != false {
		t.Fatalf("非队友应读不到: %v", resp)
	}
}

// TestTeammatePersonalCanvasNotFound 队友个人空间的项目（team_id=0）即使同团队也读不到（API.md 20）。
func TestTeammatePersonalCanvasNotFound(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	teammateCanvasExpect(mock, 0, "")

	_, resp := do(t, r, "GET", "/user/canvas/9", "", authHeaders())
	if resp["success"] != false {
		t.Fatalf("队友的个人项目不该可见: %v", resp)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestSubjectUpdateBumpsVersion 改描述：先补旧版本快照，再 version+1 并存新快照；只改共享开关不算新版本。
func TestSubjectUpdateBumpsVersion(t *testing.T) {
	mock := initMockDB(t)
	subjectRow := func(version int, look string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "user_id", "kind", "name", "look", "refs", "version"}).AddRow(3, 8, "character", "阿杰", look, "[]", version)
	}
	mock.ExpectQuery("SELECT .* FROM `subject` WHERE \\(id = \\? AND user_id = \\?\\)").WillReturnRows(subjectRow(1, "黑框眼镜"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `subject_version`").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	mock.ExpectExec("INSERT INTO `subject_version`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `subject` SET .*`version`=version \\+ 1").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectQuery("SELECT .* FROM `subject` WHERE id = \\?").WillReturnRows(subjectRow(2, "圆框眼镜"))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `subject_version`").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	mock.ExpectExec("INSERT INTO `subject_version`").WithArgs(3, 2, "character", "阿杰", "圆框眼镜", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()
	if err := model.UpdateSubjectFields(8, 3, map[string]any{"look": "圆框眼镜"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	mock.ExpectQuery("SELECT .* FROM `subject` WHERE \\(id = \\? AND user_id = \\?\\)").WillReturnRows(subjectRow(2, "圆框眼镜"))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `subject` SET `shared`=\\?,`updated_at`=\\?").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectCommit()
	if err := model.UpdateSubjectFields(8, 3, map[string]any{"shared": 1}); err != nil {
		t.Fatalf("update shared: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestTeammateCanvasEditor 编辑者打开队友画布可编辑（readonly=false）。
func TestTeammateCanvasEditor(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	teammateCanvasExpect(mock, 3, "editor")
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "username", "nickname"}).AddRow(9, "owner@example.com", "阿杰"))
	_, resp := do(t, r, "GET", "/user/canvas/9", "", authHeaders())
	data, _ := resp["data"].(map[string]any)
	if resp["success"] != true || data["readonly"] != false || data["permission"] != "editor" || data["team_shared"] != true {
		t.Fatalf("editor canvas: %v", resp)
	}
}

func TestTeamMemberPermission(t *testing.T) {
	cases := []struct {
		m    model.TeamMember
		want string
	}{
		{model.TeamMember{Role: model.TeamRoleOwner, Permission: "viewer"}, "admin"},
		{model.TeamMember{Role: model.TeamRoleMember, Permission: ""}, "editor"},
		{model.TeamMember{Role: model.TeamRoleMember, Permission: "viewer"}, "viewer"},
		{model.TeamMember{Role: model.TeamRoleMember, Permission: "boss"}, "editor"},
	}
	for _, c := range cases {
		if got := c.m.EffectivePermission(); got != c.want {
			t.Fatalf("%+v → %s, want %s", c.m, got, c.want)
		}
	}
}
