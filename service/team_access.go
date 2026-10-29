package service

import (
	"errors"

	"easytv-api/model"
)

// 团队角色与画布访问（API.md 15）：
//   - 所有者（画布作者）：全部权限；
//   - 管理员：编辑者权限 + 管理成员权限与额度、移除成员、重置邀请码、删除 / 恢复队友项目；
//   - 编辑者：打开并保存队友画布、恢复版本、在队友画布上生成（花团队积分，受月度额度）；不能分享、删除；
//   - 查看者：只读打开队友画布、批注；不能用团队积分生成。
// 画布写操作仍按作者 user_id 落库（model 层条件不变），权限在这里判断。

const (
	CanvasPermOwner  = "owner"
	CanvasPermAdmin  = model.TeamPermAdmin
	CanvasPermEditor = model.TeamPermEditor
	CanvasPermViewer = model.TeamPermViewer
)

var ErrCanvasForbidden = errors.New("没有权限修改这张画布")

// TeamPermissionOf 用户在所在团队的权限；不在团队返回空串。
func TeamPermissionOf(userId int) string {
	m, err := model.GetTeamMembership(userId)
	if err != nil || m == nil {
		return ""
	}
	return m.EffectivePermission()
}

// CanvasAccess 取画布与访问级别：自己的是 owner；非作者只有画布所属项目在自己所在团队的团队空间时（project.team_id，API.md 20）
// 才按成员权限放行；队友的个人项目、其他团队的项目一律 ErrNotFound。
func CanvasAccess(userId, canvasId int) (*model.Canvas, string, error) {
	if cv, err := model.GetUserCanvas(userId, canvasId); err == nil {
		return cv, CanvasPermOwner, nil
	} else if !errors.Is(err, model.ErrNotFound) {
		return nil, "", err
	}
	cv, err := model.GetCanvasById(canvasId)
	if err != nil || cv.ProjectId <= 0 {
		return nil, "", model.ErrNotFound
	}
	p, err := model.GetProjectById(cv.ProjectId)
	if err != nil || p.TeamId <= 0 {
		return nil, "", model.ErrNotFound
	}
	m := membershipOf(userId)
	if m == nil || m.TeamId != p.TeamId {
		return nil, "", model.ErrNotFound
	}
	return cv, m.EffectivePermission(), nil
}

// CanvasWritable 所有者、管理员、编辑者可以改画布内容。
func CanvasWritable(perm string) bool {
	return perm == CanvasPermOwner || perm == CanvasPermAdmin || perm == CanvasPermEditor
}

// TeamManager 团队的所有者或管理员；返回团队与操作者权限。
func TeamManager(userId int) (*model.Team, *model.TeamMember, error) {
	m, err := model.GetTeamMembership(userId)
	if err != nil || m == nil {
		return nil, nil, errors.New("你还不在团队里")
	}
	if m.EffectivePermission() != model.TeamPermAdmin {
		return nil, nil, errors.New("只有团队所有者或管理员可以操作")
	}
	team, err := model.GetTeamById(m.TeamId)
	if err != nil {
		return nil, nil, err
	}
	return team, m, nil
}

// canvasTeamId 画布所属项目的团队空间；个人项目 / 没有项目返回 0。
func canvasTeamId(cv *model.Canvas) int {
	if cv == nil || cv.ProjectId <= 0 {
		return 0
	}
	p, err := model.GetProjectById(cv.ProjectId)
	if err != nil {
		return 0
	}
	return p.TeamId
}

// canvasAssetOwners 画布相关素材可能属于作者或任一队友（团队项目画布上队友生成的产物），合成 / 镜头视图按这组用户找资产。
// 只有团队空间的项目才算上队友（API.md 20）。
func canvasAssetOwners(cv *model.Canvas) []int {
	ids := []int{cv.UserId}
	teamId := canvasTeamId(cv)
	if teamId == 0 {
		return ids
	}
	members, _ := model.GetTeamMembers(teamId)
	for _, m := range members {
		if m.UserId != cv.UserId {
			ids = append(ids, m.UserId)
		}
	}
	return ids
}

// canvasAsset 画布上可用的资产：作者的，或画布所在团队空间的资产。
func canvasAsset(cv *model.Canvas, assetId int) (*model.Asset, error) {
	if a, err := model.GetUserAsset(cv.UserId, assetId); err == nil {
		return a, nil
	}
	return teamAsset(canvasTeamId(cv), assetId)
}

// teamAsset 取 teamId 团队空间里的资产（未删除）；teamId<=0 或不属于该团队返回 ErrNotFound。
func teamAsset(teamId, assetId int) (*model.Asset, error) {
	if teamId <= 0 {
		return nil, model.ErrNotFound
	}
	a, err := model.GetAssetByIdUnscoped(assetId)
	if err != nil || a.DeletedAt.Valid || !assetInTeam(a, teamId) {
		return nil, model.ErrNotFound
	}
	return a, nil
}

// assetInTeam 资产属于 teamId 团队空间（纯函数）。
func assetInTeam(a *model.Asset, teamId int) bool {
	return a != nil && teamId > 0 && a.TeamId == teamId
}

// UsableAsset 在 teamId 空间里可用的资产：自己的，或该团队空间的（API.md 20）。
func UsableAsset(userId, teamId, assetId int) (*model.Asset, error) {
	if a, err := model.GetUserAsset(userId, assetId); err == nil {
		return a, nil
	}
	return teamAsset(teamId, assetId)
}

// AssetUsableBy 创建任务时输入素材的归属校验：自己的，或任务所在团队空间的资产。
func AssetUsableBy(userId, teamId, assetId int) bool {
	_, err := UsableAsset(userId, teamId, assetId)
	return err == nil
}
