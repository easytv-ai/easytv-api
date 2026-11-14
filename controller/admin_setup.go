package controller

import (
	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// AdminSetupStatus 部署就绪状态（GET /admin/setup/status，API.md 40）。
// 只读，只回「是否有值」与计数，不回地址、令牌等任何配置值。
func AdminSetupStatus(c *gin.Context) {
	prices, err := model.GetModelPrices(false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, service.BuildSetupStatus(service.SetupStatusInput{
		GatewayBaseUrl: setting.GetGatewayBaseUrl(),
		HasGatewayKey:  setting.GetGatewayKey() != "",
		GatewayEnabled: setting.IsGatewayEnabled(),
		Prices:         prices,
		StorageKind:    service.CurrentStorageKind(),
		UploadBaseUrl:  setting.GetUploadBaseUrl(),
		FFmpeg:         service.FFmpegAvailable(),
		FFprobe:        service.FFprobeAvailable(),
	}))
}
