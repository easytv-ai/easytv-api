package service

import (
	"errors"

	"easytv-api/model"
)

// ===== 个人空间 / 团队空间（API.md 20）=====
//
// 请求头 EasyTV-Team 选择空间（中间件校验成员关系后写进 gin 上下文）：
//   - 个人空间（teamId = 0）：一律花自己的钱包、按自己的会员判断会员价，团队成员也一样；
//   - 团队空间（teamId > 0）：花团队 owner 的积分池，owner 的团队版会员过期时不能生成；查看者不能用团队积分生成。
// 生成任务 / 批次 / 合成在创建时把空间写进 team_id，后续扣费、退款、产物归属都读存下来的 team_id，不再看当前团队关系。

var (
	// ErrTeamEditionExpired 团队空间里 owner 的团队版会员已失效
	ErrTeamEditionExpired = errors.New("团队版会员已过期，团队积分暂不可用，请联系团队负责人续费或切回个人空间")
	// ErrTeamViewer 查看者不能花团队积分
	ErrTeamViewer = errors.New("你在团队里是查看者，不能使用团队积分生成，请联系团队管理员调整权限")
)

// decideTeamPayer 团队空间的买单账户（纯函数，便于测试）。
// teamId<=0 个人空间 → 自己；团队空间要求 member 属于该团队、团队存在，且 owner 的团队版会员有效。
func decideTeamPayer(userId, teamId int, member *model.TeamMember, team *model.Team, ownerTeamActive bool) (int, error) {
	if teamId <= 0 {
		return userId, nil
	}
	if member == nil || member.TeamId != teamId || team == nil || team.Id != teamId {
		return 0, model.ErrNotTeamMember
	}
	if !ownerTeamActive {
		return 0, ErrTeamEditionExpired
	}
	return team.OwnerId, nil
}

// checkTeamPoolPermission 团队空间里查看者不能用团队积分生成（纯函数）。
func checkTeamPoolPermission(teamId int, member *model.TeamMember) error {
	if teamId <= 0 {
		return nil
	}
	if member == nil || member.TeamId != teamId {
		return model.ErrNotTeamMember
	}
	if member.EffectivePermission() == model.TeamPermViewer {
		return ErrTeamViewer
	}
	return nil
}

// decideTaskTeam 生成任务落在哪个空间（纯函数）：
//   - 挂在项目上：跟项目走（project.team_id）；团队项目要求用户仍是该团队成员，否则按「不存在」处理；
//   - 没有项目（主体参考图、无项目的草稿任务等）：用请求所在空间。
func decideTaskTeam(workspaceTeam int, project *model.Project, member *model.TeamMember) (int, error) {
	if project == nil {
		return workspaceTeam, nil
	}
	if project.TeamId <= 0 {
		return 0, nil
	}
	if member == nil || member.TeamId != project.TeamId {
		return 0, model.ErrNotFound
	}
	return project.TeamId, nil
}

// membershipOf 用户的团队成员记录；不在团队返回 nil。
func membershipOf(userId int) *model.TeamMember {
	m, err := model.GetTeamMembership(userId)
	if err != nil {
		return nil
	}
	return m
}

// ownerTeamEditionActive owner 是否有有效的团队版会员。
func ownerTeamEditionActive(ownerId int) bool {
	ms := model.GetActiveMembership(ownerId)
	return ms != nil && ms.Edition == model.EditionTeam
}

// PayerForTeam 在 teamId 这个空间里生成时由谁买单：个人空间是自己；团队空间是 owner（团队版会员有效时），
// 否则返回 ErrTeamEditionExpired / model.ErrNotTeamMember。
func PayerForTeam(userId, teamId int) (int, error) {
	if teamId <= 0 {
		return userId, nil
	}
	m := membershipOf(userId)
	var team *model.Team
	active := false
	if m != nil && m.TeamId == teamId {
		if t, err := model.GetTeamById(teamId); err == nil {
			team = t
			active = ownerTeamEditionActive(t.OwnerId)
		}
	}
	return decideTeamPayer(userId, teamId, m, team, active)
}

// IsMemberForPricing 是否按会员价计费：个人空间只看自己的会员；团队空间看 owner 的团队版会员。
func IsMemberForPricing(userId, teamId int) bool {
	if teamId <= 0 {
		return model.GetActiveMembership(userId) != nil
	}
	_, err := PayerForTeam(userId, teamId)
	return err == nil
}

// CanUseTeamPool 团队空间里查看者不能花团队积分生成；个人空间不限制。
func CanUseTeamPool(userId, teamId int) error {
	if teamId <= 0 {
		return nil
	}
	return checkTeamPoolPermission(teamId, membershipOf(userId))
}

// ResolveTaskTeam 生成任务的空间：挂项目的跟项目走，否则用请求空间（规则见 decideTaskTeam）。
// 项目已不存在时按没有项目处理。
func ResolveTaskTeam(userId, workspaceTeam, projectId int) (int, error) {
	var project *model.Project
	if projectId > 0 {
		if p, err := model.GetProjectById(projectId); err == nil {
			project = p
		} else if !errors.Is(err, model.ErrNotFound) {
			return 0, err
		}
	}
	var member *model.TeamMember
	if project != nil && project.TeamId > 0 {
		member = membershipOf(userId)
	}
	return decideTaskTeam(workspaceTeam, project, member)
}

// TeamOwnerOf 团队 owner；团队不存在返回 0。
func TeamOwnerOf(teamId int) int {
	if teamId <= 0 {
		return 0
	}
	t, err := model.GetTeamById(teamId)
	if err != nil {
		return 0
	}
	return t.OwnerId
}

// MyTeams GET /user/team/mine：空间切换器用，0 或 1 个团队。
func MyTeams(userId int) []map[string]any {
	items := []map[string]any{}
	m := membershipOf(userId)
	if m == nil {
		return items
	}
	team, err := model.GetTeamById(m.TeamId)
	if err != nil {
		return items
	}
	ms := model.GetActiveMembership(team.OwnerId)
	active := ms != nil && ms.Edition == model.EditionTeam
	var expiresAt int64
	if active {
		expiresAt = ms.ExpiresAt
	}
	items = append(items, map[string]any{
		"id":             team.Id,
		"name":           team.Name,
		"role":           m.Role,
		"permission":     m.EffectivePermission(),
		"member_count":   model.CountTeamMembers(nil, team.Id),
		"edition_active": active,
		"expires_at":     expiresAt,
	})
	return items
}

// ProjectAccess 当前用户对项目的权限标记（纯函数）：作者是 owner，可编辑、删除、换空间；
// 团队项目的非作者按团队权限：管理员 / 编辑者可编辑，管理员可删除 / 恢复，查看者只读。teamPerm 为空表示不是该团队成员。
func ProjectAccess(viewerId int, p *model.Project, teamPerm string) (perm string, canEdit, canDelete, canMove bool) {
	if p.UserId == viewerId {
		return CanvasPermOwner, true, true, true
	}
	if p.TeamId <= 0 || teamPerm == "" {
		return "", false, false, false
	}
	return teamPerm, CanvasWritable(teamPerm), teamPerm == model.TeamPermAdmin, false
}

// FillProjectAccess 给项目行补 owner_id / permission / can_edit / can_delete / can_move / team_shared。
// member 为当前用户的团队成员记录（可 nil）。
func FillProjectAccess(viewerId int, member *model.TeamMember, list ...*model.Project) {
	for _, p := range list {
		teamPerm := ""
		if member != nil && p.TeamId > 0 && member.TeamId == p.TeamId {
			teamPerm = member.EffectivePermission()
		}
		p.OwnerId = p.UserId
		p.Permission, p.CanEdit, p.CanDelete, p.CanMove = ProjectAccess(viewerId, p, teamPerm)
		p.TeamShared = p.UserId != viewerId
	}
}

// MembershipOf 导出给控制器：用户的团队成员记录，不在团队返回 nil。
func MembershipOf(userId int) *model.TeamMember { return membershipOf(userId) }

// ErrProjectSameSpace 项目已经在目标空间
var ErrProjectSameSpace = errors.New("项目已经在这个空间里了")

// CheckProjectMove 项目换空间的校验（纯函数，调用方已确认 p 是当前用户自己的项目）：
// 目标与当前相同拒绝；移入团队要求 member 属于该团队且不是查看者；移回个人空间不限制。
func CheckProjectMove(p *model.Project, member *model.TeamMember, targetTeam int) error {
	if p.TeamId == targetTeam {
		return ErrProjectSameSpace
	}
	if targetTeam == 0 {
		return nil
	}
	if member == nil || member.TeamId != targetTeam {
		return model.ErrNotTeamMember
	}
	if member.EffectivePermission() == model.TeamPermViewer {
		return errors.New("你在团队里是查看者，不能把项目移入团队")
	}
	return nil
}
