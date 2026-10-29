package tests

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"easytv-api/controller"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// ===== 免积分标记只能由服务端写（复核发现 high / medium 的回归）=====
//
// 全部走 sqlmock + httptest（写法照 tests/agent_http_test.go / service/agent_billing_test.go）：
// 不连库、不迁移、不写真实数据。没有登记的 SQL 会让 sqlmock 直接报错，
// 因此「不入库 / 不估价 / 不提交网关」都靠 ExpectationsWereMet + 未登记期望来钉死。

// freeFlagProvider 只记提交次数，不触达网关；Models 供 FindProvider 命中。
type freeFlagProvider struct {
	name      string
	models    []service.ModelInfo
	submitted int
}

func (p *freeFlagProvider) Name() string { return p.name }

func (p *freeFlagProvider) Models() []service.ModelInfo { return p.models }

func (p *freeFlagProvider) Submit(*model.GenerationTask) error {
	p.submitted++
	return nil
}

// registerFreeFlagProvider 注册一个文本模型 provider（大多数用例）。
func registerFreeFlagProvider(t *testing.T, modelName string) *freeFlagProvider {
	return registerFreeFlagProviderOfType(t, modelName, model.TaskTypeText)
}

// registerFreeFlagProviderOfType 注册一个指定任务类型的 provider，用例结束摘掉，避免污染其它用例。
func registerFreeFlagProviderOfType(t *testing.T, modelName, taskType string) *freeFlagProvider {
	t.Helper()
	p := &freeFlagProvider{
		name:   "freeflag-" + modelName,
		models: []service.ModelInfo{{Provider: "freeflag-" + modelName, Model: modelName, Type: taskType, Label: modelName}},
	}
	service.RegisterProvider(p)
	t.Cleanup(func() { service.RemoveProvider(p.Name()) })
	return p
}

// freeFlagNonMemberRows 非会员：membership 查不到记录。
func freeFlagNonMemberRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "status", "expires_at"})
}

// freeFlagPriceRow 已定价的文本模型：1 积分 / 轮。
func freeFlagPriceRow(modelName string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}).
		AddRow(1, modelName, model.TaskTypeText, modelName, "count", 1, 1, 1)
}

// freeFlagMediaPriceRow 带媒体能力位（upscale）的视频模型定价行：供媒体入口挑模型用。
func freeFlagMediaPriceRow(modelName string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status", "abilities"}).
		AddRow(1, modelName, model.TaskTypeVideo, modelName, "count", 2, 2, 1, "upscale")
}

// seedFreeFlagSetting 内存注入设置（不落库）；用完删掉，避免污染其它用例。
func seedFreeFlagSetting(t *testing.T, key, val string) {
	t.Helper()
	model.SetSettingCache(key, val)
	t.Cleanup(func() { model.SetSettingCache(key, "") })
}

// agentParamsOf 从响应 data 里取 params.agent。
func agentParamsOf(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	params, ok := data["params"].(map[string]any)
	if !ok {
		t.Fatalf("data.params = %#v", data["params"])
	}
	agent, ok := params["agent"].(map[string]any)
	if !ok {
		t.Fatalf("params.agent = %#v", params["agent"])
	}
	return agent
}

// TestCreateTaskRejectsClientFreeFlag 复核发现 high 的复现路径：登录用户自带 params.agent.free=true
// 打 POST /user/task，必须被拒——不估价、不入库、不提交网关。
// 只登记鉴权查询：一旦请求走到定价 / 入库 / 扣分，sqlmock 会因未预期调用报错。
func TestCreateTaskRejectsClientFreeFlag(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	p := registerFreeFlagProvider(t, "freeflag-create")

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-token"))

	body := `{"type":"text","model":"freeflag-create","prompt":"做一条橘猫广告","params":{"agent":{"question":"做一条橘猫广告","mode":"generate","max_rounds":3,"free":true}}}`
	code, resp := do(t, r, "POST", "/user/task", body, agentHeaders(8, "freeflag-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("自带 free=true 必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "画布对话") {
		t.Fatalf("message = %q，应说明 agent 任务只能走画布对话入口", msg)
	}
	if p.submitted != 0 {
		t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("被拒的请求不该再碰 DB（估价 / 入库 / 扣分）：%v", err)
	}
}

// TestCreateTaskHandlerRejectsAgentParams 直接挂 controller.CreateTask（绕过路由上的 GuardClientChatFree）：
// 带非空 params.agent 一律拒绝——free 缺失 / 为 false 也不行，agent 任务只能由画布接口创建。
// 未登记任何 DB 期望：拒绝必须发生在入库 / 估价之前。
func TestCreateTaskHandlerRejectsAgentParams(t *testing.T) {
	cases := map[string]string{
		"free=true":  `{"type":"text","model":"freeflag-direct","prompt":"做一条橘猫广告","params":{"agent":{"question":"做一条橘猫广告","mode":"generate","max_rounds":3,"free":true}}}`,
		"free 缺失":    `{"type":"text","model":"freeflag-direct","prompt":"做一条橘猫广告","params":{"agent":{"question":"做一条橘猫广告","max_rounds":3}}}`,
		"free=false": `{"type":"text","model":"freeflag-direct","prompt":"做一条橘猫广告","params":{"agent":{"question":"做一条橘猫广告","max_rounds":3,"free":false}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			mock := initMockDB(t)
			p := registerFreeFlagProvider(t, "freeflag-direct")

			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.POST("/task", func(c *gin.Context) { c.Set("id", 8) }, controller.CreateTask)

			code, resp := do(t, r, "POST", "/task", body, nil)
			if code != http.StatusOK || resp["success"] != false {
				t.Fatalf("带 params.agent 必须被拒：code=%d resp=%v", code, resp)
			}
			if msg := fmt.Sprint(resp["message"]); msg != "对话式工作流任务只能通过画布对话接口创建" {
				t.Fatalf("message = %q，want 画布入口提示", msg)
			}
			if p.submitted != 0 {
				t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("拒绝必须发生在任何 DB 操作之前：%v", err)
			}
		})
	}
}

// TestCreateTaskRejectsCaseVariantAgentParams 复核发现 medium：encoding/json 的结构体解码大小写不敏感
// （IsAgentTask / IsFreeChatTask / GuardClientChatFree 都是），入口闸门必须同语义——否则
// {"Agent":…} / {"AGENT":…} 能经 POST /user/task 建出 agent 任务：计价按 1 轮、实际跑最多 8 轮，
// 还绕过 AgentEnabled 与画布入口校验。这里走真实路由（不带 free 时 GuardClientChatFree 会放行）。
func TestCreateTaskRejectsCaseVariantAgentParams(t *testing.T) {
	for name, agentKey := range map[string]string{"Agent": "Agent", "AGENT": "AGENT"} {
		t.Run(name, func(t *testing.T) {
			mock := initMockDB(t)
			r := initTestRouter()
			p := registerFreeFlagProvider(t, "freeflag-case")
			mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-case-token"))

			body := fmt.Sprintf(`{"type":"text","model":"freeflag-case","prompt":"做一条橘猫广告","params":{"%s":{"question":"做一条橘猫广告","max_rounds":8}}}`, agentKey)
			code, resp := do(t, r, "POST", "/user/task", body, agentHeaders(8, "freeflag-case-token"))
			if code != http.StatusOK || resp["success"] != false {
				t.Fatalf("大小写变体的 params.agent 必须被拒：code=%d resp=%v", code, resp)
			}
			if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "画布对话接口创建") {
				t.Fatalf("message = %q，应说明 agent 任务只能走画布对话入口", msg)
			}
			if p.submitted != 0 {
				t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("拒绝必须发生在估价 / 入库之前：%v", err)
			}
		})
	}
}

// TestCreateTaskHandlerRejectsCaseVariantFree 直接挂 CreateTask（绕过路由闸门）：Agent+Free 的大小写变体
// 也必须被入口 helper 拦住，不能只靠 GuardClientChatFree 的结构体解码兜底。
func TestCreateTaskHandlerRejectsCaseVariantFree(t *testing.T) {
	mock := initMockDB(t)
	p := registerFreeFlagProvider(t, "freeflag-case-free")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/task", func(c *gin.Context) { c.Set("id", 8) }, controller.CreateTask)

	body := `{"type":"text","model":"freeflag-case-free","prompt":"做一条橘猫广告","params":{"Agent":{"question":"做一条橘猫广告","max_rounds":8,"Free":true}}}`
	code, resp := do(t, r, "POST", "/task", body, nil)
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("大小写变体的 Agent+Free 必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); msg != "对话式工作流任务只能通过画布对话接口创建" {
		t.Fatalf("message = %q，want 画布入口提示", msg)
	}
	if p.submitted != 0 {
		t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝必须发生在任何 DB 操作之前：%v", err)
	}
}

// assertRetryReevaluatesFreeFlag 源任务 params 为 srcParams（含 agent 段），ChatFreeEnabled=false 时走通用重试：
// 断言新任务 free 被重估为 false、走付费估价路径（3 轮预扣 3 分）、其余 params 原样保留。
// 不登记任何 ai_chat_usage 期望：一旦走免积分分支，sqlmock 会报未预期调用。
func assertRetryReevaluatesFreeFlag(t *testing.T, srcParams string) {
	t.Helper()
	seedFreeFlagSetting(t, setting.ChatFreeEnabled, "false")
	mock := initMockDB(t)
	r := initTestRouter()
	p := registerFreeFlagProvider(t, "freeflag-retry")
	// 成功路径会异步写审计日志：用例结束前排空，避免写协程带着本用例的 sqlmock DB 泄漏到下一个用例。
	t.Cleanup(func() { model.FlushAuditLogs(2 * time.Second) })

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-retry-token"))
	// 源任务：已失败的免积分对话任务（free=true）
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\? AND user_id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "type", "model", "prompt", "params", "inputs", "status", "task_no"}).
			AddRow(41, 8, model.TaskTypeText, "freeflag-retry", "做一条橘猫广告", srcParams, "[]", model.TaskStatusFailed, "GT-041"))
	// 付费路径：并发校验 → 会员价 → 估价（3 轮 → 3 分）→ 入库 → 埋点 → 预扣 3 分
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(freeFlagNonMemberRows())
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `generation_task`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(freeFlagNonMemberRows())
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(freeFlagPriceRow("freeflag-retry"))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `generation_task`").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `track_event`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `point_lot`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "remaining", "expires_at"}).AddRow(7, 8, 100, time.Now().Add(24*time.Hour).Unix()))
	mock.ExpectExec("UPDATE `point_lot` SET `remaining`=remaining - \\?").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(97))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\?,`points_frozen`=\\?").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()

	code, resp := do(t, r, "POST", "/user/task/41/retry", "", agentHeaders(8, "freeflag-retry-token"))
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("开关关闭时重试旧的免积分任务应走付费路径：code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("data = %v", resp["data"])
	}
	agent := agentParamsOf(t, data)
	if got := agent["free"]; got != false {
		t.Fatalf("重试后的 free 必须按当前开关重估为 false，得到 %v（params=%v）", got, data["params"])
	}
	if agent["question"] != "做一条橘猫广告" || agent["mode"] != "generate" || agent["max_rounds"].(float64) != 3 {
		t.Fatalf("除 free 外的 params 必须原样保留：%v", agent)
	}
	if data["points_frozen"].(float64) != 3 {
		t.Fatalf("重试应走付费估价路径（3 轮预扣 3 分），points_frozen=%v", data["points_frozen"])
	}
	if p.submitted != 1 {
		t.Fatalf("重试任务应提交网关 1 次，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet（开关关闭时不许碰 ai_chat_usage）: %v", err)
	}
}

// TestRetryAgentTaskReevaluatesFreeFlag 复核发现 medium 的复现路径：源任务 params.agent.free=true，
// ChatFreeEnabled=false 时走通用重试，新任务的 free 必须按当前开关重估为 false 并走付费估价路径（预扣积分），
// 而不是照抄旧值继续免单，也不是被拒绝（不打断用户在任务列表里的重试）。
func TestRetryAgentTaskReevaluatesFreeFlag(t *testing.T) {
	assertRetryReevaluatesFreeFlag(t,
		`{"agent":{"question":"做一条橘猫广告","mode":"generate","max_rounds":3,"free":true}}`)
}

// TestRetryCaseVariantAgentFreeReevaluated 复核发现 medium 的大小写变体：{"Agent":…,"Free":true} 在
// encoding/json 的结构体解码里同样被 IsFreeChatTask 认成免费任务，重估必须用同一套大小写不敏感语义，
// 否则开关关闭后重试它仍然免单（且不会按 agent.max_rounds 计价）。
func TestRetryCaseVariantAgentFreeReevaluated(t *testing.T) {
	assertRetryReevaluatesFreeFlag(t,
		`{"Agent":{"question":"做一条橘猫广告","mode":"generate","max_rounds":3,"Free":true}}`)
}

// TestRewriteChatFreeFlag 重估只改 free：其余键原样保留；没有 agent 段 / 非 JSON 时原样返回。
func TestRewriteChatFreeFlag(t *testing.T) {
	src := `{"agent":{"question":"做个片子","max_rounds":3,"free":true},"count":1}`
	var p struct {
		Agent struct {
			Question  string `json:"question"`
			MaxRounds int    `json:"max_rounds"`
			Free      bool   `json:"free"`
		} `json:"agent"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(service.RewriteChatFreeFlag(src, false)), &p); err != nil {
		t.Fatalf("重估后的 params 不是合法 JSON：%v", err)
	}
	if p.Agent.Free || p.Agent.Question != "做个片子" || p.Agent.MaxRounds != 3 || p.Count != 1 {
		t.Fatalf("只允许改 free，其余原样保留：%s", service.RewriteChatFreeFlag(src, false))
	}
	if again := service.RewriteChatFreeFlag(src, true); !strings.Contains(again, `"free":true`) {
		t.Fatalf("开关开启时应写回 true：%s", again)
	}
	if plain := service.RewriteChatFreeFlag(`{"count":1}`, false); plain != `{"count":1}` {
		t.Fatalf("没有 agent 段必须原样返回：%s", plain)
	}
	if bad := service.RewriteChatFreeFlag(`not json`, false); bad != `not json` {
		t.Fatalf("非 JSON 原样返回：%s", bad)
	}
	// 大小写变体（复核发现 medium）：IsFreeChatTask 的结构体解码认 {"Agent":…,"Free":true}，
	// 重估必须同语义，并把键统一成规范小写，避免新旧两份 agent 段并存。
	rewritten := service.RewriteChatFreeFlag(`{"Agent":{"question":"做个片子","max_rounds":3,"Free":true}}`, false)
	if strings.Contains(rewritten, `"Agent"`) || strings.Contains(rewritten, `"Free"`) {
		t.Fatalf("大小写变体应统一成规范小写键：%s", rewritten)
	}
	if !strings.Contains(rewritten, `"agent"`) || !strings.Contains(rewritten, `"free":false`) {
		t.Fatalf("大小写变体的 free 也必须按开关重估：%s", rewritten)
	}
}

// TestCanvasAgentTaskIsFreeAndCountsOnce 开关开启时画布接口建出的任务：免费、points_frozen=0、
// 每日次数只扣一次（不写 point_lot / point_log）。走真实路由 POST /user/canvas/:id/agent。
func TestCanvasAgentTaskIsFreeAndCountsOnce(t *testing.T) {
	seedFreeFlagSetting(t, setting.AgentEnabled, "true")
	seedFreeFlagSetting(t, setting.ChatFreeEnabled, "true")
	seedFreeFlagSetting(t, setting.ChatDailyFreeCount, "2")
	seedFreeFlagSetting(t, setting.AgentModel, "freeflag-canvas")
	seedFreeFlagSetting(t, setting.ChatKnowledge, " ") // 非空缓存值，避免回查 setting 表
	mock := initMockDB(t)
	r := initTestRouter()
	p := registerFreeFlagProvider(t, "freeflag-canvas")
	// 成功路径会异步写审计日志：用例结束前排空，避免写协程带着本用例的 sqlmock DB 泄漏到下一个用例。
	t.Cleanup(func() { model.FlushAuditLogs(2 * time.Second) })

	// 1. 鉴权 + 2. 自己的画布（个人项目）
	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-canvas-token"))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id"}).AddRow(5, 8, 0))
	// 3. NewAgentTask：后台配置的对话模型（setting.AgentModel → 查定价）
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(freeFlagPriceRow("freeflag-canvas"))
	// 4. 提示词里的模型目录
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE status = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}))
	// 5. 【平台事实】：今日配额（非会员 → 读已用次数）+ 积分余额
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(freeFlagNonMemberRows())
	mock.ExpectQuery("SELECT count FROM ai_chat_usage WHERE user_id = \\? AND day = \\?").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(12))
	// 6. StartTask：并发校验 → 免积分分支只消费一次每日次数（limit=2）→ 入库 → 埋点 → 提交
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(freeFlagNonMemberRows())
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `generation_task`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(freeFlagNonMemberRows())
	mock.ExpectExec("INSERT INTO ai_chat_usage .*ON DUPLICATE KEY UPDATE count = IF\\(\\? <= 0 OR count < \\?, count \\+ 1, count\\)").
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT count FROM ai_chat_usage WHERE user_id = \\? AND day = \\?").WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `generation_task`").WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `track_event`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	// 不登记 point_lot / point_log：一旦走计费分支，sqlmock 会报未预期调用。

	code, resp := do(t, r, "POST", "/user/canvas/5/agent", `{"question":"做一条橘猫广告","max_rounds":3}`, agentHeaders(8, "freeflag-canvas-token"))
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("画布对话建任务失败：code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("data = %v", resp["data"])
	}
	if got := agentParamsOf(t, data)["free"]; got != true {
		t.Fatalf("开关开启时画布任务应免费：free=%v（params=%v）", got, data["params"])
	}
	if data["points_frozen"].(float64) != 0 {
		t.Fatalf("免积分任务 points_frozen 必须为 0，得到 %v", data["points_frozen"])
	}
	if p.submitted != 1 {
		t.Fatalf("应提交网关 1 次，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet（每日次数只允许消费一次，且不许碰 point_lot/point_log）: %v", err)
	}
}

// TestCreateBatchRejectsClientAgentParams 复核发现 high 的同类旁路：批量编排
// POST /user/canvas/:id/batch 的条目 params 由客户端原样透传（service/batch.go:92-99 → :390），
// worker 再经 batchSubmitter（:291-297）调 StartTask——不挡的话 params.agent.free=true 会直接命中
// 免积分分支（service/task_start.go:35-36 → :87-97），ChatFreeEnabled=false 时也免单。
// 批量建批次必须在入口拒绝；只登记到拒绝点为止的查询，继续估价 / 入库会因未预期调用报错。
func TestCreateBatchRejectsClientAgentParams(t *testing.T) {
	seedFreeFlagSetting(t, setting.ChatFreeEnabled, "false") // 开关关闭也必须挡住
	mock := initMockDB(t)
	r := initTestRouter()
	registerFreeFlagProvider(t, "freeflag-batch")

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-batch-token"))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id", "graph"}).
			AddRow(5, 8, 0, `{"nodes":[{"id":"n1"}],"edges":[]}`))
	mock.ExpectQuery("SELECT .* FROM `generation_batch`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	body := `{"items":[{"node_id":"n1","type":"text","model":"freeflag-batch","prompt":"做一条橘猫广告",` +
		`"params":{"agent":{"question":"做一条橘猫广告","mode":"generate","max_rounds":3,"free":true}}}]}`
	code, resp := do(t, r, "POST", "/user/canvas/5/batch", body, agentHeaders(8, "freeflag-batch-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("批量条目自带 params.agent 必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "画布对话接口创建") || !strings.Contains(msg, "n1") {
		t.Fatalf("message = %q，应说明 agent 任务只能走画布对话入口", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝必须发生在估价 / 入库之前：%v", err)
	}
}

// TestCreateBatchRejectsCaseVariantAgentFree 复核发现 medium 的大小写变体也适用于批量入口：
// {"Agent":{"Free":true}} 会被 IsFreeChatTask 的结构体解码认成免费任务，批量条目不能只挡小写 agent 键，
// 否则开关关闭时仍能经批量 worker → StartTask 免单。
func TestCreateBatchRejectsCaseVariantAgentFree(t *testing.T) {
	seedFreeFlagSetting(t, setting.ChatFreeEnabled, "false")
	mock := initMockDB(t)
	r := initTestRouter()
	registerFreeFlagProvider(t, "freeflag-batch-case")

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-batch-case-token"))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id", "graph"}).
			AddRow(5, 8, 0, `{"nodes":[{"id":"n1"}],"edges":[]}`))
	mock.ExpectQuery("SELECT .* FROM `generation_batch`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	body := `{"items":[{"node_id":"n1","type":"text","model":"freeflag-batch-case","prompt":"做一条橘猫广告",` +
		`"params":{"Agent":{"question":"做一条橘猫广告","max_rounds":8,"Free":true}}}]}`
	code, resp := do(t, r, "POST", "/user/canvas/5/batch", body, agentHeaders(8, "freeflag-batch-case-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("批量条目的大小写变体 Agent+Free 必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "画布对话接口创建") || !strings.Contains(msg, "n1") {
		t.Fatalf("message = %q，应说明 agent 任务只能走画布对话入口", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝必须发生在估价 / 入库之前：%v", err)
	}
}

// TestCreateMediaJobRejectsClientAgentParams 复核发现 low 的覆盖缺口：媒体模型动作的 params 同样是
// 客户端透传（service/media.go:154-159），必须与通用建任务 / 批量入口同一口径拒绝 params.agent，
// 否则模型目录变化（text 模型被打上媒体能力位）后会变成免积分旁路。
// 只登记到拒绝点为止的查询：继续提交任务会因未预期调用报错。
func TestCreateMediaJobRejectsClientAgentParams(t *testing.T) {
	seedFreeFlagSetting(t, setting.MediaActionsDisabled, " ") // 非空缓存值：没有动作被关闭，且不回查 setting 表
	mock := initMockDB(t)
	r := initTestRouter()
	p := registerFreeFlagProviderOfType(t, "freeflag-media", model.TaskTypeVideo)

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-media-token"))
	mock.ExpectQuery("SELECT .* FROM `asset` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "team_id", "type", "project_id"}).AddRow(77, 8, 0, model.AssetTypeVideo, 0))
	// 能力模型目录 + 单模型定价（video + upscale 能力位）
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE status = \\?").WillReturnRows(freeFlagMediaPriceRow("freeflag-media"))
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(freeFlagMediaPriceRow("freeflag-media"))

	body := `{"action":"upscale","asset_id":77,"model":"freeflag-media",` +
		`"params":{"resolution":"1080p","agent":{"question":"做一条橘猫广告","max_rounds":8,"free":true}}}`
	code, resp := do(t, r, "POST", "/user/media/jobs", body, agentHeaders(8, "freeflag-media-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("媒体动作自带 params.agent 必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "画布对话接口创建") {
		t.Fatalf("message = %q，应说明 agent 任务只能走画布对话入口", msg)
	}
	if p.submitted != 0 {
		t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝必须发生在提交任务 / 落作业之前：%v", err)
	}
}

// TestCheckNoClientAgentParams 闸门语义：非空 agent 对象一律拦（free=true/false 都算客户端自报），
// 且与 encoding/json 的结构体解码同语义——Agent / AGENT 等大小写变体同样拦；
// 空对象 / null / 没有 agent / 非 JSON 不拦，交给各入口自己的参数校验。
func TestCheckNoClientAgentParams(t *testing.T) {
	blocked := []string{
		`{"agent":{"free":true}}`,
		`{"agent":{"question":"做个片子","max_rounds":3,"free":true}}`,
		`{"count":1,"agent":{"free":false}}`,
		`{"Agent":{"question":"做个片子","max_rounds":8}}`,          // 大小写变体：不拦就会被计价按 1 轮、实际跑 8 轮
		`{"AGENT":{"free":true}}`,                               // 大小写变体：不拦就是免积分旁路
		`{"agent":{"Question":"x","Max_Rounds":8,"Free":true}}`, // 内层键大小写变体同样被结构体解码认出来
	}
	for _, params := range blocked {
		if err := service.CheckNoClientAgentParams(params); !errors.Is(err, service.ErrClientAgentParams) {
			t.Fatalf("应拒绝 %s，得到 %v", params, err)
		}
	}
	allowed := []string{``, `{}`, `{"count":1}`, `{"agent":{}}`, `{"Agent":{}}`, `{"agent":null}`, `not json`}
	for _, params := range allowed {
		if err := service.CheckNoClientAgentParams(params); err != nil {
			t.Fatalf("不该拒绝 %s，得到 %v", params, err)
		}
	}
}

func TestCreateTaskRejectsClientChatFreeFlag(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	p := registerFreeFlagProvider(t, "freeflag-director-create")

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-director-token"))

	body := `{"type":"text","model":"freeflag-director-create","prompt":"加一个角色","params":{"chat":{"kind":"director","free":true}}}`
	code, resp := do(t, r, "POST", "/user/task", body, agentHeaders(8, "freeflag-director-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("自带 chat.free 必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "chat.free") {
		t.Fatalf("message = %q，应说明 chat.free 由服务端管理", msg)
	}
	if p.submitted != 0 {
		t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("被拒的请求不该再碰 DB：%v", err)
	}
}

func TestCreateDirectorChatRejectsWhenDisabledOrEmpty(t *testing.T) {
	seedFreeFlagSetting(t, setting.AgentEnabled, "false")
	mock := initMockDB(t)
	r := initTestRouter()
	p := registerFreeFlagProvider(t, "freeflag-director-off")
	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-director-off-token"))

	code, resp := do(t, r, "POST", "/user/ai-chat", `{"model":"freeflag-director-off","prompt":"加一个角色"}`, agentHeaders(8, "freeflag-director-off-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("对话关掉必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "暂未开启") {
		t.Fatalf("message = %q", msg)
	}
	if p.submitted != 0 {
		t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝必须发生在估价 / 入库之前：%v", err)
	}

	seedFreeFlagSetting(t, setting.AgentEnabled, "true")
	mock = initMockDB(t)
	r = initTestRouter()
	p = registerFreeFlagProvider(t, "freeflag-director-empty")
	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "freeflag-director-empty-token"))
	code, resp = do(t, r, "POST", "/user/ai-chat", `{"model":"freeflag-director-empty","prompt":"   "}`, agentHeaders(8, "freeflag-director-empty-token"))
	if code != http.StatusOK || resp["success"] != false || fmt.Sprint(resp["message"]) != "请输入问题" {
		t.Fatalf("空提示必须被拒：code=%d resp=%v", code, resp)
	}
	if p.submitted != 0 {
		t.Fatalf("被拒的请求不该提交网关，实际 %d 次", p.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("拒绝必须发生在估价 / 入库之前：%v", err)
	}
}
