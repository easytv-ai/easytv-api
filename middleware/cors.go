package middleware

import (
	"github.com/gin-gonic/gin"
)

// CORS 跨域中间件：回显任意来源。
// 带 Origin 的请求原样回显该来源并允许携带凭据；预检 OPTIONS 直接返回 204；
// 无 Origin 的请求不干预。鉴权依赖 Authorization 头而非 Cookie，因此放行所有
// 来源不会扩大 CSRF 面；自托管时如需收紧，可在反向代理层限制 Origin。
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, EasyTV-User, EasyTV-Device, EasyTV-Team, Accept-Language, X-Requested-With")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
