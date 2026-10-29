package agent_eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"easytv-api/service"
)

// MockCatalog 内存模型目录：实现 service.AgentModelCatalog，替代线上 model_price。
// 评测台与自测都用它——**不查库、不查网关**，估价完全确定。
type MockCatalog struct {
	byType map[string][]service.AgentModelHint
}

// NewMockCatalog 与 mock 图里用到的模型名严格对应（mockImageModel / mockVideoModel / ...）。
// 单价刻意取整数，便于手算估价：image 8 / video 12 每秒 / audio 2 / text 1。
func NewMockCatalog() *MockCatalog {
	return NewCatalogFromHints(map[string][]service.AgentModelHint{
		"text": []service.AgentModelHint{
			{Model: mockTextModel, Type: "text", Label: "GPT-4o mini", Unit: "count", Points: 1},
		},
		"image": []service.AgentModelHint{
			{Model: mockImageModel, Type: "image", Label: "GPT Image 1", Unit: "image", Points: 8,
				Resolutions: []string{"1K", "2K", "4K"}},
			{Model: "seedream-3", Type: "image", Label: "Seedream 3", Unit: "image", Points: 5,
				Resolutions: []string{"1K", "2K"}},
		},
		"video": []service.AgentModelHint{
			{Model: mockVideoModel, Type: "video", Label: "Kling v1", Unit: "second", Points: 12,
				Durations: []int{5, 10}, Resolutions: []string{"720P", "1080P"}},
			{Model: "wan-2.2", Type: "video", Label: "Wan 2.2", Unit: "second", Points: 4,
				Durations: []int{5}, Resolutions: []string{"480P", "720P"}},
		},
		"audio": []service.AgentModelHint{
			{Model: mockAudioModel, Type: "audio", Label: "TTS-1", Unit: "call", Points: 2},
		},
	})
}

// NewCatalogFromHints 用声明式清单构造目录（live 跑法用它装 AGENT_EVAL_MODELS）。
// 空清单返回 nil，让调用方自己决定是跳过还是报错。
func NewCatalogFromHints(hints map[string][]service.AgentModelHint) *MockCatalog {
	if len(hints) == 0 {
		return nil
	}
	c := &MockCatalog{byType: make(map[string][]service.AgentModelHint, len(hints))}
	for taskType, list := range hints {
		kept := make([]service.AgentModelHint, 0, len(list))
		for _, h := range list {
			if strings.TrimSpace(h.Model) == "" {
				continue
			}
			if h.Type == "" {
				h.Type = taskType
			}
			kept = append(kept, h)
		}
		if len(kept) > 0 {
			c.byType[taskType] = kept
		}
	}
	if len(c.byType) == 0 {
		return nil
	}
	return c
}

// ParseModelHintsJSON 解析 AGENT_EVAL_MODELS（JSON，map[string][]service.AgentModelHint）。
func ParseModelHintsJSON(raw string) (*MockCatalog, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("AGENT_EVAL_MODELS 为空")
	}
	var hints map[string][]service.AgentModelHint
	if err := json.Unmarshal([]byte(raw), &hints); err != nil {
		return nil, fmt.Errorf("AGENT_EVAL_MODELS 不是合法 JSON：%w", err)
	}
	c := NewCatalogFromHints(hints)
	if c == nil {
		return nil, errors.New("AGENT_EVAL_MODELS 里没有任何模型")
	}
	return c, nil
}

// Resolve 实现 service.AgentModelCatalog：按任务类型找模型；未接入 → ErrAgentModelMissing，
// 接入但单价 <= 0 → ErrAgentModelNotPriced（与生产 DBModelCatalog 的错误语义一致）。
func (c *MockCatalog) Resolve(taskType, model string) (*service.AgentModelHint, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, service.ErrAgentModelMissing
	}
	for _, h := range c.byType[taskType] {
		if h.Model != model {
			continue
		}
		if h.Points <= 0 {
			return nil, service.ErrAgentModelNotPriced
		}
		cp := h
		return &cp, nil
	}
	return nil, service.ErrAgentModelMissing
}

// Estimate 实现 service.AgentModelCatalog：second 计费的模型按 params.duration 乘单价，
// 其余按单价一次。duration 缺失时取模型第一个档位（与生成链路的缺省口径一致）。
func (c *MockCatalog) Estimate(taskType, model, paramsJSON string, isMember bool) (int, error) {
	h, err := c.Resolve(taskType, model)
	if err != nil {
		return 0, err
	}
	if h.Unit != "second" {
		return h.Points, nil
	}
	d := durationFromParams(paramsJSON)
	if d <= 0 {
		if len(h.Durations) > 0 {
			d = h.Durations[0]
		} else {
			d = 5
		}
	}
	return h.Points * d, nil
}

// All 实现 service.AgentModelCatalog：全部已接入且已定价的模型。
func (c *MockCatalog) All() map[string][]service.AgentModelHint {
	out := make(map[string][]service.AgentModelHint, len(c.byType))
	for taskType, list := range c.byType {
		cp := make([]service.AgentModelHint, 0, len(list))
		cp = append(cp, list...)
		out[taskType] = cp
	}
	return out
}

// Models 某一任务类型下的模型名（按清单顺序）。
func (c *MockCatalog) Models(taskType string) []string {
	names := make([]string, 0, len(c.byType[taskType]))
	for _, h := range c.byType[taskType] {
		names = append(names, h.Model)
	}
	return names
}

// ModelNames 全部模型名，形如 "image/gpt-image-1"（排障用，便于一眼看出清单装了什么）。
func (c *MockCatalog) ModelNames() []string {
	var out []string
	for taskType, list := range c.byType {
		for _, h := range list {
			out = append(out, taskType+"/"+h.Model)
		}
	}
	sort.Strings(out)
	return out
}

// durationFromParams 从节点参数 JSON 里取 duration：兼容 {"duration":5} 与 {"params":{"duration":5}}。
func durationFromParams(paramsJSON string) int {
	paramsJSON = strings.TrimSpace(paramsJSON)
	if paramsJSON == "" {
		return 0
	}
	var m map[string]any
	if json.Unmarshal([]byte(paramsJSON), &m) != nil {
		return 0
	}
	return durationInMap(m, 0)
}

func durationInMap(m map[string]any, depth int) int {
	if depth > 3 {
		return 0
	}
	if v, ok := m["duration"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return int(i)
			}
		}
	}
	if nested, ok := m["params"].(map[string]any); ok {
		if d := durationInMap(nested, depth+1); d > 0 {
			return d
		}
	}
	return 0
}

// ===== pricer（regenerate 用）=====

// CatalogPricer 用内存目录给 regenerate 挑模型并估价（实现 service.AgentPricer）。
// 生产用 dbAssistPricer；评测台注入这个假实现，避免任何查库。
type CatalogPricer struct {
	Catalog *MockCatalog
}

// Resolve 实现 service.AgentPricer：wanted → last → 该类型第一个可用模型。
func (p CatalogPricer) Resolve(taskType, wanted, last, paramsJSON string) (string, int, error) {
	if p.Catalog == nil {
		return "", 0, errors.New("评测台没有注入模型目录")
	}
	for _, name := range []string{wanted, last} {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if points, err := p.Catalog.Estimate(taskType, name, paramsJSON, false); err == nil {
			return name, points, nil
		}
	}
	for _, name := range p.Catalog.Models(taskType) {
		if points, err := p.Catalog.Estimate(taskType, name, paramsJSON, false); err == nil {
			return name, points, nil
		}
	}
	return "", 0, fmt.Errorf("暂无可用的%s模型", taskType)
}
