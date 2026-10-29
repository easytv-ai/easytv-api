package providers

import (
	"strings"
	"testing"
)

// 定价表里有、网关令牌没开通的模型：上游 404 翻成人话，状态码仍保留给轮询用
func TestGatewayCallErrorDeploymentMissing(t *testing.T) {
	err := gatewayCallError(404, "/v1/audio/transcriptions", "The API deployment for this resource does not exist. If you created the deployment within the last 5 minutes, please wait a moment and try again.")
	if !isGatewayNotFound(err) {
		t.Fatalf("应保留 404 状态：%v", err)
	}
	if !strings.Contains(err.Error(), "没有给这个模型开通服务") {
		t.Fatalf("应翻成人话：%v", err)
	}
	// 普通 404（任务不存在）不受影响
	if got := gatewayCallError(404, "/v1/videos/x", "任务不存在").Error(); got != "网关返回 404：任务不存在" {
		t.Fatalf("普通 404 文案变了：%s", got)
	}
}

// Kling 异步失败的风控拒绝也走人话（friendlyGatewayError(0, …) 是异步失败的入口）
func TestFriendlyGatewayErrorRiskControl(t *testing.T) {
	got := friendlyGatewayError(0, "task failed with status: FAIL, message: Failure to pass the risk control system")
	if !strings.Contains(got, "风控") {
		t.Fatalf("风控拒绝应翻成人话：%q", got)
	}
}
