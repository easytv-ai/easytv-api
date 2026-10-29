package tests

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-redis/redis/v8"
)

// ===== 对话式工作流生成的 HTTP 层（agent-contract 第 8 节 / 验收 9）=====
//
// 全部走 sqlmock + httptest：不连库、不连 Redis（限流用进程内的假 Redis 服务）、不写任何真实数据。

func agentAuthRow(userId int, token string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "username", "role", "status", "group", "access_token"}).
		AddRow(userId, fmt.Sprintf("agent%d@easytv.ai", userId), 1, 1, "default", token)
}

func agentHeaders(userId int, token string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + token,
		"EasyTV-User":   strconv.Itoa(userId),
	}
}

// TestCreateCanvasAgentForeignCanvas 别人的画布：writableCanvas 拿不到画布 → 资源不存在。
func TestCreateCanvasAgentForeignCanvas(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "agent-token"))
	// 不是自己的画布 → GetUserCanvas 查不到 → 退回 GetCanvasById；画布没有项目 → 不可写
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id"}).AddRow(5, 999, 0))

	code, resp := do(t, r, "POST", "/user/canvas/5/agent", `{"question":"做一条橘猫广告"}`, agentHeaders(8, "agent-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("非本人画布必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); msg != model.ErrNotFound.Error() {
		t.Fatalf("message = %q, want %q", msg, model.ErrNotFound.Error())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestCreateCanvasAgentTeamViewer 团队空间里的查看者：能读画布但不能改。
func TestCreateCanvasAgentTeamViewer(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "agent-token"))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id"}).AddRow(5, 999, 7))
	mock.ExpectQuery("SELECT .* FROM `project` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "team_id"}).AddRow(7, 999, 3))
	mock.ExpectQuery("SELECT .* FROM `team_member` WHERE user_id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "team_id", "user_id", "role", "permission"}).
			AddRow(2, 3, 8, model.TeamRoleMember, model.TeamPermViewer))

	code, resp := do(t, r, "POST", "/user/canvas/5/agent", `{"question":"做一条橘猫广告"}`, agentHeaders(8, "agent-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("团队查看者必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); !strings.Contains(msg, "查看者") {
		t.Fatalf("message = %q，应提示是查看者", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestCreateCanvasAgentEmptyQuestion 自己的画布但问题为空：在碰 setting / 定价之前就拒掉。
func TestCreateCanvasAgentEmptyQuestion(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	// 运营开关默认开启（setting 走内存缓存，不查库）
	model.SetSettingCache(setting.AgentEnabled, "true")

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "agent-token"))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id"}).AddRow(5, 8, 0))

	code, resp := do(t, r, "POST", "/user/canvas/5/agent", `{"question":"   "}`, agentHeaders(8, "agent-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("空问题必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); msg != "请输入问题" {
		t.Fatalf("message = %q", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestGetCanvasAgentPlanOwnership 计划读取的归属校验：任务不是自己的 → 资源不存在。
func TestGetCanvasAgentPlanOwnership(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "agent-token"))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id"}).AddRow(5, 8, 0))
	// 任务属于别人（user_id=999）
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "canvas_id", "type", "status", "params", "result_text", "model", "task_no"}).
			AddRow(41, 999, 5, model.TaskTypeText, model.TaskStatusSucceeded,
				`{"agent":{"question":"做个片子","max_rounds":6}}`, `{"reply":"ok"}`, "gpt-4o-mini", "GT-041"))

	code, resp := do(t, r, "GET", "/user/canvas/5/agent/41", "", agentHeaders(8, "agent-token"))
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("别人的任务必须被拒：code=%d resp=%v", code, resp)
	}
	if msg := fmt.Sprint(resp["message"]); msg != model.ErrNotFound.Error() {
		t.Fatalf("message = %q", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestGetCanvasAgentPlanReadsSteps 计划读取：自己的任务能拿到 steps 与计划正文。
func TestGetCanvasAgentPlanReadsSteps(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(8, "agent-token"))
	mock.ExpectQuery("FROM `canvas`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "project_id"}).AddRow(5, 8, 0))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "canvas_id", "type", "status", "params", "result_text", "model", "task_no", "cost_points"}).
			AddRow(41, 8, 5, model.TaskTypeText, model.TaskStatusSucceeded,
				`{"agent":{"question":"做个片子","mode":"generate","max_rounds":6,"rounds_used":2}}`,
				`{"reply":"搭好了","round":2,"changes":[],"validate":{"ok":true,"errors":[],"warnings":[],"estimate":{"points":8,"items":[{"node_id":"n2","kind":"image","type":"image","model":"gpt-image-1","points":8}]}},"dropped":0}`,
				"gpt-4o-mini", "GT-041", 2))
	mock.ExpectQuery("SELECT .* FROM `canvas_agent_step` WHERE task_id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "task_id", "round", "tool", "result_summary", "ok", "error"}).
			AddRow(1, 41, 1, "list_templates", "命中 2 条", true, "").
			AddRow(2, 41, 2, "plan", "ok=true errors=0", true, ""))

	code, resp := do(t, r, "GET", "/user/canvas/5/agent/41", "", agentHeaders(8, "agent-token"))
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("自己的任务应可读：code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("data = %v", resp["data"])
	}
	if data["question"] != "做个片子" || data["reply"] != "搭好了" || data["mode"] != "generate" {
		t.Fatalf("计划字段不对：%v", data)
	}
	if data["estimated_points"].(float64) != 8 {
		t.Fatalf("estimated_points = %v", data["estimated_points"])
	}
	steps, _ := data["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("steps = %v", data["steps"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAgentRoutesRegistered 三个新路由都注册在登录态后面。
func TestAgentRoutesRegistered(t *testing.T) {
	initMockDB(t)
	r := initTestRouter()
	want := []string{
		"POST /user/canvas/:id/agent",
		"GET /user/canvas/:id/agent/:task_id",
		"POST /user/canvas/:id/agent/:task_id/retry",
	}
	got := map[string]bool{}
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("路由未注册：%s", w)
		}
	}
}

// ===== 限流 =====

// TestCreateCanvasAgentRateLimited 第 31 次请求必须被 UserCriticalRateLimit("task") 挡住。
// 限流走 Redis 滑动窗口，测试里用进程内的假 Redis（只认 incr/set），不连任何外部服务。
func TestCreateCanvasAgentRateLimited(t *testing.T) {
	addr := startFakeRedis(t)
	old := common.RedisClient
	common.RedisClient = redis.NewClient(&redis.Options{Addr: addr})
	defer func() {
		_ = common.RedisClient.Close()
		common.RedisClient = old
	}()

	mock := initMockDB(t)
	r := initTestRouter()
	const userId, canvasId = 777001, 99
	headers := agentHeaders(userId, "rate-limit-token")

	blockedAt := 0
	for i := 0; i < 31; i++ {
		mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(agentAuthRow(userId, "rate-limit-token"))
		if i < 30 {
			// 画布不是自己的：两个查询后返回「资源不存在」，不会碰 setting / 定价
			mock.ExpectQuery("FROM `canvas`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectQuery("FROM `canvas`").WillReturnRows(
				sqlmock.NewRows([]string{"id", "user_id", "project_id"}).AddRow(canvasId, 999, 0))
		}
		code, resp := do(t, r, "POST", fmt.Sprintf("/user/canvas/%d/agent", canvasId),
			`{"question":"做一条橘猫广告"}`, headers)
		if code != http.StatusOK {
			t.Fatalf("第 %d 次请求 code=%d", i+1, code)
		}
		msg := fmt.Sprint(resp["message"])
		if strings.Contains(msg, "过于频繁") {
			blockedAt = i + 1
			break
		}
		if resp["success"] != false {
			t.Fatalf("第 %d 次请求应走到画布权限校验：%v", i+1, resp)
		}
	}
	if blockedAt != 31 {
		t.Fatalf("应在第 31 次触发限流，实际 %d", blockedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// ===== 进程内假 Redis（RESP 协议最小实现）=====

type fakeRedisServer struct {
	ln   net.Listener
	mu   sync.Mutex
	vals map[string]int64
}

func startFakeRedis(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeRedisServer{ln: ln, vals: map[string]int64{}}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func (s *fakeRedisServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeRedisServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		args, err := readRESPArgs(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		switch strings.ToLower(args[0]) {
		case "incr":
			s.mu.Lock()
			s.vals[args[1]]++
			n := s.vals[args[1]]
			s.mu.Unlock()
			_, _ = fmt.Fprintf(w, ":%d\r\n", n)
		case "ping":
			_, _ = w.WriteString("+PONG\r\n")
		case "get":
			_, _ = w.WriteString("$-1\r\n")
		default:
			_, _ = w.WriteString("+OK\r\n")
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

func readRESPArgs(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" || line[0] != '*' {
		return nil, nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		head, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		head = strings.TrimRight(head, "\r\n")
		if head == "" || head[0] != '$' {
			return nil, nil
		}
		size, err := strconv.Atoi(head[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}
