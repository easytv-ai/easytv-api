package tests

import (
	"strings"
	"testing"

	"easytv-api/service/providers"
)

// TestFriendlyGatewayError 已知网关报错要翻成能照着做的一句话，未知的保留原文。
func TestFriendlyGatewayError(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		want   string // 期望出现的关键词；空串表示应保留原文
	}{
		{400, `unsupported duration: 5`, "时长"},
		{400, `unsupported resolution: 720p`, "分辨率"},
		{502, `cloudsway returned HTTP 400: Azure OpenAI bad request: Inpaint image must match the requested width and height`, "首帧图"},
		{502, `cloudsway returned HTTP 500: Internal server error, please try again later`, "临时不可用"},
		{500, `boom`, "临时不可用"},
		{401, `invalid token`, "鉴权"},
		{402, `Pending payment`, "额度不足"},
		{429, `rate limited`, "排队"},
		{400, `your prompt was rejected by the content policy`, "内容审核"},
		{400, `something nobody mapped`, ""},
	}
	for _, c := range cases {
		got := providers.FriendlyGatewayErrorForTest(c.status, c.msg)
		if c.want == "" {
			if got != "" {
				t.Errorf("status=%d msg=%q 不该有映射，得到 %q", c.status, c.msg, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("status=%d msg=%q → %q，期望包含 %q", c.status, c.msg, got, c.want)
		}
	}
}

// TestFriendlyGatewayErrorPriority 具体原因优先于按状态码兜底：
// 502 包着「首帧尺寸不符」时要给首帧的提示，而不是笼统的「临时不可用」。
func TestFriendlyGatewayErrorPriority(t *testing.T) {
	got := providers.FriendlyGatewayErrorForTest(502, "cloudsway returned HTTP 400: Inpaint image must match the requested width and height")
	if strings.Contains(got, "临时不可用") {
		t.Fatalf("应给首帧尺寸的具体提示，得到 %q", got)
	}
	if !strings.Contains(got, "首帧图") {
		t.Fatalf("没提到首帧图: %q", got)
	}
}
