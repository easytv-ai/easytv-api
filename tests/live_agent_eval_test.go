package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"easytv-api/service"
	"easytv-api/service/providers"
	"easytv-api/tests/agent_eval"
)

// 编译期锁：生产 provider 必须满足多轮循环要的文本调用能力（契约 2.6）。
var _ service.AgentChatter = (*providers.EasyAPIProvider)(nil)

// TestAgentEvalLive 真实模型评测（工作流按 `go test ./tests/ -run TestAgentEvalLive -v` 调）。
//
// 门禁（契约 10.4）：显式开关 + 网关地址 + 网关 Key + 模型清单 + 积分预算，五项齐备才真跑。
// 判定只读环境变量与 .env 文本——**不连库、不调 setting 读取函数、不试图补齐配置**。
// 缺任何一项：t.Skip，并且仍然把 live=false 与「缺什么」写进 golang-report/agent-eval.json；
// 绝不能因为缺配置让测试失败。
//
// 真跑时用的是纯函数 service.RunAgentRounds：不连库、不扣积分、不提交生成任务。
// 可执行轴是纯静态判定（AGENT_EVAL_EXECUTE=1 打开），永不提交真实任务（契约 10.2 / 12.3）。
func TestAgentEvalLive(t *testing.T) {
	root := agent_eval.RepoRoot()
	cfg := agent_eval.DetectLiveFromEnv(root)

	// ===== 离线路径：缺配置也要留下结果文件 =====
	if !cfg.Live {
		reason := cfg.Reason()
		if len(cfg.Notes) > 0 {
			reason += "；" + strings.Join(cfg.Notes, "；")
		}
		if root == "" {
			reason += "；找不到仓库根（go.mod），结果未落盘"
		}
		summary := agent_eval.NewSummary(false, reason, nil)
		if root != "" {
			if err := agent_eval.EnsureDirs(root); err != nil {
				reason += "；建目录失败：" + err.Error()
			}
			path, err := agent_eval.WriteSummary(root, summary)
			if err != nil {
				reason += "；落盘失败：" + err.Error()
			} else {
				t.Logf("离线结果已落盘：%s（%s）", path, summary.Stats)
			}
		}
		t.Skip(reason)
	}

	// ===== live 路径 =====
	if root == "" {
		t.Fatalf("配置齐备但找不到仓库根（go.mod），结果无处落盘")
	}
	summary := runAgentEvalLive(t, root, cfg)
	t.Logf("真实模型评测：%s", summary.Stats)
	if summary.Reason != "" {
		t.Logf("说明：%s", summary.Reason)
	}
}

// TestAgentEvalFakeGatewayLivePath 用本地假网关把 live 分支整条跑通：
// 配置判定 → providers.Chat（真 HTTP，但只打本地 httptest）→ RunAgentRounds → 打分 → 落盘。
// 不联网、不烧 token、不碰数据库；写盘落在 t.TempDir()，不会覆盖仓库里的真实评测结果。
func TestAgentEvalFakeGatewayLivePath(t *testing.T) {
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
			http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
			return
		}
		prompt := body.Messages[0].Content
		reply := `{"reply":"这次不改画布","changes":[]}`
		for _, c := range agent_eval.Cases() {
			if !strings.Contains(prompt, c.Brief) {
				continue
			}
			if fixed, ok := agent_eval.MockReplyForCase(c); ok {
				reply = fixed
			}
			break
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	t.Setenv(agent_eval.EnvLive, "1")
	t.Setenv(agent_eval.EnvBaseURL, server.URL)
	t.Setenv(agent_eval.EnvGatewayKey, "sk-fake-gateway")
	// 清单必须覆盖 mock 图里用到的每一类模型：少了 audio，带配音的用例会被判 MODEL_NOT_FOUND
	// （关键错误 → 直接 0 分），这正是评测台要暴露的那类问题。
	t.Setenv(agent_eval.EnvModels, `{"text":[{"model":"gpt-4o-mini","type":"text","unit":"count","points":1}],`+
		`"image":[{"model":"gpt-image-1","type":"image","unit":"image","points":8}],`+
		`"video":[{"model":"kling-v1","type":"video","unit":"second","points":12,"durations":[5,10]}],`+
		`"audio":[{"model":"tts-1","type":"audio","unit":"call","points":2}]}`)
	t.Setenv(agent_eval.EnvBudget, "100000")

	root := t.TempDir()
	cfg := agent_eval.DetectLiveFromEnv(root)
	if !cfg.Live {
		t.Fatalf("五项齐备时必须判定为可跑，reason=%q", cfg.Reason())
	}
	summary := runAgentEvalLive(t, root, cfg)

	if !summary.Live {
		t.Fatalf("live 跑法必须写 live=true，got %+v", summary)
	}
	if len(summary.Cases) != len(agent_eval.Cases()) {
		t.Fatalf("应逐条跑完 %d 条用例，got %d", len(agent_eval.Cases()), len(summary.Cases))
	}
	// 假网关按 brief 返回「一个合格模型」的固定输出，所以结构与 mock 自测逐条相同，
	// 只有 stats 末尾的 live 标记不同。
	wantStats := strings.Replace(mockStats, "live=false", "live=true", 1)
	if summary.Stats != wantStats {
		t.Errorf("stats 与手算不符：\n got %s\nwant %s", summary.Stats, wantStats)
	}
	for _, item := range summary.Cases {
		if !item.SchemaOk || item.Structure < 0.5 || item.Rounds != 1 || item.Repaired {
			t.Errorf("用例 %s 结果异常：%+v", item.Id, item)
		}
		if item.Execution != agent_eval.ExecSkipped {
			t.Errorf("用例 %s：可执行轴默认应 skipped，got %q", item.Id, item.Execution)
		}
	}
	if hits < int64(len(summary.Cases)) {
		t.Errorf("每条用例至少一次文本调用，got %d 次", hits)
	}

	// 结果 JSON 落在指定路径，字段严格
	body, err := os.ReadFile(agent_eval.ReportPath(root))
	if err != nil {
		t.Fatalf("读结果失败：%v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("结果不是合法 JSON：%v", err)
	}
	if live, _ := decoded["live"].(bool); !live {
		t.Errorf("结果里 live 应为 true：%s", string(body))
	}
	if reason, _ := decoded["reason"].(string); !strings.Contains(reason, "真实模型评测") {
		t.Errorf("live=true 时 reason 应说明这次怎么跑的，got %q", reason)
	}

	// 原始模型输出按用例逐条落盘
	for _, c := range agent_eval.Cases() {
		path := agent_eval.RawDir(root) + string(os.PathSeparator) + c.Id + ".txt"
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s 的原始输出失败：%v", c.Id, err)
		}
		if !strings.Contains(string(raw), "--- round 1 ---") {
			t.Errorf("%s 的原始输出缺少轮次分隔：%s", c.Id, string(raw))
		}
	}
}

// runAgentEvalLive 跑一遍 live 评测并落盘，返回汇总。
func runAgentEvalLive(t *testing.T, root string, cfg agent_eval.LiveConfig) agent_eval.Summary {
	t.Helper()
	chatter := providers.NewEasyAPIProvider(cfg.BaseURL, cfg.APIKey, nil)
	opts := agent_eval.ScoreOptions{Catalog: cfg.Catalog, Execute: cfg.Execute}
	cases := agent_eval.Cases()

	// 积分按「轮数 × 文本模型单价 × 换算系数」估算（只用于止损，不是真实扣费）
	unitPoints := cfg.PointUnit
	if hint, err := cfg.Catalog.Resolve("text", cfg.Model); err == nil {
		unitPoints = hint.Points * cfg.PointUnit
	}
	total := liveTotalTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()

	results := make([]agent_eval.CaseResult, 0, len(cases))
	var notes []string
	used, hardErrors := 0, 0
	stopped := ""
	for _, c := range cases {
		if ctx.Err() != nil {
			stopped = fmt.Sprintf("总时限 %s 用尽，剩余用例未跑", total)
			break
		}
		if used >= cfg.Budget {
			stopped = fmt.Sprintf("估算消耗已达积分预算 %d，剩余用例未跑", cfg.Budget)
			break
		}
		in := agent_eval.NewLoopInput(c, cfg.Catalog, cfg.Model, cfg.RoundsFor(c.Mode))
		outcome := agent_eval.RunCase(ctx, c, chatter, in, opts)
		if _, err := agent_eval.WriteRaw(root, c.Id, outcome.Raw); err != nil {
			notes = append(notes, "原始输出落盘失败："+err.Error())
		}
		if outcome.Err != nil {
			hardErrors++
			notes = append(notes, c.Id+" 循环硬错："+outcome.Err.Error())
		}
		t.Logf("%s 校验=%v 结构=%.2f 可执行=%s 轮数=%d 修复=%v",
			c.Id, outcome.Result.SchemaOk, outcome.Result.Structure,
			outcome.Result.Execution, outcome.Result.Rounds, outcome.Result.Repaired)
		results = append(results, outcome.Result)
		used += outcome.Result.Rounds * unitPoints
	}

	reason := fmt.Sprintf("真实模型评测：规划模型 %s，跑完 %d/%d 条，估算消耗 %d 积分（预算 %d）",
		cfg.Model, len(results), len(cases), used, cfg.Budget)
	if cfg.Execute {
		reason += "；可执行轴已开启（静态判定，不提交任务）"
	} else {
		reason += "；可执行轴未开启（AGENT_EVAL_EXECUTE=1 才开）"
	}
	if stopped != "" {
		reason += "；" + stopped
	}
	if hardErrors > 0 {
		reason += fmt.Sprintf("；%d 条用例循环报硬错（原始输出见 %s/）", hardErrors, agent_eval.RawDirName)
	}
	if len(notes) > 0 {
		reason += "；" + strings.Join(notes, "；")
	}

	summary := agent_eval.NewSummary(true, reason, results)
	if _, err := agent_eval.WriteSummary(root, summary); err != nil {
		t.Errorf("结果落盘失败：%v", err)
	}
	return summary
}

// liveTotalTimeout 整轮 live 评测的总时限（默认 45 分钟；超时只是止损，不算测试失败）。
func liveTotalTimeout() time.Duration {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_EVAL_TOTAL_TIMEOUT"))); err == nil && v > 0 {
		return time.Duration(v) * time.Second
	}
	return 45 * time.Minute
}
