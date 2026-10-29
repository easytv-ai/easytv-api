package controller

import (
	"easytv-api/common"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// GetAppConfig 前端应用配置（GET /app/config，匿名）：功能入口开关，前端据此显示/隐藏入口。
func GetAppConfig(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		// 3D 导演台入口是否对用户端可见（管理端 PUT /admin/setting/Director3DEnabled 控制）
		"director_3d_enabled": setting.IsDirector3DEnabled(),
		// 发票入口是否显示（管理端 InvoiceEnabled，API.md 21）
		"invoice_enabled": setting.IsInvoiceEnabled(),
		// 增长参数展示值（礼包、任务、签到、邀请、促销条），数值来自管理端「增长」面板
		"growth": setting.GetGrowthConfig(),
		// 服务器当前时间（Unix 秒），前端倒计时按此校准本机时钟偏差（T19）
		"server_time": common.GetTimestamp(),
	})
}
