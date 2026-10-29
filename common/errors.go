package common

import "errors"

var (
	ErrInvalidRequestBody = errors.New("无效的请求体")
	ErrUnauthorized       = errors.New("未登录或登录已过期")
	ErrForbidden          = errors.New("无权限执行该操作")
	ErrDatabase           = errors.New("数据库错误")
	ErrUserNotFound       = errors.New("用户不存在")
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrTokenInvalid       = errors.New("无效的令牌")
)
