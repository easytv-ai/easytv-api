package tests

import (
	"testing"

	"easytv-api/router"

	"github.com/gin-gonic/gin"
)

// 路由注册冲突（静态段与参数段混用等）在 gin 里是运行期 panic，这里在测试里提前暴露。
func TestRoutesRegisterWithoutPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("route registration panicked: %v", r)
		}
	}()
	engine := gin.New()
	router.SetApiRouter(engine)
	router.SetAdminRouter(engine)
	if len(engine.Routes()) == 0 {
		t.Fatal("no routes registered")
	}
}
