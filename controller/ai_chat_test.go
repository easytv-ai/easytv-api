package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// ===== 客户端自带 params.agent.free 的闸门（复核发现 high：白嫖免积分对话）=====

// runChatFreeGuard 在最小 gin 引擎上挂 GuardClientChatFree + 探针 handler，
// 返回：响应、探针是否执行、探针读到的请求体（应为原始请求体）。
func runChatFreeGuard(t *testing.T, body string) (*httptest.ResponseRecorder, bool, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	reached, got := false, ""
	r.POST("/task", GuardClientChatFree(), func(c *gin.Context) {
		reached = true
		b, _ := io.ReadAll(c.Request.Body)
		got = string(b)
		c.String(http.StatusOK, "ok")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w, reached, got
}

// TestGuardClientChatFreeRejectsClientFree 客户端指定 params.agent.free（true 或 false）一律拒绝，不进 CreateTask。
func TestGuardClientChatFreeRejectsClientFree(t *testing.T) {
	cases := []string{
		// 复核发现里的原始攻击体：自带 free=true 想走免积分分支
		`{"type":"text","model":"gpt-4o-mini","prompt":"写一段 500 字的营销文案","params":{"agent":{"question":"写一段 500 字的营销文案","mode":"generate","max_rounds":3,"free":true}}}`,
		`{"params":{"agent":{"free":false}}}`,
		`{"params":{"agent":{"free":null}}}`,
		`{"params":{"chat":{"kind":"director","free":true}}}`,
	}
	for _, body := range cases {
		w, reached, _ := runChatFreeGuard(t, body)
		if reached {
			t.Fatalf("带 params.agent.free 的请求不该进入 CreateTask：%s", body)
		}
		resp := w.Body.String()
		if !strings.Contains(resp, `"success":false`) || (!strings.Contains(resp, "agent.free") && !strings.Contains(resp, "chat.free")) {
			t.Fatalf("应返回说明 free 由服务端管理的错误，得到 %s", resp)
		}
	}
}

// TestGuardClientChatFreePassesLegitRequests 不带 agent.free 的请求原样放行，且请求体不能被消费 / 改写。
func TestGuardClientChatFreePassesLegitRequests(t *testing.T) {
	cases := []string{
		`{"type":"text","model":"gpt-4o-mini","prompt":"你好","params":{"count":1}}`, // 普通文本任务
		`{"type":"image","model":"seedream","prompt":"画图","params":{"ratio":"16:9"}}`,
		`{"type":"text","model":"gpt-4o-mini","prompt":"你好"}`, // 没有 params
		`{"params":{}}`,
		`{"params":{"agent":{"question":"只问不改","max_rounds":2}}}`, // 合法 agent 参数（free 由服务端写）
		`not json`,
		``,
	}
	for _, body := range cases {
		w, reached, got := runChatFreeGuard(t, body)
		if !reached {
			t.Fatalf("合法请求被拦下：%s（%s）", body, w.Body.String())
		}
		if got != body {
			t.Fatalf("请求体被消费或改写：got %q want %q", got, body)
		}
	}
}

func TestRespondChatQuotaExceeded(t *testing.T) {
	model.SetSettingCache(setting.ChatDailyFreeCount, "37")
	t.Cleanup(func() { model.SetSettingCache(setting.ChatDailyFreeCount, "") })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/quota", respondChatQuotaExceeded)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/quota", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d", w.Code)
	}
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Reason     string `json:"reason"`
			DailyLimit int    `json:"daily_limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Success || resp.Data.Reason != service.ChatQuotaExceededReason || resp.Data.DailyLimit != 37 {
		t.Fatalf("响应不对：%s", w.Body.String())
	}
	if !strings.Contains(resp.Message, "37") || !strings.Contains(resp.Message, "今日 AI 对话次数已用完") {
		t.Fatalf("中文 message 应保留次数：%q", resp.Message)
	}
}
