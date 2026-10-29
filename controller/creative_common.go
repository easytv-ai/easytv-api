package controller

import (
	"errors"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// paramInt 读取路径参数为正整数，非法返回 0。
func paramInt(c *gin.Context, name string) int {
	v, err := strconv.Atoi(strings.TrimSpace(c.Param(name)))
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

// queryInt 读取查询参数为整数，缺省/非法返回 def。
func queryInt(c *gin.Context, name string, def int) int {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

// respondModelError 统一把 model 层错误转成响应：ErrNotFound → 资源不存在，其它 → fallback 文案。
func respondModelError(c *gin.Context, err error, fallback string) {
	switch {
	case errors.Is(err, model.ErrNotFound):
		common.ApiErrorMsg(c, model.ErrNotFound.Error())
	case errors.Is(err, model.ErrCanvasVersionConflict):
		common.ApiErrorMsg(c, model.ErrCanvasVersionConflict.Error())
	case errors.Is(err, model.ErrTaskStatus):
		common.ApiErrorMsg(c, model.ErrTaskStatus.Error())
	default:
		common.SysError(fallback + ": " + err.Error())
		common.ApiErrorMsg(c, fallback)
	}
}

// pagedResponse 分页响应体。
func pagedResponse(c *gin.Context, items any, total int64) {
	common.ApiSuccess(c, gin.H{
		"items": items,
		"total": total,
	})
}

// trimLimit 去空白并按最大字符数（rune）截断。
func trimLimit(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max])
	}
	return s
}
