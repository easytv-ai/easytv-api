package service

import (
	"errors"
	"testing"
	"time"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

// ===== 个人空间 / 团队空间（API.md 20）=====

// TestDecideTeamPayer 买单矩阵：个人空间永远自己付（成员、owner 都一样）；团队空间 owner 付，会员过期 / 不是成员报错。
func TestDecideTeamPayer(t *testing.T) {
	team := &model.Team{Id: 3, OwnerId: 9}
	member := &model.TeamMember{TeamId: 3, UserId: 8, Role: model.TeamRoleMember, Permission: model.TeamPermEditor}
	owner := &model.TeamMember{TeamId: 3, UserId: 9, Role: model.TeamRoleOwner}
	other := &model.TeamMember{TeamId: 4, UserId: 8, Role: model.TeamRoleMember}
	cases := []struct {
		name    string
		userId  int
		teamId  int
		member  *model.TeamMember
		team    *model.Team
		active  bool
		want    int
		wantErr error
	}{
		{"个人空间·团队成员", 8, 0, member, team, true, 8, nil},
		{"个人空间·团队 owner", 9, 0, owner, team, true, 9, nil},
		{"个人空间·不在团队", 8, 0, nil, nil, false, 8, nil},
		{"团队空间·会员有效·成员", 8, 3, member, team, true, 9, nil},
		{"团队空间·会员有效·owner", 9, 3, owner, team, true, 9, nil},
		{"团队空间·会员过期", 8, 3, member, team, false, 0, ErrTeamEditionExpired},
		{"团队空间·不是该团队成员", 8, 3, other, nil, true, 0, model.ErrNotTeamMember},
		{"团队空间·不在任何团队", 8, 3, nil, nil, true, 0, model.ErrNotTeamMember},
	}
	for _, c := range cases {
		got, err := decideTeamPayer(c.userId, c.teamId, c.member, c.team, c.active)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, got, err, c.want, c.wantErr)
		}
	}
}

// TestCheckTeamPoolPermission 团队空间里查看者不能用团队积分；owner 恒为管理员；个人空间不限制。
func TestCheckTeamPoolPermission(t *testing.T) {
	viewer := &model.TeamMember{TeamId: 3, Role: model.TeamRoleMember, Permission: model.TeamPermViewer}
	editor := &model.TeamMember{TeamId: 3, Role: model.TeamRoleMember, Permission: model.TeamPermEditor}
	owner := &model.TeamMember{TeamId: 3, Role: model.TeamRoleOwner, Permission: model.TeamPermViewer}
	if err := checkTeamPoolPermission(0, viewer); err != nil {
		t.Fatalf("个人空间的查看者应能生成（花自己的钱）: %v", err)
	}
	if err := checkTeamPoolPermission(3, viewer); !errors.Is(err, ErrTeamViewer) {
		t.Fatalf("团队空间查看者应被拒: %v", err)
	}
	if err := checkTeamPoolPermission(3, editor); err != nil {
		t.Fatalf("编辑者应放行: %v", err)
	}
	if err := checkTeamPoolPermission(3, owner); err != nil {
		t.Fatalf("owner 应放行: %v", err)
	}
	if err := checkTeamPoolPermission(4, editor); !errors.Is(err, model.ErrNotTeamMember) {
		t.Fatalf("别的团队应报不在团队: %v", err)
	}
}

// TestDecideTaskTeam 任务空间：挂项目跟项目走（个人项目在团队空间也算个人），没有项目用请求空间。
func TestDecideTaskTeam(t *testing.T) {
	member := &model.TeamMember{TeamId: 3}
	cases := []struct {
		name      string
		workspace int
		project   *model.Project
		member    *model.TeamMember
		want      int
		wantErr   error
	}{
		{"无项目·个人空间", 0, nil, member, 0, nil},
		{"无项目·团队空间", 3, nil, member, 3, nil},
		{"个人项目·团队空间请求", 3, &model.Project{Id: 1}, member, 0, nil},
		{"团队项目·个人空间请求", 0, &model.Project{Id: 1, TeamId: 3}, member, 3, nil},
		{"团队项目·已不在团队", 0, &model.Project{Id: 1, TeamId: 3}, nil, 0, model.ErrNotFound},
		{"团队项目·别的团队", 3, &model.Project{Id: 1, TeamId: 5}, member, 0, model.ErrNotFound},
	}
	for _, c := range cases {
		got, err := decideTaskTeam(c.workspace, c.project, c.member)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, got, err, c.want, c.wantErr)
		}
	}
}

// TestProjectAccessFlags 项目权限标记：作者全权；团队项目按角色；队友的个人项目无权限。
func TestProjectAccessFlags(t *testing.T) {
	type flags struct {
		perm                     string
		canEdit, canDelete, move bool
	}
	cases := []struct {
		name     string
		viewer   int
		p        *model.Project
		teamPerm string
		want     flags
	}{
		{"作者·个人项目", 8, &model.Project{UserId: 8}, "", flags{"owner", true, true, true}},
		{"作者·团队项目（即使是查看者）", 8, &model.Project{UserId: 8, TeamId: 3}, "viewer", flags{"owner", true, true, true}},
		{"管理员·团队项目", 9, &model.Project{UserId: 8, TeamId: 3}, "admin", flags{"admin", true, true, false}},
		{"编辑者·团队项目", 9, &model.Project{UserId: 8, TeamId: 3}, "editor", flags{"editor", true, false, false}},
		{"查看者·团队项目", 9, &model.Project{UserId: 8, TeamId: 3}, "viewer", flags{"viewer", false, false, false}},
		{"队友·个人项目", 9, &model.Project{UserId: 8}, "admin", flags{"", false, false, false}},
	}
	for _, c := range cases {
		perm, e, d, m := ProjectAccess(c.viewer, c.p, c.teamPerm)
		if got := (flags{perm, e, d, m}); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
	// FillProjectAccess 只在成员团队与项目团队一致时给团队权限
	p := &model.Project{UserId: 8, TeamId: 3}
	FillProjectAccess(9, &model.TeamMember{TeamId: 4, Role: model.TeamRoleOwner}, p)
	if p.CanEdit || p.Permission != "" || !p.TeamShared || p.OwnerId != 8 {
		t.Fatalf("别的团队的管理员不该有权限: %+v", p)
	}
}

// TestCheckProjectMove 换空间校验。
func TestCheckProjectMove(t *testing.T) {
	editor := &model.TeamMember{TeamId: 3, Role: model.TeamRoleMember, Permission: model.TeamPermEditor}
	viewer := &model.TeamMember{TeamId: 3, Role: model.TeamRoleMember, Permission: model.TeamPermViewer}
	if err := CheckProjectMove(&model.Project{TeamId: 0}, editor, 3); err != nil {
		t.Fatalf("编辑者可以移入团队: %v", err)
	}
	if err := CheckProjectMove(&model.Project{TeamId: 3}, editor, 3); !errors.Is(err, ErrProjectSameSpace) {
		t.Fatalf("已在团队应拒绝: %v", err)
	}
	if err := CheckProjectMove(&model.Project{TeamId: 0}, editor, 0); !errors.Is(err, ErrProjectSameSpace) {
		t.Fatalf("已在个人空间应拒绝: %v", err)
	}
	if err := CheckProjectMove(&model.Project{TeamId: 0}, viewer, 3); err == nil {
		t.Fatal("查看者不能移入团队")
	}
	if err := CheckProjectMove(&model.Project{TeamId: 0}, editor, 4); !errors.Is(err, model.ErrNotTeamMember) {
		t.Fatalf("不是该团队成员应拒绝: %v", err)
	}
	if err := CheckProjectMove(&model.Project{TeamId: 3}, nil, 0); err != nil {
		t.Fatalf("已退团的作者也能移回个人空间: %v", err)
	}
}

func TestAssetInTeam(t *testing.T) {
	if assetInTeam(&model.Asset{TeamId: 0}, 0) {
		t.Fatal("个人资产不属于任何团队空间")
	}
	if !assetInTeam(&model.Asset{TeamId: 3}, 3) || assetInTeam(&model.Asset{TeamId: 3}, 4) {
		t.Fatal("团队资产只属于自己的团队")
	}
}

// TestPayerForTeamPersonalNoQuery 个人空间不查任何团队数据。
func TestPayerForTeamPersonalNoQuery(t *testing.T) {
	mock := mockDB(t)
	payer, err := PayerForTeam(8, 0)
	if err != nil || payer != 8 {
		t.Fatalf("个人空间应自己付: %d %v", payer, err)
	}
	if err := CanUseTeamPool(8, 0); err != nil {
		t.Fatalf("个人空间不限制: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestPayerForTeamExpired 团队空间：成员 → 团队 → owner 会员是创作版（不是团队版）→ 团队积分不可用。
func TestPayerForTeamExpired(t *testing.T) {
	mock := mockDB(t)
	now := time.Now().Unix()
	mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "team_id", "user_id", "role"}).AddRow(2, 3, 8, model.TeamRoleMember))
	mock.ExpectQuery("SELECT .* FROM `team`").WillReturnRows(sqlmock.NewRows([]string{"id", "owner_id"}).AddRow(3, 9))
	mock.ExpectQuery("SELECT .* FROM `membership`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "edition", "status", "expires_at"}).AddRow(1, 9, model.EditionCreator, "active", now+86400))
	if _, err := PayerForTeam(8, 3); !errors.Is(err, ErrTeamEditionExpired) {
		t.Fatalf("err = %v, want ErrTeamEditionExpired", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestFreezeTaskPointsTeamExpired 团队空间任务在会员过期后预扣直接失败，不会退回去扣成员自己的钱包。
func TestFreezeTaskPointsTeamExpired(t *testing.T) {
	mock := mockDB(t)
	mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "team_id", "user_id", "role"}).AddRow(2, 3, 8, model.TeamRoleMember))
	mock.ExpectQuery("SELECT .* FROM `team`").WillReturnRows(sqlmock.NewRows([]string{"id", "owner_id"}).AddRow(3, 9))
	mock.ExpectQuery("SELECT .* FROM `membership`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	task := &model.GenerationTask{Id: 7, UserId: 8, TeamId: 3, TaskNo: "GT-007"}
	if err := FreezeTaskPoints(task, 10); !errors.Is(err, ErrTeamEditionExpired) {
		t.Fatalf("err = %v, want ErrTeamEditionExpired", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
