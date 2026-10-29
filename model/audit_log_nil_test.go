package model

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// TestReadDeviceNilContext 查单补单（支付宝 / 微信）传 nil 上下文时不能 panic。
func TestReadDeviceNilContext(t *testing.T) {
	var c *gin.Context
	if got := readDevice(c); got != "" {
		t.Fatalf("got %q", got)
	}
}
