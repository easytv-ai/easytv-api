package router

import "github.com/gin-gonic/gin"

// SetRouter 聚合全部路由。
func SetRouter(router *gin.Engine) {
	SetApiRouter(router)
	SetAdminRouter(router)
}
