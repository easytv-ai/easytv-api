package common

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ApiError 统一错误响应（HTTP 200 + success:false，与前端 wrapIfUnauthorized 兼容）。
func ApiError(c *gin.Context, err error) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": err.Error(),
	})
}

// ApiErrorMsg 统一错误响应（指定消息）。
func ApiErrorMsg(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": msg,
	})
}

// ApiErrorData 统一错误响应 + 机器可读的 data。
// 前端要按原因分流时用它（比如并发已满应该排队重试，而不是当成任务失败）；
// message 仍然是给人看的那句话。
func ApiErrorData(c *gin.Context, msg string, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": msg,
		"data":    data,
	})
}

// ApiSuccess 统一成功响应（HTTP 200 + success:true + data）。
func ApiSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

// ApiSuccessMsg 统一成功响应（带消息）。
func ApiSuccessMsg(c *gin.Context, msg string, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": msg,
		"data":    data,
	})
}

// DecodeJson 解析 JSON 请求体到 v，失败返回错误。
func DecodeJson(body io.Reader, v any) error {
	if body == nil {
		return ErrInvalidRequestBody
	}
	return json.NewDecoder(body).Decode(v)
}
