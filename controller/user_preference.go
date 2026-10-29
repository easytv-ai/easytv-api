package controller

import (
	"errors"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 用户偏好（API.md 33）=====
// 前端自定义的小块状态（运镜收藏 / 最近等）跨设备同步。值是 JSON 文本，后端只存不解释。

// GetMyPreferences 全部偏好（GET /user/preference）：`{ "camera.fav": "[...]", ... }`。
func GetMyPreferences(c *gin.Context) {
	prefs, err := model.ListUserPreferences(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, prefs)
}

// SetMyPreference 写一个键（PUT /user/preference/:key），body `{ value }`；value 为空串等于删除。
func SetMyPreference(c *gin.Context) {
	key, err := model.NormalizeUserPreferenceKey(c.Param("key"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, errors.New("无效的参数"))
		return
	}
	value := strings.TrimSpace(req.Value)
	if len(value) > model.UserPreferenceValueMax {
		common.ApiError(c, errors.New("偏好内容过大"))
		return
	}
	userId := c.GetInt("id")
	if value == "" {
		err = model.DeleteUserPreference(userId, key)
	} else {
		err = model.SetUserPreference(userId, key, value)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"key": key, "value": value})
}
