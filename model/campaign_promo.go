package model

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// CampaignPromoItem 活动价一条（API.md 8.6）：指定模型在活动进行中按折扣或一口价扣费。
// 后台可写对象或数组：{"model":"veo-3","rate":0.5} / {"models":["veo-3","hailuo-02"],"rate":0.8,"label":"夏日 8 折"}。
type CampaignPromoItem struct {
	Model  string  `json:"model"`
	Rate   float64 `json:"rate,omitempty"`
	Points int     `json:"points,omitempty"`
	Label  string  `json:"label,omitempty"`
}

type campaignPromoRaw struct {
	Model  string   `json:"model"`
	Models []string `json:"models"`
	Rate   float64  `json:"rate"`
	Points int      `json:"points"`
	Label  string   `json:"label"`
}

var (
	campaignPromoMu      sync.Mutex
	campaignPromoCache   map[string][]CampaignPromoItem
	campaignPromoCacheAt int64
	campaignPromoTest    map[string][]CampaignPromoItem
)

const campaignPromoCacheTTL = 15

// ParseCampaignModelPromo 解析活动价 JSON；非法条目丢掉，不让整场活动挂掉。
func ParseCampaignModelPromo(raw string) []CampaignPromoItem {
	s := strings.TrimSpace(raw)
	if s == "" || s == "null" || s == `""` {
		return nil
	}
	var nodes []campaignPromoRaw
	if strings.HasPrefix(s, "[") {
		if json.Unmarshal([]byte(s), &nodes) != nil {
			return nil
		}
	} else {
		var one campaignPromoRaw
		if json.Unmarshal([]byte(s), &one) != nil {
			return nil
		}
		nodes = []campaignPromoRaw{one}
	}
	out := make([]CampaignPromoItem, 0, len(nodes))
	seen := map[string]int{}
	for _, node := range nodes {
		rate, points, label, ok := normalizeCampaignPromo(node.Rate, node.Points, node.Label)
		if !ok {
			continue
		}
		names := append([]string{}, node.Models...)
		if n := strings.TrimSpace(node.Model); n != "" {
			names = append(names, n)
		}
		for _, name := range names {
			modelName := normalizeCampaignModelName(name)
			if modelName == "" {
				continue
			}
			item := CampaignPromoItem{Model: modelName, Rate: rate, Points: points, Label: label}
			if i, exists := seen[modelName]; exists {
				out[i] = item
				continue
			}
			seen[modelName] = len(out)
			out = append(out, item)
		}
	}
	return out
}

// NormalizeCampaignModelPromo 管理端保存前清洗；有内容但解析不出有效条目则报错。
func NormalizeCampaignModelPromo(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "null" || s == `""` {
		return "", ""
	}
	items := ParseCampaignModelPromo(s)
	if len(items) == 0 {
		return "", "模型促销需包含 model 与 rate(0~1) 或 points"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", "模型促销配置需是 JSON"
	}
	return string(b), ""
}

func normalizeCampaignModelName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || utf8.RuneCountInString(n) > 80 {
		return ""
	}
	return n
}

func normalizeCampaignPromo(rate float64, points int, label string) (float64, int, string, bool) {
	hasRate := rate > 0 && rate < 1
	hasPoints := points >= 1
	if !hasRate && !hasPoints {
		return 0, 0, "", false
	}
	if !hasRate {
		rate = 0
	}
	if !hasPoints {
		points = 0
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = defaultCampaignPromoLabel(rate)
	}
	if r := []rune(label); len(r) > 20 {
		label = string(r[:20])
	}
	return rate, points, label, true
}

func defaultCampaignPromoLabel(rate float64) string {
	if rate > 0 && rate < 1 {
		n := int(math.Round(rate * 10))
		if n < 1 {
			n = 1
		}
		if n > 9 {
			n = 9
		}
		return "活动 " + strconv.Itoa(n) + " 折"
	}
	return "活动特价"
}

// CampaignPromoPoints 活动价折成积分；不比标准价便宜时返回 0。
func CampaignPromoPoints(base int, item CampaignPromoItem) int {
	if base <= 1 {
		return 0
	}
	best := 0
	if item.Points > 0 && item.Points < base {
		best = item.Points
	}
	if item.Rate > 0 && item.Rate < 1 {
		fromRate := int(math.Ceil(float64(base)*item.Rate - 1e-9))
		if fromRate < 1 {
			fromRate = 1
		}
		if fromRate < base && (best == 0 || fromRate < best) {
			best = fromRate
		}
	}
	return best
}

// ResetCampaignPromoCache 活动增删改后立刻失效，避免 15 秒内还按旧价扣。
func ResetCampaignPromoCache() {
	campaignPromoMu.Lock()
	defer campaignPromoMu.Unlock()
	campaignPromoCache = nil
	campaignPromoCacheAt = 0
}

// SetTestCampaignPromos 单测注入活动价；传 nil 取消。
func SetTestCampaignPromos(items []CampaignPromoItem) {
	campaignPromoMu.Lock()
	defer campaignPromoMu.Unlock()
	if items == nil {
		campaignPromoTest = nil
		return
	}
	next := map[string][]CampaignPromoItem{}
	for _, item := range items {
		name := normalizeCampaignModelName(item.Model)
		if name == "" {
			continue
		}
		item.Model = name
		next[name] = append(next[name], item)
	}
	campaignPromoTest = next
}

// CampaignPromoFor 进行中的活动里，该模型最便宜的一条活动价。
func CampaignPromoFor(modelName string, base int, now int64) (CampaignPromoItem, int, bool) {
	name := normalizeCampaignModelName(modelName)
	if name == "" || base <= 1 {
		return CampaignPromoItem{}, 0, false
	}
	offers := liveCampaignPromos(now)[name]
	if len(offers) == 0 {
		return CampaignPromoItem{}, 0, false
	}
	best := offers[0]
	bestPts := CampaignPromoPoints(base, best)
	for _, item := range offers[1:] {
		pts := CampaignPromoPoints(base, item)
		if pts > 0 && (bestPts == 0 || pts < bestPts) {
			best, bestPts = item, pts
		}
	}
	if bestPts <= 0 {
		return CampaignPromoItem{}, 0, false
	}
	return best, bestPts, true
}

func liveCampaignPromos(now int64) map[string][]CampaignPromoItem {
	campaignPromoMu.Lock()
	defer campaignPromoMu.Unlock()
	if campaignPromoTest != nil {
		return campaignPromoTest
	}
	if campaignPromoCache != nil && now >= campaignPromoCacheAt && now-campaignPromoCacheAt < campaignPromoCacheTTL {
		return campaignPromoCache
	}
	campaignPromoCache = loadLiveCampaignPromos(now)
	campaignPromoCacheAt = now
	return campaignPromoCache
}

func loadLiveCampaignPromos(now int64) map[string][]CampaignPromoItem {
	out := map[string][]CampaignPromoItem{}
	if DB == nil {
		return out
	}
	var list []*Campaign
	q := DB.Where("status = ? AND (starts_at = 0 OR starts_at <= ?) AND (ends_at = 0 OR ends_at > ?)",
		CampaignStatusActive, now, now)
	if err := q.Find(&list).Error; err != nil {
		return out
	}
	for _, c := range list {
		if c.EffectiveStatus(now) != CampaignStatusActive {
			continue
		}
		for _, item := range ParseCampaignModelPromo(c.ModelPromo) {
			out[item.Model] = append(out[item.Model], item)
		}
	}
	return out
}
