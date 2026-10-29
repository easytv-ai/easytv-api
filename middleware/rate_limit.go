package middleware

import (
	"fmt"
	"time"

	"easytv-api/common"

	"github.com/gin-gonic/gin"
)

// rateLimitKeyPrefix 限流计数 key 前缀。
const rateLimitKeyPrefix = "easytv:ratelimit:"

// rateLimit 通用 Redis 滑动窗口限流。
// window 为窗口秒数，limit 为窗口内最大次数；key 为限流维度（如 ip、user id）。
func rateLimit(c *gin.Context, window int64, limit int64, key string) bool {
	if common.RedisClient == nil {
		return true // Redis 未初始化时放行
	}
	redisKey := fmt.Sprintf("%s%s:%d:%s", rateLimitKeyPrefix, c.Request.URL.Path, window, key)
	count, err := common.RedisIncr(redisKey)
	if err != nil {
		common.SysLog("rate limit incr failed: " + err.Error())
		return true
	}
	if count == 1 {
		_ = common.RedisSetEX(redisKey, 1, time.Duration(window)*time.Second)
	}
	if count > limit {
		return false
	}
	return true
}

// CriticalRateLimit 关键接口限流（登录/注册/充值等），按 IP + 路径，默认 10 次/60 秒。
func CriticalRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rateLimit(c, 60, 10, c.ClientIP()) {
			common.ApiErrorMsg(c, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// LoginStatusRateLimit 登录状态轮询限流（按 IP + 路径，30 次/60 秒）。
// 轮询间隔较短，放宽到 30 次/60s，避免扫码等待期频繁触发 CriticalRateLimit 的 10 次/60s 上限。
func LoginStatusRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rateLimit(c, 60, 30, c.ClientIP()) {
			common.ApiErrorMsg(c, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// TrackRateLimit 事件上报限流，按 IP + 路径，60 次/60 秒（前端每 2 秒或攒 10 条才发一次）。
func TrackRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rateLimit(c, 60, 60, c.ClientIP()) {
			common.ApiErrorMsg(c, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// GlobalAPIRateLimit 全局 API 限流，按 IP，默认 300 次/60 秒。
func GlobalAPIRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rateLimit(c, 60, 300, "global:"+c.ClientIP()) {
			common.ApiErrorMsg(c, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// UserCriticalRateLimit 用户维度关键操作限流，按 user id，默认 30 次/60 秒。
func UserCriticalRateLimit(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userId := c.GetInt("id")
		if userId <= 0 {
			userId = 0
		}
		key := fmt.Sprintf("user:%d:%s", userId, action)
		if !rateLimit(c, 60, 30, key) {
			common.ApiErrorMsg(c, "操作过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// EmailVerificationRateLimit 邮箱验证码发送限流，按邮箱 + IP，默认 5 次/300 秒。
func EmailVerificationRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		email := c.Query("email")
		if email == "" {
			common.ApiErrorMsg(c, "缺少邮箱参数")
			c.Abort()
			return
		}
		key := fmt.Sprintf("email:%s:%s", email, c.ClientIP())
		if !rateLimit(c, 300, 5, key) {
			common.ApiErrorMsg(c, "验证码发送过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// SmsVerificationRateLimit 短信验证码发送限流，按手机号 + IP，默认 5 次/300 秒。
func SmsVerificationRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		mobile := c.Query("mobile")
		if mobile == "" {
			common.ApiErrorMsg(c, "缺少手机号参数")
			c.Abort()
			return
		}
		key := fmt.Sprintf("mobile:%s:%s", mobile, c.ClientIP())
		if !rateLimit(c, 300, 5, key) {
			common.ApiErrorMsg(c, "验证码发送过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}
