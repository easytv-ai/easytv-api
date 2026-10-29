package middleware

import (
	"github.com/gin-gonic/gin"
)

// CORS 跨域中间件：无条件放行所有来源。
// 任意带 Origin 的请求均回显该来源并允许携带凭据（与 easyapi 母版中继接口的
// allow-all 行为一致）；预检 OPTIONS 直接返回 204；无 Origin 的请求不干预。
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
