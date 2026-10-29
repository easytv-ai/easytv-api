package controller

import (
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// DeactivateSelf 用户自助注销（POST /user/self/deactivate）：`{ password }`。
//
// 规则：管理员不能自助注销；团队所有者需先解散团队（踢掉其他成员）；有密码的账号必须校验密码。
// 动作：作品全部下架、画布分享关闭、退出所在团队、账号标识改为 deleted-<id>-… 释放邮箱 / 手机号 / 微信绑定，
// 昵称改为「已注销用户」，令牌失效，最后软删除。积分与会员随账号作废，不退款。
func DeactivateSelf(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Password string `json:"password"`
	}
	if c.Request.ContentLength > 0 {
		if err := common.DecodeJson(c.Request.Body, &req); err != nil {
			common.ApiErrorMsg(c, "无效的参数")
			return
		}
	}
	user, err := model.GetUserById(userId)
	if err != nil || user == nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	if user.Role >= constant.RoleAdminUser {
		common.ApiErrorMsg(c, "管理员账号不能自助注销，请联系超级管理员")
		return
	}
	if user.Password != "" {
		if strings.TrimSpace(req.Password) == "" {
			common.ApiErrorMsg(c, "请输入登录密码确认")
			return
		}
		if !common.ValidatePasswordAndHash(req.Password, user.Password) {
			common.ApiErrorMsg(c, "密码不正确")
			return
		}
	}
	if m, err := model.GetTeamMembership(userId); err == nil {
		if m.Role == model.TeamRoleOwner {
			if members, err := model.GetTeamMembers(m.TeamId); err == nil && len(members) > 1 {
				common.ApiErrorMsg(c, "你是团队所有者，请先在「设置 · 团队」移除其他成员再注销")
				return
			}
		}
		if m.Role == model.TeamRoleMember {
			_ = model.RemoveTeamMember(m.TeamId, userId)
		}
	}

	now := common.GetTimestamp()
	// 作品下架、分享关闭
	model.DB.Model(&model.Work{}).Where("user_id = ? AND status = ?", userId, model.WorkStatusPublished).
		Updates(map[string]any{"status": model.WorkStatusUnlisted, "remark": "账号已注销", "updated_at": now})
	model.DB.Model(&model.Canvas{}).Where("user_id = ? AND share_enabled = ?", userId, true).
		Updates(map[string]any{"share_enabled": false, "updated_at": now})
	// 释放唯一标识，避免占用邮箱 / 手机号 / 第三方绑定
	prefix := fmt.Sprintf("deleted-%d-%d-", userId, now)
	fields := map[string]any{
		"username": prefix + user.Username,
		"nickname": "已注销用户",
		"status":   constant.UserStatusDisabled,
	}
	for _, col := range []string{"mobile", "wechat_id", "github_id", "discord_id", "oidc_id", "linux_do_id", "telegram_id"} {
		fields[col] = ""
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", userId).Updates(fields).Error; err != nil {
		common.SysError("deactivate: anonymize failed: " + err.Error())
		common.ApiErrorMsg(c, "注销失败，请稍后重试")
		return
	}
	_ = model.InvalidateUserAccessToken(userId)
	if err := model.DB.Where("id = ?", userId).Delete(&model.User{}).Error; err != nil {
		common.SysError("deactivate: delete failed: " + err.Error())
		common.ApiErrorMsg(c, "注销失败，请稍后重试")
		return
	}
	common.SysLog(fmt.Sprintf("user %d deactivated self", userId))
	common.ApiSuccess(c, nil)
}
