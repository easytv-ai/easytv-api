package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

// ===== 对话式工作流生成的 kind 注册表与模型目录（API.md 32，agent-contract 2.3）=====

// KindSchema 节点 kind 的权威定义。字段名与 service/template.go 的 nodeTaskType、前端 canvas-node-kinds.ts 对齐。
type KindSchema struct {
	Kind        string      `json:"kind"`
	TaskType    string      `json:"task_type"`    // 由 nodeTaskType 派生：text/image/video/audio；非生成节点为空
	Label       string      `json:"label"`        // 中文名，给模型看
	Creatable   bool        `json:"creatable"`    // 模型能否用 apply_graph 新建
	Promptable  bool        `json:"promptable"`   // 是否需要 prompt
	Required    []string    `json:"required"`     // data 必填键
	Optional    []string    `json:"optional"`     // data 可选键（白名单，其余键一律丢弃）
	Params      []ParamSpec `json:"params"`       // data.params 允许的键
	AcceptsFrom []string    `json:"accepts_from"` // 允许的上游 kind（语义连线校验用）
}

// ParamSpec data.params 里一个键的定义。
type ParamSpec struct {
	Key  string   `json:"key"`
	Type string   `json:"type"` // int / string / enum
	Enum []string `json:"enum,omitempty"`
	Min  int      `json:"min,omitempty"`
	Max  int      `json:"max,omitempty"`
	Note string   `json:"note,omitempty"`
}

// agentParamSpecs data.params 的全局白名单（2.3 表）。不在表里的键一律丢弃。
var agentParamSpecs = map[string]ParamSpec{
	"ratio": {Key: "ratio", Type: "enum", Enum: []string{
		"1:1", "1:2", "2:1", "9:16", "16:9", "3:4", "4:3", "3:2", "2:3", "5:4", "4:5", "21:9", "9:21",
	}},
	"clarity":    {Key: "clarity", Type: "enum", Enum: []string{"1K", "2K", "4K"}},
	"quality":    {Key: "quality", Type: "enum", Enum: []string{"low", "std", "high"}},
	"size":       {Key: "size", Type: "string", Note: "形如 1024x1536"},
	"duration":   {Key: "duration", Type: "int", Min: 1, Max: 600, Note: "必须命中所选模型支持的时长档位"},
	"resolution": {Key: "resolution", Type: "enum", Enum: []string{"480P", "720P", "1080P"}, Note: "必须命中所选模型支持的分辨率"},
	"mode":       {Key: "mode", Type: "enum", Enum: []string{"reference", "i2v", "fl2v", "talking"}},
}

// agentParamKeys 每种 kind 的 data.params 白名单（顺序固定，便于提示词与测试断言）。
func agentParamKeys(kind string) []string {
	switch kind {
	case "image", "panorama":
		return []string{"ratio", "clarity", "quality", "size"}
	case "video", "director":
		return []string{"ratio", "resolution", "duration", "mode"}
	}
	return nil
}

func agentParamsOf(kind string) []ParamSpec {
	keys := agentParamKeys(kind)
	if len(keys) == 0 {
		return nil
	}
	out := make([]ParamSpec, 0, len(keys))
	for _, k := range keys {
		if spec, ok := agentParamSpecs[k]; ok {
			out = append(out, spec)
		}
	}
	return out
}

// agentKindSchemas kind 注册表（冻结内容，顺序固定）。
// frameRole 在 image 的 optional 里、videoMode 在 video/director 的 optional 里：
// 它们是 FRAME_ORDER 与 VIDEO_NEEDS_FRAME 两条规则的唯一数据来源，漏了白名单会先把键丢掉。
var agentKindSchemas = []KindSchema{
	{Kind: "text", TaskType: model.TaskTypeText, Label: "文本", Creatable: true, Promptable: false,
		Required: nil, Optional: []string{"content", "label", "color", "fontSize"},
		Params: nil, AcceptsFrom: []string{"text"}},
	{Kind: "image", TaskType: model.TaskTypeImage, Label: "图片", Creatable: true, Promptable: true,
		Required: []string{"prompt", "model"}, Optional: []string{"label", "params", "frameRole"},
		Params: agentParamsOf("image"), AcceptsFrom: []string{"image", "text"}},
	{Kind: "panorama", TaskType: model.TaskTypeImage, Label: "全景图", Creatable: true, Promptable: true,
		Required: []string{"prompt", "model"}, Optional: []string{"label", "params"},
		Params: agentParamsOf("panorama"), AcceptsFrom: []string{"image", "text"}},
	{Kind: "video", TaskType: model.TaskTypeVideo, Label: "视频", Creatable: true, Promptable: true,
		Required: []string{"prompt", "model"}, Optional: []string{"label", "params", "videoMode"},
		Params: agentParamsOf("video"), AcceptsFrom: []string{"image", "video", "audio", "text"}},
	{Kind: "director", TaskType: model.TaskTypeVideo, Label: "3D 导演台", Creatable: true, Promptable: true,
		Required: []string{"prompt", "model"}, Optional: []string{"label", "params", "videoMode"},
		Params: agentParamsOf("director"), AcceptsFrom: []string{"image", "video", "audio", "text"}},
	{Kind: "audio", TaskType: model.TaskTypeAudio, Label: "音频", Creatable: true, Promptable: true,
		Required: []string{"prompt", "model"}, Optional: []string{"label", "params"},
		Params: agentParamsOf("audio"), AcceptsFrom: []string{"text", "audio"}},
	// 以下三个只能出现在已有画布上：模型不能新建，但画布上下文里可能有
	{Kind: "character", TaskType: model.TaskTypeImage, Label: "角色", Creatable: false, Promptable: true},
	{Kind: "scene", TaskType: model.TaskTypeImage, Label: "场景", Creatable: false, Promptable: true},
	{Kind: "edit", TaskType: model.TaskTypeImage, Label: "智能编辑", Creatable: false, Promptable: true},
}

var agentKindIndex = func() map[string]*KindSchema {
	m := make(map[string]*KindSchema, len(agentKindSchemas))
	for i := range agentKindSchemas {
		m[agentKindSchemas[i].Kind] = &agentKindSchemas[i]
	}
	return m
}()

// AgentKindSchemas 全部 kind，顺序固定（便于测试断言）。
func AgentKindSchemas() []KindSchema {
	out := make([]KindSchema, len(agentKindSchemas))
	copy(out, agentKindSchemas)
	return out
}

// AgentKindSchema 单个 kind 的定义；未收录返回 nil。
func AgentKindSchema(kind string) *KindSchema {
	return agentKindIndex[strings.TrimSpace(kind)]
}

// AgentCreatableKinds 模型可新建的 kind（提示词白名单 + UNKNOWN_KIND 判定依据）。
func AgentCreatableKinds() []string {
	out := make([]string, 0, len(agentKindSchemas))
	for _, s := range agentKindSchemas {
		if s.Creatable {
			out = append(out, s.Kind)
		}
	}
	return out
}

// AgentAllKinds 全部 kind（含只能出现在已有画布上的）。
func AgentAllKinds() []string {
	out := make([]string, 0, len(agentKindSchemas))
	for _, s := range agentKindSchemas {
		out = append(out, s.Kind)
	}
	return out
}

// AgentKindCreatable 该 kind 能否被模型新建。
func AgentKindCreatable(kind string) bool {
	s := AgentKindSchema(kind)
	return s != nil && s.Creatable
}

// AgentKindAcceptable 语义连线：sourceKind 能否接到 targetKind。
func AgentKindAcceptable(targetKind, sourceKind string) bool {
	t, s := AgentKindSchema(targetKind), AgentKindSchema(sourceKind)
	if t == nil || s == nil {
		return false
	}
	for _, k := range t.AcceptsFrom {
		if k == s.Kind {
			return true
		}
	}
	return false
}

// ===== 错误码与严重级别（agent-contract 4.1 / 4.2）=====

// 阻断级错误码（severity = "error"）。
const (
	AgentCodeBadGraph         = "BAD_GRAPH"
	AgentCodeDuplicateId      = "DUPLICATE_ID"
	AgentCodeUnknownKind      = "UNKNOWN_KIND"
	AgentCodeMissingRequired  = "MISSING_REQUIRED"
	AgentCodeModelNotFound    = "MODEL_NOT_FOUND"
	AgentCodeModelTypeMismatch = "MODEL_TYPE_MISMATCH"
	AgentCodeModelNotPriced   = "MODEL_NOT_PRICED"
	AgentCodeParamOutOfRange  = "PARAM_OUT_OF_RANGE"
	AgentCodeEdgeDangling     = "EDGE_DANGLING"
	AgentCodeSelfLoop         = "SELF_LOOP"
	AgentCodeDuplicateEdge    = "DUPLICATE_EDGE"
	AgentCodeCycle            = "CYCLE"
	AgentCodeVideoNeedsFrame  = "VIDEO_NEEDS_FRAME"
	AgentCodeRoleKindMismatch = "ROLE_KIND_MISMATCH"
	AgentCodeRefTokenLeft     = "REF_TOKEN_LEFT"
	AgentCodeAssetForbidden   = "ASSET_FORBIDDEN"
	AgentCodeTooManyChanges   = "TOO_MANY_CHANGES"
	AgentCodeTooLarge         = "TOO_LARGE"
	AgentCodeOverBudget       = "OVER_BUDGET"
)

// 警告级错误码（severity = "warning"，不阻断）。
const (
	AgentCodeFrameOrder    = "FRAME_ORDER"
	AgentCodeUselessEdge   = "USELESS_EDGE"
	AgentCodeOrphanNode    = "ORPHAN_NODE"
	AgentCodeUnknownAction = "UNKNOWN_ACTION"
	AgentCodeNoChanges     = "NO_CHANGES"
	AgentCodeBadJson       = "BAD_JSON"
)

// AgentIssueSeverity 严重级别的唯一真相源："error" / "warning"；未知 code 返回 "error"。
func AgentIssueSeverity(code string) string {
	switch code {
	case AgentCodeFrameOrder, AgentCodeUselessEdge, AgentCodeOrphanNode, AgentCodeUnknownAction, AgentCodeNoChanges, AgentCodeBadJson:
		return "warning"
	}
	return "error"
}

// ===== 模型目录 =====

// AgentModelHint 给模型看的模型清单（含各自档位）。
type AgentModelHint struct {
	Model  string `json:"model"`
	Type   string `json:"type"`
	Label  string `json:"label"`
	Unit   string `json:"unit"`   // count / second / image / call
	Points int    `json:"points"` // 标准单价（给模型做取舍参考）
	// 促销价（T18）：窗口生效期内实际结算取 min(标准/会员, 促销)（model.EffectiveUnitPoints）。
	// 平台事实要如实渲染促销价，否则提示词报价会比真实扣费偏贵（复核 claim2c）。
	PromoActive bool     `json:"promo_active,omitempty"` // 促销窗口生效（不代表一定更便宜）
	PromoPoints int      `json:"promo_points,omitempty"` // 促销单价
	Durations   []int    `json:"durations,omitempty"`
	Resolutions []string `json:"resolutions,omitempty"`
}

// AgentModelCatalog 模型目录来源。生产用 dbAgentCatalog；测试与评测台注入内存实现。
type AgentModelCatalog interface {
	// Resolve 返回模型的类型与能力；未接入返回 ErrAgentModelMissing，未定价返回 ErrAgentModelNotPriced。
	Resolve(taskType, model string) (*AgentModelHint, error)
	// Estimate 与生成链路同一套计价函数（内部调 EstimateTaskPoints）。
	Estimate(taskType, model, paramsJSON string, isMember bool) (int, error)
	// All 全部已接入且已定价的模型（提示词用）。
	All() map[string][]AgentModelHint
}

var (
	ErrAgentModelMissing   = errors.New("模型未接入网关")
	ErrAgentModelNotPriced = errors.New("模型未定价")
)

// DBModelCatalog 生产实现（model.GetModelPrice + FindProvider + EstimateTaskPoints）。
type DBModelCatalog struct{}

// Resolve 查定价表与 provider 注册表：
//   - 定价表里有记录：返回该记录的真实 type（调用方据此判 MODEL_TYPE_MISMATCH）；
//   - 定价表没记录：provider 也没有 → ErrAgentModelMissing，provider 有 → ErrAgentModelNotPriced。
func (DBModelCatalog) Resolve(taskType, modelName string) (*AgentModelHint, error) {
	name := strings.TrimSpace(modelName)
	if name == "" {
		return nil, ErrAgentModelMissing
	}
	price, err := model.GetModelPrice(name)
	if err != nil {
		if FindProvider(taskType, name) == nil {
			return nil, ErrAgentModelMissing
		}
		return nil, ErrAgentModelNotPriced
	}
	return hintFromPrice(price), nil
}

// Estimate 与生成链路同一套计价函数。
func (DBModelCatalog) Estimate(taskType, modelName, paramsJSON string, isMember bool) (int, error) {
	points, _, err := EstimateTaskPoints(taskType, modelName, paramsJSON, isMember)
	return points, err
}

// All 全部已接入且已定价的模型，按任务类型分组。
func (DBModelCatalog) All() map[string][]AgentModelHint {
	out := map[string][]AgentModelHint{}
	prices, _ := model.GetModelPrices(true)
	for _, p := range prices {
		if p == nil || FindProvider(p.Type, p.Model) == nil {
			continue
		}
		out[p.Type] = append(out[p.Type], *hintFromPrice(p))
	}
	return out
}

func hintFromPrice(p *model.ModelPrice) *AgentModelHint {
	h := &AgentModelHint{
		Model:       p.Model,
		Type:        p.Type,
		Label:       p.Label,
		Unit:        p.Unit,
		Points:      p.PointsPerUnit,
		Durations:   p.AllowedDurations(),
		Resolutions: p.AllowedResolutions(),
	}
	// 促销：只在窗口生效时带上原始促销价；是否真的更便宜由渲染方按 min 口径判断（复核 claim2c）
	if p.IsPromoActive(common.GetTimestamp()) {
		h.PromoActive, h.PromoPoints = true, p.PromoPointsPerUnit
	}
	return h
}

// agentModelListText 把模型目录渲染成给模型看的几行文本（提示词与 search_models 共用）。
func agentModelListText(hints map[string][]AgentModelHint, taskType string) string {
	types := []string{model.TaskTypeText, model.TaskTypeImage, model.TaskTypeVideo, model.TaskTypeAudio}
	if taskType != "" {
		types = []string{taskType}
	}
	lines := []string{}
	for _, t := range types {
		list := hints[t]
		if len(list) == 0 {
			continue
		}
		parts := make([]string, 0, len(list))
		for _, h := range list {
			parts = append(parts, agentModelLine(h))
		}
		lines = append(lines, fmt.Sprintf("type=%s 可用：%s", t, strings.Join(parts, "、")))
	}
	if len(lines) == 0 {
		return "（没有可用的模型）"
	}
	return strings.Join(lines, "\n")
}

func agentModelLine(h AgentModelHint) string {
	detail := []string{}
	if len(h.Durations) > 0 {
		secs := make([]string, 0, len(h.Durations))
		for _, d := range h.Durations {
			secs = append(secs, fmt.Sprintf("%d", d))
		}
		detail = append(detail, "时长 "+strings.Join(secs, "/")+"s")
	}
	if len(h.Resolutions) > 0 {
		detail = append(detail, "分辨率 "+strings.Join(h.Resolutions, "/"))
	}
	if len(detail) == 0 {
		return h.Model
	}
	return fmt.Sprintf("%s（%s）", h.Model, strings.Join(detail, "，"))
}

// agentKindSchemaLine get_kind_schema 工具与提示词共用的单行描述。
func agentKindSchemaLine(s KindSchema) string {
	creatable := "不能新建"
	if s.Creatable {
		creatable = "可新建"
	}
	parts := []string{fmt.Sprintf("%s：%s", s.Kind, creatable)}
	if len(s.Required) > 0 {
		parts = append(parts, "必填 "+strings.Join(s.Required, "/"))
	}
	if len(s.AcceptsFrom) > 0 {
		parts = append(parts, "可接 "+strings.Join(s.AcceptsFrom, "/"))
	}
	if len(s.Params) > 0 {
		keys := make([]string, 0, len(s.Params))
		for _, p := range s.Params {
			keys = append(keys, p.Key)
		}
		parts = append(parts, "参数 "+strings.Join(keys, "/"))
	}
	return strings.Join(parts, "，")
}

// agentJSON 紧凑序列化（失败返回空串）。
func agentJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
