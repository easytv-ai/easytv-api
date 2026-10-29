package middleware

import (
	"context"
	"fmt"

	"easytv-api/common"

	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

// RouteTag 为请求设置路由标签（用于 [GIN] 访问日志区分来源）。
func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

// RequestId 为每个请求生成 request id，写入 context 与响应头。
func RequestId() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := common.NewRequestId()
		c.Set(common.RequestIdKey, id)
		ctx := context.WithValue(c.Request.Context(), common.RequestIdKey, id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(common.RequestIdKey, id)
		c.Next()
	}
}

// SetUpLogger [GIN] 访问日志：时间 | 路由标签 | request id | 状态码 | 耗时 | 客户端 IP | 方法 | 路径。
func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			param.Path,
		)
	}))
}

// CustomRecovery panic 恢复，返回统一 JSON 错误。
func CustomRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				common.SysError(fmt.Sprintf("panic recovered: %v", err))
				c.JSON(500, gin.H{
					"success": false,
					"message": "服务器内部错误",
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
