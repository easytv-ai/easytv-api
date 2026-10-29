package middleware

import (
	"errors"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 个人空间 / 团队空间（API.md 20）=====

// TeamSpaceHeader 请求头：团队 id 选择团队空间；缺省 / 空 / 0 为个人空间。
const TeamSpaceHeader = "EasyTV-Team"

// ParseTeamHeader 解析 EasyTV-Team：正整数返回团队 id；缺省、空、0、非数字、负数一律按个人空间返回 0。
// 非法值按个人空间处理不会多花别人的钱，最坏只是花自己的钱包。
func ParseTeamHeader(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// teamSpaceExempt 不看空间头的路由：个人资料、团队管理（/user/team/*）与发票（/user/invoice/*，按账户开）。
// 被移出团队后前端还带着旧头，这些接口要照常可用，切换器才能拿到最新团队列表。
func teamSpaceExempt(fullPath string) bool {
	return fullPath == "/user/self" || fullPath == "/user/team" || strings.HasPrefix(fullPath, "/user/team/") ||
		strings.HasPrefix(fullPath, "/user/invoice/")
}

// teamSpaceAllowed 头里的团队是否就是用户所在团队（纯函数）。
func teamSpaceAllowed(teamId int, m *model.TeamMember) bool {
	return teamId > 0 && m != nil && m.TeamId == teamId
}

// TeamSpace 解析团队空间头，放在 UserAuth 之后。头 > 0 时校验当前用户是该团队成员：
// 通过则 c.Set("team_id", id)、c.Set("team_permission", 成员权限)；不是成员返回
// success=false「你已不在该团队，请切回个人空间」+ data.reason=team_forbidden，前端据此切回个人空间。
// 个人空间不写 team_id（c.GetInt 取到 0）。
func TeamSpace() gin.HandlerFunc {
	return func(c *gin.Context) {
		teamId := ParseTeamHeader(c.GetHeader(TeamSpaceHeader))
		if teamId == 0 || teamSpaceExempt(c.FullPath()) {
			c.Next()
			return
		}
		m, err := model.GetTeamMembership(c.GetInt("id"))
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			common.ApiErrorMsg(c, "查询团队失败，请稍后重试")
			c.Abort()
			return
		}
		if !teamSpaceAllowed(teamId, m) {
			common.ApiErrorData(c, model.ErrNotTeamMember.Error(), gin.H{"reason": "team_forbidden", "team_id": teamId})
			c.Abort()
			return
		}
		c.Set("team_id", teamId)
		c.Set("team_permission", m.EffectivePermission())
		c.Next()
	}
}
