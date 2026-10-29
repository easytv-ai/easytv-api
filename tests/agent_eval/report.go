package agent_eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ===== 结果落盘（契约 10.3）=====
//
// ⚠️ golang-report/ 在仓库里不存在（没有任何代码创建过它），所以写文件之前必须先 MkdirAll，
// 而且**离线与 live 两条路都要写**：工作流直接读 golang-report/agent-eval.json，
// 读不到就把 reason 写成「读不到 golang-report/agent-eval.json」，整个评测门禁静默降级。

const (
	// ReportDir 结果目录（仓库根下）。
	ReportDir = "golang-report"
	// ReportJSON 结果文件名。
	ReportJSON = "agent-eval.json"
	// RawDirName 原始模型输出目录名。
	RawDirName = "agent-eval-raw"
)

// RepoRoot 从当前工作目录向上找 go.mod；找不到返回 ""。
// go test ./tests/ 的 CWD 是 tests/，所以不能假设 CWD 就是仓库根。
func RepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ReportPath 结果 JSON 路径；root 为空时返回 ""（调用方据此把「找不到仓库根」写进 reason）。
func ReportPath(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, ReportDir, ReportJSON)
}

// RawDir 原始输出目录；root 为空时返回 ""。
func RawDir(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, ReportDir, RawDirName)
}

// RawText 渲染一条用例的原始输出：每轮一段，段首一行 `--- round N ---`；失败时附 `ERROR: …`。
func RawText(rounds []RoundOutput) string {
	var b strings.Builder
	for _, r := range rounds {
		round := r.Round
		if round <= 0 {
			round = 1
		}
		fmt.Fprintf(&b, "--- round %d ---\n", round)
		if strings.TrimSpace(r.Raw) != "" {
			b.WriteString(r.Raw)
			if !strings.HasSuffix(r.Raw, "\n") {
				b.WriteString("\n")
			}
		}
		if r.Err != "" {
			fmt.Fprintf(&b, "ERROR: %s\n", r.Err)
		}
	}
	if b.Len() == 0 {
		b.WriteString("--- round 1 ---\n（没有拿到模型输出）\n")
	}
	return b.String()
}

// EnsureDirs 建好结果目录与原始输出目录（契约 10.3：两个目录都要建，且必须在写文件之前）。
// 离线路径也建：判读环节会去 agent-eval-raw 下读原始输出，目录不存在会让它报错。
func EnsureDirs(root string) error {
	if ReportPath(root) == "" {
		return fmt.Errorf("找不到仓库根（go.mod），无法定位 %s", ReportDir)
	}
	if err := os.MkdirAll(filepath.Dir(ReportPath(root)), 0o755); err != nil {
		return fmt.Errorf("创建 %s 目录失败：%w", ReportDir, err)
	}
	if err := os.MkdirAll(RawDir(root), 0o755); err != nil {
		return fmt.Errorf("创建 %s 目录失败：%w", RawDirName, err)
	}
	return nil
}

// WriteSummary 写结果 JSON（先建目录）。返回写入路径。
func WriteSummary(root string, s Summary) (string, error) {
	path := ReportPath(root)
	if path == "" {
		return "", fmt.Errorf("找不到仓库根（go.mod），无法定位 %s/%s", ReportDir, ReportJSON)
	}
	if err := EnsureDirs(root); err != nil {
		return "", err
	}
	if s.Cases == nil {
		s.Cases = []CaseResult{}
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化评测结果失败：%w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("写入 %s 失败：%w", path, err)
	}
	return path, nil
}

// WriteRaw 写一条用例的原始模型输出到 golang-report/agent-eval-raw/<用例id>.txt。
func WriteRaw(root, caseID string, rounds []RoundOutput) (string, error) {
	dir := RawDir(root)
	if dir == "" {
		return "", fmt.Errorf("找不到仓库根（go.mod），无法定位 %s/%s", ReportDir, RawDirName)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建 %s 目录失败：%w", dir, err)
	}
	path := filepath.Join(dir, caseID+".txt")
	if err := os.WriteFile(path, []byte(RawText(rounds)), 0o644); err != nil {
		return "", fmt.Errorf("写入 %s 失败：%w", path, err)
	}
	return path, nil
}

// ===== live 门禁（契约 10.4）=====
//
// 四项（网关地址 / 网关 Key / 模型清单 / 积分预算）齐备且显式打开开关才真跑。
// 判定**只读 .env 文本与 setting 约定的键名**：不连库、不调 setting 读取函数、不试图补齐配置。

// 环境变量名（契约 10.4 冻结）。
const (
	EnvLive          = "AGENT_EVAL_LIVE"
	EnvBaseURL       = "EASYAPI_BASE_URL"
	EnvGatewayKey    = "AGENT_EVAL_GATEWAY_KEY"
	EnvEasyAPIKey    = "EASYAPI_KEY"
	EnvModels        = "AGENT_EVAL_MODELS"
	EnvBudget        = "AGENT_EVAL_POINTS_BUDGET"
	EnvPointUnit     = "AGENT_EVAL_POINT_UNIT"
	EnvModel         = "AGENT_EVAL_MODEL"
	EnvMaxRounds     = "AGENT_EVAL_MAX_ROUNDS"
	EnvExecute       = "AGENT_EVAL_EXECUTE"
	DefaultPointUnit = 1
)

// 缺配置的 reason 文案（契约 10.4 表，逐字冻结）。
const (
	MissingLiveSwitch = "缺显式开关 AGENT_EVAL_LIVE=1"
	MissingBaseURL    = "缺网关地址（EASYAPI_BASE_URL）"
	MissingGatewayKey = "缺网关 Key（EASYAPI_KEY / AGENT_EVAL_GATEWAY_KEY）"
	MissingModels     = "缺模型清单（AGENT_EVAL_MODELS）"
	MissingModelsText = "缺模型清单里的 text 模型（AGENT_EVAL_MODELS）"
	MissingBudget     = "缺积分预算（AGENT_EVAL_POINTS_BUDGET）"
)

// LiveConfig live 跑法的全部外部配置。
type LiveConfig struct {
	Live      bool
	BaseURL   string
	APIKey    string
	Model     string // 规划用文本模型
	Catalog   *MockCatalog
	Budget    int // AGENT_EVAL_POINTS_BUDGET，估算消耗上限
	PointUnit int // AGENT_EVAL_POINT_UNIT，默认 1
	// 轮数上限：setting 的缺省值（契约 2.1），不读 setting 以避免连库。
	MaxRoundsGenerate int
	MaxRoundsPatch    int
	Execute           bool
	Missing           []string
	Notes             []string // 非阻塞的提示（例如 .env 读不到）
}

// Reason 缺什么配置（live=false 时写进结果 JSON 与 t.Skip 文案）。
func (c LiveConfig) Reason() string {
	if len(c.Missing) == 0 {
		return ""
	}
	return "真实模型评测跳过：" + strings.Join(c.Missing, "、")
}

// RoundsFor 该模式的轮数上限。
func (c LiveConfig) RoundsFor(mode string) int {
	if mode == "generate" {
		if c.MaxRoundsGenerate > 0 {
			return c.MaxRoundsGenerate
		}
		return DefaultMaxRoundsGenerate
	}
	if c.MaxRoundsPatch > 0 {
		return c.MaxRoundsPatch
	}
	return DefaultMaxRoundsPatch
}

// ReadDotEnv 只读地解析 .env 文本（不连库、不改文件）。读不到返回空 map。
func ReadDotEnv(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		value = strings.Trim(value, `"'`)
		out[key] = value
	}
	return out
}

// DetectLive 逐项检查 live 是否可用。env 一般传 os.Getenv；dotEnv 是 .env 的文本解析结果。
func DetectLive(env func(string) string, dotEnv map[string]string) LiveConfig {
	if env == nil {
		env = func(string) string { return "" }
	}
	if dotEnv == nil {
		dotEnv = map[string]string{}
	}
	cfg := LiveConfig{
		PointUnit:         DefaultPointUnit,
		MaxRoundsGenerate: DefaultMaxRoundsGenerate,
		MaxRoundsPatch:    DefaultMaxRoundsPatch,
	}
	pick := func(key string) string {
		if v := strings.TrimSpace(env(key)); v != "" {
			return v
		}
		return strings.TrimSpace(dotEnv[key])
	}

	// 1) 显式开关
	if strings.TrimSpace(env(EnvLive)) != "1" {
		cfg.Missing = append(cfg.Missing, MissingLiveSwitch)
	}
	// 2) 网关地址：环境变量优先，其次 .env 文本
	cfg.BaseURL = pick(EnvBaseURL)
	if cfg.BaseURL == "" {
		cfg.Missing = append(cfg.Missing, MissingBaseURL)
	}
	// 3) 网关 Key：AGENT_EVAL_GATEWAY_KEY → EASYAPI_KEY → .env 的 EASYAPI_KEY
	cfg.APIKey = strings.TrimSpace(env(EnvGatewayKey))
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(env(EnvEasyAPIKey))
	}
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(dotEnv[EnvEasyAPIKey])
	}
	if cfg.APIKey == "" {
		cfg.Missing = append(cfg.Missing, MissingGatewayKey)
	}
	// 4) 模型清单：声明式目录，替代线上 model_price
	rawModels := strings.TrimSpace(env(EnvModels))
	if rawModels == "" {
		cfg.Missing = append(cfg.Missing, MissingModels)
	} else if catalog, err := ParseModelHintsJSON(rawModels); err != nil {
		cfg.Missing = append(cfg.Missing, MissingModels+"："+err.Error())
	} else {
		cfg.Catalog = catalog
		if len(catalog.Models("text")) == 0 {
			cfg.Missing = append(cfg.Missing, MissingModelsText)
		} else {
			cfg.Model = strings.TrimSpace(env(EnvModel))
			if cfg.Model == "" {
				cfg.Model = catalog.Models("text")[0]
			}
		}
	}
	// 5) 积分预算
	if v, err := strconv.Atoi(strings.TrimSpace(env(EnvBudget))); err != nil || v <= 0 {
		cfg.Missing = append(cfg.Missing, MissingBudget)
	} else {
		cfg.Budget = v
	}
	// 可选：积分换算系数、轮数上限、可执行轴开关
	if v, err := strconv.Atoi(strings.TrimSpace(env(EnvPointUnit))); err == nil && v > 0 {
		cfg.PointUnit = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(env(EnvMaxRounds))); err == nil && v > 0 {
		cfg.MaxRoundsGenerate, cfg.MaxRoundsPatch = v, v
	}
	cfg.Execute = strings.TrimSpace(env(EnvExecute)) == "1"

	cfg.Live = len(cfg.Missing) == 0
	return cfg
}

// DetectLiveFromEnv 便利包装：读 os.Getenv 与仓库根下的 .env 文本。
// 返回的第二个值是 .env 是否真的读到了（读不到只是提示，不阻塞）。
func DetectLiveFromEnv(root string) LiveConfig {
	dotEnv := map[string]string{}
	read := false
	if root != "" {
		if info, err := os.Stat(filepath.Join(root, ".env")); err == nil && !info.IsDir() {
			dotEnv = ReadDotEnv(filepath.Join(root, ".env"))
			read = true
		}
	}
	cfg := DetectLive(os.Getenv, dotEnv)
	if !read {
		cfg.Notes = append(cfg.Notes, "读不到仓库根下的 .env 文本，只看了环境变量")
	}
	return cfg
}
