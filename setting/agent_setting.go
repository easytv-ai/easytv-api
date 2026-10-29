package setting

import (
	"encoding/json"
	"strings"
	"time"

	"easytv-api/model"
)

// 对话式工作流生成（API.md 32）。PascalCase 键，管理端热更新，缺省值即下面注释里的值。
const (
	AgentEnabled             = "AgentEnabled"             // "false"/"0" 关闭；缺省开启（同 setting.IsGatewayEnabled 的写法）
	AgentModel               = "AgentModel"               // 规划用文本模型；空 → ComposePlanModel → 第一个已定价文本模型
	AgentMaxRoundsPatch      = "AgentMaxRoundsPatch"      // 对话改图轮数上限，默认 4
	AgentMaxRoundsGenerate   = "AgentMaxRoundsGenerate"   // 从零生成轮数上限，默认 6
	AgentMaxNodes            = "AgentMaxNodes"            // apply_graph 节点数上限，默认 60
	AgentMaxPoints           = "AgentMaxPoints"           // 单次计划估价上限，0=不限，默认 0
	AgentRoundTimeoutSeconds = "AgentRoundTimeoutSeconds" // 单轮网关超时秒数，默认 90
	AgentTotalTimeoutSeconds = "AgentTotalTimeoutSeconds" // 总超时秒数，默认 480
	AgentExamples            = "AgentExamples"            // 运营经验条目 JSON 数组
)

// AgentMaxRoundsHardLimit 轮数硬上限（防止运营把值配成 10000 烧积分）。
// service.AgentMaxRoundsHardLimit 与本常量取同一个值 8（两处各写一次，注释互相指认）；
// setting 只 import common + model + 标准库，不 import service，避免循环依赖。
const AgentMaxRoundsHardLimit = 8

// AgentExample 运营经验条目（结构对齐 ComfyUI-Copilot 的 expert json）。
type AgentExample struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"` // 可抄的 DSL/动作片段
}

// IsAgentEnabled 对话式工作流开关："false"/"0" → false；缺省 true。
func IsAgentEnabled() bool {
	v := GetSettingString(AgentEnabled)
	return v != "false" && v != "0"
}

// GetAgentModel 规划用文本模型：AgentModel 非空且已定价（model_price 里有 text 类型记录）时返回它，否则 ""。
// 「已在网关接入」的最终判定在 service.AgentModel 里补一次 FindProvider，这里只看定价表，避免 setting import service。
func GetAgentModel() string {
	m := strings.TrimSpace(GetSettingString(AgentModel))
	if m == "" {
		return ""
	}
	if p, err := model.GetModelPrice(m); err != nil || p.Type != model.TaskTypeText {
		return ""
	}
	return m
}

// GetAgentMaxRounds 轮数上限：mode=="generate" → AgentMaxRoundsGenerate，否则 AgentMaxRoundsPatch；结果夹到 [1, 8]。
func GetAgentMaxRounds(mode string) int {
	key, def := AgentMaxRoundsPatch, 4
	if mode == "generate" {
		key, def = AgentMaxRoundsGenerate, 6
	}
	return clampAgentInt(getIntSetting(key, def), 1, AgentMaxRoundsHardLimit)
}

// GetAgentMaxNodes apply_graph 节点数上限，默认 60，夹到 [1, 200]。
func GetAgentMaxNodes() int {
	return clampAgentInt(getIntSetting(AgentMaxNodes, 60), 1, 200)
}

// GetAgentMaxPoints 单次计划估价上限，默认 0（不限），负数按 0。
func GetAgentMaxPoints() int {
	return clampAgentInt(getIntSetting(AgentMaxPoints, 0), 0, 0)
}

// GetAgentRoundTimeout 单轮网关超时，默认 90s，夹到 [10s, 300s]。
func GetAgentRoundTimeout() time.Duration {
	return time.Duration(clampAgentInt(getIntSetting(AgentRoundTimeoutSeconds, 90), 10, 300)) * time.Second
}

// GetAgentTotalTimeout 总超时，默认 480s，夹到 [30s, 1800s]。
func GetAgentTotalTimeout() time.Duration {
	return time.Duration(clampAgentInt(getIntSetting(AgentTotalTimeoutSeconds, 480), 30, 1800)) * time.Second
}

// GetAgentExamples 运营经验条目；JSON 解析失败返回 nil，不报错。
func GetAgentExamples() []AgentExample {
	raw := strings.TrimSpace(GetSettingString(AgentExamples))
	if raw == "" {
		return nil
	}
	var list []AgentExample
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil
	}
	out := make([]AgentExample, 0, len(list))
	for _, e := range list {
		e.Name, e.Description, e.Content = strings.TrimSpace(e.Name), strings.TrimSpace(e.Description), strings.TrimSpace(e.Content)
		if e.Name == "" && e.Content == "" {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clampAgentInt 把配置值夹到 [min, max]；max <= 0 表示只保底（0 = 不限）。
func clampAgentInt(v, min, max int) int {
	if v < min {
		return min
	}
	if max > 0 && v > max {
		return max
	}
	return v
}
