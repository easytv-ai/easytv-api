package controller

import (
	"errors"
	"strconv"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 团队成员月度积分上限（API.md 10.5，F7）=====

// SetTeamMemberLimit PUT /user/team/member/:uid/limit { points_limit }
func SetTeamMemberLimit(c *gin.Context) {
	userId := c.GetInt("id")
	team, _, err := service.TeamManager(userId)
	if err != nil {
		common.ApiErrorMsg(c, "只有团队所有者或管理员可以设置成员额度")
		return
	}
	memberId := paramInt(c, "uid")
	if memberId == 0 || memberId == userId || memberId == team.OwnerId {
		common.ApiErrorMsg(c, "无效的成员")
		return
	}
	var req struct {
		PointsLimit int `json:"points_limit"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.PointsLimit < 0 || req.PointsLimit > 10000000 {
		common.ApiErrorMsg(c, "额度需在 0 到 1000 万之间，0 表示不限")
		return
	}
	if err := model.SetMemberPointsLimit(team.Id, memberId, req.PointsLimit); err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	model.PushNotification(memberId, model.NotificationTypeSystem, "团队额度已更新",
		limitNotice(req.PointsLimit), "/settings?tab=team")
	common.ApiSuccess(c, model.GetMemberQuota(memberId))
}

func limitNotice(limit int) string {
	if limit <= 0 {
		return "团队管理员取消了你的每月积分上限，现在可以自由使用团队积分池。"
	}
	return "团队管理员把你的每月积分上限设为 " + itoaSafe(limit) + " 积分，超出后当月无法再发起生成。"
}

func itoaSafe(n int) string {
	return strconv.Itoa(n)
}

// SetTeamMemberPermission PUT /user/team/member/:uid/permission { permission: admin|editor|viewer }（API.md 15）
func SetTeamMemberPermission(c *gin.Context) {
	userId := c.GetInt("id")
	memberId := paramInt(c, "uid")
	var req struct {
		Permission string `json:"permission"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || memberId == 0 || memberId == userId {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := service.SetMemberPermission(userId, memberId, req.Permission); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			common.ApiErrorMsg(c, "该用户不是团队成员")
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	label := map[string]string{model.TeamPermAdmin: "管理员", model.TeamPermEditor: "编辑者", model.TeamPermViewer: "查看者"}[req.Permission]
	model.PushNotification(memberId, model.NotificationTypeSystem, "团队权限已更新", "你在团队里的权限已调整为「"+label+"」", "/settings?tab=team")
	common.ApiSuccess(c, gin.H{"permission": req.Permission})
}
