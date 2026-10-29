package service

import (
	"errors"
	"fmt"

	"easytv-api/common"
	"easytv-api/model"

	"gorm.io/gorm"
)

// ===== 团队版会员（API.md 6.7）=====
//
// 规则：
//   - 购买团队版会员的用户成为 owner，团队席位 = 订单 seats，续费/换档同步席位
//   - 成员通过邀请码加入；一个用户同时只属于一个团队
//   - 个人空间 / 团队空间（API.md 20）：个人空间永远花自己的钱包；只有团队空间里的生成才从 owner 的积分池扣减、退回并享受会员价
//   - 团队版会员过期或退款后，团队空间不能再生成（见 PayerForTeam），个人空间不受影响

// EnsureTeamOnFulfill 团队版会员履约时创建/更新团队（事务内）。
func EnsureTeamOnFulfill(tx *gorm.DB, ownerId, seats int) error {
	name := "我的团队"
	if u, err := model.GetUserById(ownerId); err == nil && u.Nickname != "" {
		name = u.Nickname + "的团队"
	}
	_, err := model.UpsertTeam(tx, ownerId, seats, name)
	return err
}

// TeamView 当前用户的团队视图；不在团队返回 nil。
func TeamView(userId int) map[string]any {
	m, err := model.GetTeamMembership(userId)
	if err != nil {
		return nil
	}
	team, err := model.GetTeamById(m.TeamId)
	if err != nil {
		return nil
	}
	members, _ := model.GetTeamMembers(team.Id)
	model.AttachMemberQuotas(members) // 成员月度额度（F7）
	ownerMembership := model.GetActiveMembership(team.OwnerId)
	view := map[string]any{
		"id":            team.Id,
		"name":          team.Name,
		"owner_id":      team.OwnerId,
		"seats":         team.Seats,
		"used_seats":    len(members),
		"role":          m.Role,
		"permission":    m.EffectivePermission(),
		"members":       members,
		"active":        ownerMembership != nil && ownerMembership.Edition == model.EditionTeam,
		"expires_at":    0,
		"pool_balance":  model.GetPointsBalance(nil, team.OwnerId),
		"invite_code":   "",
		"invite_expiry": 0,
	}
	if ownerMembership != nil {
		view["expires_at"] = ownerMembership.ExpiresAt
		view["plan_name"] = ownerMembership.PlanName
	}
	if m.EffectivePermission() == model.TeamPermAdmin {
		if inv, err := model.GetActiveInvite(team.Id); err == nil {
			view["invite_code"] = inv.Code
			view["invite_expiry"] = inv.ExpiresAt
		}
	}
	return view
}

// JoinTeam 用邀请码加入。
func JoinTeam(userId int, code string) (*model.Team, error) {
	inv, err := model.GetInviteByCode(code)
	if err != nil {
		return nil, err
	}
	team, err := model.GetTeamById(inv.TeamId)
	if err != nil {
		return nil, model.ErrInviteInvalid
	}
	if team.OwnerId == userId {
		return nil, errors.New("你已经是该团队的所有者")
	}
	ms := model.GetActiveMembership(team.OwnerId)
	if ms == nil || ms.Edition != model.EditionTeam {
		return nil, errors.New("该团队的团队版会员已过期，暂不能加入")
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.AddTeamMember(tx, team, userId); err != nil {
			return err
		}
		return model.IncrInviteUses(tx, inv.Id)
	})
	if err != nil {
		return nil, err
	}
	common.SysLog(fmt.Sprintf("team %d: user %d joined via %s", team.Id, userId, inv.Code))
	return team, nil
}

// QuitTeam 成员退出。
func QuitTeam(userId int) error {
	m, err := model.GetTeamMembership(userId)
	if err != nil {
		return errors.New("你不在任何团队里")
	}
	if m.Role == model.TeamRoleOwner {
		return model.ErrOwnerCannotQuit
	}
	return model.RemoveTeamMember(m.TeamId, userId)
}

// KickMember owner 移除成员。
// KickMember 所有者或管理员移除成员；管理员不能移除所有者和其他管理员。
func KickMember(operatorId, userId int) error {
	team, op, err := TeamManager(operatorId)
	if err != nil {
		return err
	}
	if userId == team.OwnerId {
		return model.ErrOwnerCannotQuit
	}
	if err := canManageMember(team, op, userId); err != nil {
		return err
	}
	return model.RemoveTeamMember(team.Id, userId)
}

// canManageMember 目标必须是本团队成员；管理员只能管编辑者和查看者。
func canManageMember(team *model.Team, op *model.TeamMember, targetId int) error {
	target, err := model.GetTeamMembership(targetId)
	if err != nil || target == nil || target.TeamId != team.Id || target.Role == model.TeamRoleOwner {
		return model.ErrNotFound
	}
	if op.Role != model.TeamRoleOwner && target.EffectivePermission() == model.TeamPermAdmin {
		return errors.New("管理员不能管理其他管理员，请联系团队所有者")
	}
	return nil
}

// SetMemberPermission 所有者可设任意权限；管理员只能在编辑者 / 查看者之间调整。
func SetMemberPermission(operatorId, userId int, permission string) error {
	if !model.IsValidTeamPermission(permission) {
		return errors.New("权限只能是 admin / editor / viewer")
	}
	team, op, err := TeamManager(operatorId)
	if err != nil {
		return err
	}
	if err := canManageMember(team, op, userId); err != nil {
		return err
	}
	if op.Role != model.TeamRoleOwner && permission == model.TeamPermAdmin {
		return errors.New("只有团队所有者可以设置管理员")
	}
	return model.SetMemberPermission(team.Id, userId, permission)
}

// RegenerateInvite owner 重新生成邀请码。
func RegenerateInvite(operatorId int) (*model.TeamInvite, error) {
	team, _, err := TeamManager(operatorId)
	if err != nil {
		return nil, err
	}
	return model.CreateInvite(team.Id, operatorId)
}

// RenameTeam owner 改名。
func RenameTeam(ownerId int, name string) error {
	if _, err := model.GetTeamByOwner(ownerId); err != nil {
		return errors.New("你不是团队所有者")
	}
	return model.UpdateTeamName(ownerId, name)
}
