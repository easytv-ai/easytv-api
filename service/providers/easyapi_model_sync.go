package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

// ===== 从网关模型详情同步能力字段（API.md 28.3）=====
//
// 网关 /pricing/detail 的 params.<分类>.properties 对部分模型（2026-09 只有 Kling 系列）给出了结构化参数：
// duration 的 enum 或 min/max、resolution 的 enum、输入类型里有没有 video、有没有 generate_audio。
// 这些正好对应 model_price 上运营一直手填的 durations / resolutions / abilities。
// 这里把它们翻译过来；网关没给的字段保持原样，运营手填的值默认不覆盖（force 才覆盖）。

// 时长按 min~max 范围给出时最多展开多少档；再多说明是连续值，前端按输入框处理更合适，且 durations 列只有 60 字符
const gatewaySyncMaxDurationTiers = 16

// GatewaySyncFields 从网关参数表翻译出的字段；nil / 空表示网关没给。
type GatewaySyncFields struct {
	Durations   []int
	Resolutions []string
	// Abilities 能推断出的能力位：有 generate_audio → native_audio（输入类型里的 video 不可信，见 ParseGatewayParams）
	Abilities []string
	// AspectRatios 目前 model_price 没有对应列，只在 changes 里回显给运营看
	AspectRatios []string
}

type gatewayParamProperty struct {
	Type    string            `json:"type"`
	Enum    []json.RawMessage `json:"enum"`
	Min     json.RawMessage   `json:"min"`
	Max     json.RawMessage   `json:"max"`
	Default json.RawMessage   `json:"default"`
}

type gatewayParamCategory struct {
	Input      []string                        `json:"input"`
	Properties map[string]gatewayParamProperty `json:"properties"`
}

func rawNumber(raw json.RawMessage) (float64, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	return n, err == nil
}

func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

// ParseGatewayParams 把网关 params 翻译成 model_price 字段。params 为空或没有结构化参数时返回 ok=false。
func ParseGatewayParams(params json.RawMessage) (fields GatewaySyncFields, ok bool) {
	if len(params) == 0 || !json.Valid(params) {
		return fields, false
	}
	var cats map[string]gatewayParamCategory
	if json.Unmarshal(params, &cats) != nil || len(cats) == 0 {
		return fields, false
	}
	// 分类名固定顺序，保证同样的输入得到同样的输出
	names := make([]string, 0, len(cats))
	for name := range cats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cat := cats[name]
		// 注意：cat.Input 里带 "video" 不能当成「能吃参考视频」。2026-09-18 实测 Kling 3.0 Omni 声明了 video 输入，
		// 但网关只把素材送进上游的图片参考字段，带参考视频必败（Image format is invalid）。video_reference 只靠
		// 模型名推断或运营手动勾，不从网关参数表同步。
		for key, prop := range cat.Properties {
			switch strings.ToLower(key) {
			case "duration":
				if d := durationsFromProperty(prop); len(d) > 0 {
					fields.Durations = d
					ok = true
				}
			case "resolution":
				if r := enumStrings(prop); len(r) > 0 {
					fields.Resolutions = r
					ok = true
				}
			case "aspect_ratio", "ratio":
				if r := enumStrings(prop); len(r) > 0 {
					fields.AspectRatios = r
					ok = true
				}
			case "generate_audio", "audio":
				fields.Abilities = appendUnique(fields.Abilities, model.AbilityNativeAudio)
				ok = true
			}
		}
	}
	return fields, ok
}

func appendUnique(list []string, v string) []string {
	for _, item := range list {
		if item == v {
			return list
		}
	}
	return append(list, v)
}

func enumStrings(prop gatewayParamProperty) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range prop.Enum {
		v := rawString(raw)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// durationsFromProperty enum 优先；否则 min~max 整数展开（最多 gatewaySyncMaxDurationTiers 档，再多不展开）。
func durationsFromProperty(prop gatewayParamProperty) []int {
	if len(prop.Enum) > 0 {
		out := []int{}
		seen := map[int]bool{}
		for _, raw := range prop.Enum {
			n, ok := rawNumber(raw)
			if !ok || n <= 0 || n != math.Trunc(n) || seen[int(n)] {
				continue
			}
			seen[int(n)] = true
			out = append(out, int(n))
		}
		sort.Ints(out)
		return out
	}
	lo, okLo := rawNumber(prop.Min)
	hi, okHi := rawNumber(prop.Max)
	if !okLo || !okHi || lo <= 0 || hi < lo {
		return nil
	}
	start, end := int(math.Ceil(lo)), int(math.Floor(hi))
	if end-start+1 > gatewaySyncMaxDurationTiers {
		return nil
	}
	out := make([]int, 0, end-start+1)
	for n := start; n <= end; n++ {
		out = append(out, n)
	}
	return out
}

// GatewaySyncResult 一个模型的同步结果。
type GatewaySyncResult struct {
	Id      int      `json:"id"`
	Model   string   `json:"model"`
	Changes []string `json:"changes"`
	// Notes 只回显、不落库的信息（比例枚举），不算改动
	Notes   []string `json:"notes,omitempty"`
	Skipped string   `json:"skipped,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// SyncModelPriceFromGateway 拉网关详情并写回 durations / resolutions / abilities。
// force=false 时只填空字段（运营手填的不动），force=true 时网关给了什么就覆盖什么；abilities 永远只增不减。
func SyncModelPriceFromGateway(p *model.ModelPrice, force bool) GatewaySyncResult {
	res := GatewaySyncResult{Id: p.Id, Model: p.Model, Changes: []string{}}
	detail, err := FetchGatewayModelDetail(p.Model, "")
	if err != nil {
		// 网关按令牌分组判可见性，不少模型（gpt-image / gemini / sora 等）详情接口直接 404：这是预期情况，算跳过不算失败
		if strings.Contains(err.Error(), "404") {
			res.Skipped = "网关没有这个模型的详情"
			return res
		}
		res.Error = "网关详情拉取失败：" + err.Error()
		return res
	}
	if detail == nil {
		res.Error = "网关没有这个模型的详情"
		return res
	}
	fields, ok := ParseGatewayParams(detail.Params)
	if !ok {
		res.Skipped = "网关没有给出结构化参数"
		return res
	}
	updates := map[string]any{}
	if len(fields.Durations) > 0 {
		parts := make([]string, 0, len(fields.Durations))
		for _, n := range fields.Durations {
			parts = append(parts, strconv.Itoa(n))
		}
		next := model.NormalizeDurations(strings.Join(parts, ","))
		if next != "" && next != p.Durations && (force || strings.TrimSpace(p.Durations) == "") {
			updates["durations"] = next
			res.Changes = append(res.Changes, fmt.Sprintf("时长 %s → %s", orEmpty(p.Durations), next))
		}
	}
	if len(fields.Resolutions) > 0 {
		next := model.NormalizeResolutions(strings.Join(fields.Resolutions, ","))
		if next != "" && next != p.Resolutions && (force || strings.TrimSpace(p.Resolutions) == "") {
			updates["resolutions"] = next
			res.Changes = append(res.Changes, fmt.Sprintf("分辨率 %s → %s", orEmpty(p.Resolutions), next))
		}
	}
	if len(fields.Abilities) > 0 {
		merged := p.AllowedAbilities()
		added := []string{}
		for _, a := range fields.Abilities {
			if !p.DeclaresAbility(a) {
				merged = append(merged, a)
				added = append(added, a)
			}
		}
		if len(added) > 0 {
			next := model.NormalizeAbilities(strings.Join(merged, ","))
			if next != p.Abilities {
				updates["abilities"] = next
				res.Changes = append(res.Changes, "能力 +"+strings.Join(added, ","))
			}
		}
	}
	if len(fields.AspectRatios) > 0 {
		res.Notes = append(res.Notes, "比例 "+strings.Join(fields.AspectRatios, ","))
	}
	if len(updates) == 0 {
		res.Skipped = "已是最新"
		return res
	}
	if err := model.UpdateModelPriceFields(p.Id, updates); err != nil {
		res.Error = "写回失败：" + err.Error()
		return res
	}
	common.SysLog(fmt.Sprintf("model_price %s synced from gateway: %v", p.Model, updates))
	return res
}

func orEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（空）"
	}
	return s
}

// SyncAllModelPricesFromGateway 对全部定价行逐个同步（网关侧有缓存，顺序拉即可）。
func SyncAllModelPricesFromGateway(force bool) ([]GatewaySyncResult, error) {
	list, err := model.GetModelPrices(false)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("还没有任何定价")
	}
	out := make([]GatewaySyncResult, 0, len(list))
	for _, p := range list {
		out = append(out, SyncModelPriceFromGateway(p, force))
	}
	return out, nil
}
