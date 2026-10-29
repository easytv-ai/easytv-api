package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// BindMobile 绑定 / 换绑手机号（POST /user/self/mobile {mobile, code}）。
// 验证码复用 GET /sms/verification；短信注册的账号 username 同步改为新手机号，保证短信登录一致。
func BindMobile(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Mobile string `json:"mobile"`
		Code   string `json:"code"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	mobile := strings.TrimSpace(req.Mobile)
	if !model.IsMobileFormat(mobile) {
		common.ApiErrorMsg(c, "手机号格式不正确")
		return
	}
	if !common.VerifyCodeWithKey(mobile, strings.TrimSpace(req.Code), common.SmsLoginPurpose) {
		common.ApiErrorMsg(c, "验证码错误或已过期")
		return
	}
	user, err := model.GetUserById(userId)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	if user.Mobile == mobile || user.Username == mobile {
		common.ApiErrorMsg(c, "该手机号已是当前绑定手机号")
		return
	}
	if model.IsMobileAlreadyTaken(mobile) {
		common.ApiErrorMsg(c, "该手机号已被其他账号绑定")
		return
	}
	fields := map[string]any{"mobile": mobile}
	// username 同步为新手机号：短信注册账号（username 即手机号）与微信扫码自动注册的占位账号（wx_ 前缀），保证短信登录一致
	if model.IsMobileFormat(user.Username) || strings.HasPrefix(user.Username, "wx_") {
		fields["username"] = mobile
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", userId).Updates(fields).Error; err != nil {
		common.ApiErrorMsg(c, "绑定失败")
		return
	}
	common.ApiSuccess(c, gin.H{"mobile": mobile})
}
