package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SetRouter 聚合全部路由。
func SetRouter(router *gin.Engine) {
	// 存活探针：容器编排与负载均衡用，不查库、不鉴权。
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	SetApiRouter(router)
	SetAdminRouter(router)
}
