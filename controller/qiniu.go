package controller

import (
	"easytv-api/common"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// GetQiniuUploadToken 获取七牛上传 Token（GET /user/qiniu/upload-token?key=，UserAuth）。
// 返回 up_token / upload_token / bucket / domain，供前端直传七牛（镜像母版 easyapi-ai-api）。
func GetQiniuUploadToken(c *gin.Context) {
	if !setting.IsQiniuEnabled() {
		common.ApiErrorMsg(c, "七牛存储未启用，请先在管理端配置 QiniuAccessKey / QiniuSecretKey")
		return
	}
	key := c.Query("key")
	bucket := setting.GetQiniuBucketName()
	token := service.GenerateQiniuUploadToken(bucket, key)
	common.ApiSuccess(c, gin.H{
		"up_token":     token,
		"upload_token": token,
		"bucket":       bucket,
		"domain":       setting.GetQiniuDomain(),
	})
}

// GetQiniuKey 获取七牛存储 key（GET /user/qiniu/key?suffix=）。
func GetQiniuKey(c *gin.Context) {
	if !setting.IsQiniuEnabled() {
		common.ApiErrorMsg(c, "七牛存储未启用，请先在管理后台配置七牛存储")
		return
	}
	suffix := c.DefaultQuery("suffix", "")
	key := service.GenerateQiniuKey(suffix)
	common.ApiSuccess(c, gin.H{
		"key":    key,
		"domain": setting.GetQiniuDomain(),
	})
}