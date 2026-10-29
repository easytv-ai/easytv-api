package controller

import (
	"easytv-api/common"

	"github.com/gin-gonic/gin"
)

// Verify2FALogin 两步验证登录（预留：本期未实现 TOTP，返回错误提示）。
func Verify2FALogin(c *gin.Context) {
	common.ApiErrorMsg(c, "两步验证暂未开放")
}

// PasskeyLoginBegin Passkey 登录（预留占位）。
func PasskeyLoginBegin(c *gin.Context) {
	common.ApiErrorMsg(c, "Passkey 登录暂未开放")
}

// PasskeyLoginFinish Passkey 登录（预留占位）。
func PasskeyLoginFinish(c *gin.Context) {
	common.ApiErrorMsg(c, "Passkey 登录暂未开放")
}
