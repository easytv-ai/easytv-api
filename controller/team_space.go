package controller

import (
	"errors"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 个人空间 / 团队空间（API.md 20）=====

// teamIdOf 请求所在空间：middleware.TeamSpace 校验过的团队 id；个人空间为 0。
func teamIdOf(c *gin.Context) int {
	return c.GetInt("team_id")
}

// teamPermissionOf 团队空间里当前用户的权限（admin / editor / viewer）；个人空间为空串。
func teamPermissionOf(c *gin.Context) string {
	return c.GetString("team_permission")
}

// respondSpaceError 空间相关错误带机器可读的 reason 回给前端；不是空间错误返回 false 由调用方处理。
//   - team_forbidden：已不在该团队
//   - team_expired：团队版会员过期，团队积分不可用
func respondSpaceError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, model.ErrNotTeamMember):
		common.ApiErrorData(c, err.Error(), gin.H{"reason": "team_forbidden"})
	case errors.Is(err, service.ErrTeamEditionExpired):
		common.ApiErrorData(c, err.Error(), gin.H{"reason": "team_expired"})
	default:
		return false
	}
	return true
}

// resolveTaskTeam 给即将创建的任务定空间（跟画布项目走，否则用请求空间），失败时已回错误。
func resolveTaskTeam(c *gin.Context, task *model.GenerationTask) bool {
	teamId, err := service.ResolveTaskTeam(task.UserId, teamIdOf(c), task.ProjectId)
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return false
	}
	task.TeamId = teamId
	return true
}
