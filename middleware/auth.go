package middleware

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ParseUserAccessToken 从 Authorization 头读取 access_token（去掉 Bearer 前缀）。
func ParseUserAccessToken(c *gin.Context) string {
	return model.NormalizeUserAccessToken(c.GetHeader(constant.HeaderAuthorization))
}

// getEasyTVUserHeader 读取用户标识请求头（EasyTV-User）。
func getEasyTVUserHeader(c *gin.Context) string {
	return c.Request.Header.Get(constant.HeaderEasyTVUser)
}

type resolvedUserAuth struct {
	username       string
	role           int
	id             int
	status         int
	group          string
	useAccessToken bool
}

// resolveUserAuth 通过 access_token 解析用户（easytv-api 无 cookie session，仅 access_token 通道）。
// 返回值：userAuth 解析成功时为非 nil；errDb 表示数据库错误（需区分提示）；authErr 表示凭证无效。
func resolveUserAuth(c *gin.Context) (auth *resolvedUserAuth, dbErr error, authErr error) {
	token := ParseUserAccessToken(c)
	if token == "" {
		return nil, nil, nil // 未提供 token
	}
	user, err := model.ValidateAccessToken(token)
	if err != nil {
		if errors.Is(err, common.ErrDatabase) {
			return nil, err, nil
		}
		return nil, nil, err
	}
	if user == nil || user.Username == "" {
		return nil, nil, common.ErrTokenInvalid
	}
	return &resolvedUserAuth{
		username:       user.Username,
		role:           user.Role,
		id:             user.Id,
		status:         user.Status,
		group:          user.Group,
		useAccessToken: true,
	}, nil, nil
}

// authHelper 统一鉴权：access_token + EasyTV-User 头双通道，校验角色与用户状态。
func authHelper(c *gin.Context, minRole int) {
	auth, dbErr, authErr := resolveUserAuth(c)
	switch {
	case dbErr != nil:
		common.ApiErrorMsg(c, "数据库错误")
		c.Abort()
		return
	case authErr != nil:
		common.ApiErrorMsg(c, "无效的登录凭证")
		recordAdminAccessDenied(c, minRole, "invalid_token", nil)
		c.Abort()
		return
	case auth == nil:
		// 未提供 token
		common.ApiErrorMsg(c, "未登录或登录已过期")
		recordAdminAccessDenied(c, minRole, "not_logged_in", nil)
		c.Abort()
		return
	}

	// EasyTV-User 头校验（与前端 auth-user.ts 对齐）
	apiUserIdStr := getEasyTVUserHeader(c)
	if apiUserIdStr == "" {
		common.ApiErrorMsg(c, "未提供用户标识（EasyTV-User 头）")
		recordAdminAccessDenied(c, minRole, "user_header_missing", auth)
		c.Abort()
		return
	}
	apiUserId, err := strconv.Atoi(apiUserIdStr)
	if err != nil {
		common.ApiErrorMsg(c, "用户标识格式错误")
		recordAdminAccessDenied(c, minRole, "user_header_invalid", auth)
		c.Abort()
		return
	}
	if auth.id != apiUserId {
		common.ApiErrorMsg(c, "用户标识与登录凭证不匹配")
		recordAdminAccessDenied(c, minRole, "user_mismatch", auth)
		c.Abort()
		return
	}
	if auth.status == constant.UserStatusDisabled {
		common.ApiErrorMsg(c, "账号已被禁用")
		recordAdminAccessDenied(c, minRole, "user_disabled", auth)
		c.Abort()
		return
	}
	if auth.role < minRole {
		common.ApiErrorMsg(c, "权限不足")
		recordAdminAccessDenied(c, minRole, "insufficient_role", auth)
		c.Abort()
		return
	}

	c.Set("username", auth.username)
	c.Set("role", auth.role)
	c.Set("id", auth.id)
	c.Set("group", auth.group)
	c.Set("user_group", auth.group)
	c.Set("use_access_token", auth.useAccessToken)
	c.Next()
}

// UserAuth 普通用户鉴权。
func UserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, constant.RoleCommonUser)
	}
}

// AdminAuth 管理员鉴权。
func AdminAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, constant.RoleAdminUser)
	}
}

// RootAuth 超级管理员鉴权。
func RootAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, constant.RoleRootUser)
	}
}

// TryUserAuth 可选鉴权：提供有效 token 时写入用户上下文，未提供则放行（匿名）。
func TryUserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		if token := ParseUserAccessToken(c); token != "" {
			user, err := model.ValidateAccessToken(token)
			if err == nil && user != nil && user.Status == constant.UserStatusEnabled {
				c.Set("id", user.Id)
				c.Set("username", user.Username)
				c.Set("role", user.Role)
				c.Set("group", user.Group)
				c.Set("user_group", user.Group)
				c.Next()
				return
			}
		}
		c.Next()
	}
}

// validUserInfo 校验用户名与角色合法性。
func validUserInfo(username string, role int) bool {
	if strings.TrimSpace(username) == "" {
		return false
	}
	if !common.IsValidateRole(role) {
		return false
	}
	return true
}

// UnauthorizedResponse 统一未授权响应（供 controller 复用）。
func UnauthorizedResponse(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": msg,
	})
}
