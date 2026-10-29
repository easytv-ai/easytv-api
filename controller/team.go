package controller

import (
	"errors"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 团队（用户侧，API.md 6.7）=====

// ValidateTeamRechargeRequester 校验团队充值发起人：团队须存在且发起人须为该团队 owner。
// easytv 团队角色只有 owner/member（无 easyapi 的 admin），故仅 owner 可发起团队充值。
// 返回空串表示通过，否则返回中文错误原因。
func ValidateTeamRechargeRequester(userId, teamId int) string {
	if _, err := model.GetTeamById(teamId); err != nil {
		return "团队不存在"
	}
	m, err := model.GetTeamMembership(userId)
	if err != nil || m.TeamId != teamId {
		return "不是该团队成员"
	}
	if m.Role != model.TeamRoleOwner {
		return "仅团队所有者可以为团队充值"
	}
	return ""
}

// GetMyTeam 我的团队（GET /user/team）；不在团队返回 data=null。
func GetMyTeam(c *gin.Context) {
	common.ApiSuccess(c, service.TeamView(c.GetInt("id")))
}

// GetMyTeams 空间切换器（GET /user/team/mine）：`{ items: [{ id, name, role, permission, member_count, edition_active, expires_at }] }`，
// 不在团队时 items 为空（API.md 20）。
func GetMyTeams(c *gin.Context) {
	common.ApiSuccess(c, gin.H{"items": service.MyTeams(c.GetInt("id"))})
}

// RenameMyTeam 改团队名（PUT /user/team {name}），仅 owner。
func RenameMyTeam(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	name := trimLimit(req.Name, 50)
	if name == "" {
		common.ApiErrorMsg(c, "请填写团队名称")
		return
	}
	if err := service.RenameTeam(c.GetInt("id"), name); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, nil)
}

// RegenerateTeamInvite 重新生成邀请码（POST /user/team/invite），仅 owner。
func RegenerateTeamInvite(c *gin.Context) {
	inv, err := service.RegenerateInvite(c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"code": inv.Code, "expires_at": inv.ExpiresAt})
}

// JoinTeam 用邀请码加入（POST /user/team/join {code}）。
func JoinTeam(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || trimLimit(req.Code, 16) == "" {
		common.ApiErrorMsg(c, "请填写邀请码")
		return
	}
	team, err := service.JoinTeam(c.GetInt("id"), req.Code)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"team_id": team.Id, "name": team.Name})
}

// QuitTeam 退出团队（POST /user/team/quit）。
func QuitTeam(c *gin.Context) {
	if err := service.QuitTeam(c.GetInt("id")); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, nil)
}

// KickTeamMember 移除成员（DELETE /user/team/member/:uid），仅 owner。
func KickTeamMember(c *gin.Context) {
	uid := paramInt(c, "uid")
	if uid == 0 {
		common.ApiErrorMsg(c, "无效的成员 ID")
		return
	}
	if err := service.KickMember(c.GetInt("id"), uid); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			common.ApiErrorMsg(c, "该用户不是团队成员")
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, nil)
}
