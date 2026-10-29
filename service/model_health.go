package service

import (
	"sort"
	"sync"
	"time"

	"easytv-api/model"
)

// 模型健康（API.md 37）：按模型看最近 N 天的生成成功率与失败原因，运营据此发现「哪个模型在坏」，
// 创作台在失败率过高的模型上给一句提醒。口径与创作指标一致（countableTasks：排除扣分前就被拒的任务）。

const (
	ModelHealthDays = 7
	// ModelHealthMinTasks 样本少于这个数不评价（一两次失败说明不了什么）
	ModelHealthMinTasks = 5
	// ModelHealthWarnRate 失败率（百分比，与创作指标的 Rate 同口径）达到这个值标红 / 提醒
	ModelHealthWarnRate = 50.0
	// modelHealthCacheTTL 匿名的模型列表每次都算一遍太贵，缓存几分钟
	modelHealthCacheTTL  = 5 * time.Minute
	modelHealthTopErrors = 3

	// 建议下架（API.md 37.3）：样本够多且几乎全失败，已经不是「偶尔抽风」
	ModelHealthOfflineMinTasks = 10
	ModelHealthOfflineRate     = 90.0
)

type ModelHealthError struct {
	Message string `json:"message"`
	Count   int64  `json:"count"`
}

type ModelHealthStat struct {
	Model  string `json:"model"`
	Total  int64  `json:"total"`
	Failed int64  `json:"failed"`
	// FailRate 失败率百分比（0–100，保留一位小数），与创作指标同口径；没有样本为 nil
	FailRate *float64 `json:"fail_rate"`
	Warn     bool     `json:"warn"`
	// SuggestOffline 样本 ≥ 10 且失败率 ≥ 90%：建议运营下架或补参数（API.md 37.3）
	SuggestOffline bool               `json:"suggest_offline"`
	TopErrors      []ModelHealthError `json:"top_errors"`
}

type modelHealthRow struct {
	Model  string
	Total  int64
	Failed int64
}

type modelHealthErrorRow struct {
	Model   string
	Message string
	Count   int64
}

// BuildModelHealth 最近 days 天（到现在）每个模型的健康统计；没有任务的模型不在结果里。
func BuildModelHealth(days int, now time.Time) (map[string]*ModelHealthStat, error) {
	if days <= 0 {
		days = ModelHealthDays
	}
	from := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
	var rows []modelHealthRow
	if err := model.DB.Model(&model.GenerationTask{}).
		Select("model, COUNT(*) AS total, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed", model.TaskStatusFailed).
		Scopes(countableTasks).
		Where("created_at >= ? AND status <> ? AND type IN ?", from, model.TaskStatusCancelled, creationMediaTypes).
		Group("model").Scan(&rows).Error; err != nil {
		return nil, err
	}
	var errRows []modelHealthErrorRow
	if err := model.DB.Model(&model.GenerationTask{}).
		Select("model, LEFT(error_msg, 80) AS message, COUNT(*) AS count").
		Scopes(countableTasks).
		Where("created_at >= ? AND status = ? AND error_msg <> '' AND type IN ?", from, model.TaskStatusFailed, creationMediaTypes).
		Group("model, LEFT(error_msg, 80)").Scan(&errRows).Error; err != nil {
		return nil, err
	}
	return assembleModelHealth(rows, errRows), nil
}

// assembleModelHealth 纯函数：聚合行 → 每模型统计（失败率、是否预警、失败原因 top 3）。
func assembleModelHealth(rows []modelHealthRow, errRows []modelHealthErrorRow) map[string]*ModelHealthStat {
	out := map[string]*ModelHealthStat{}
	for _, r := range rows {
		if r.Model == "" || r.Total <= 0 {
			continue
		}
		st := &ModelHealthStat{Model: r.Model, Total: r.Total, Failed: r.Failed, TopErrors: []ModelHealthError{}}
		st.FailRate = Rate(r.Failed, r.Total)
		st.Warn = r.Total >= ModelHealthMinTasks && st.FailRate != nil && *st.FailRate >= ModelHealthWarnRate
		st.SuggestOffline = r.Total >= ModelHealthOfflineMinTasks && st.FailRate != nil && *st.FailRate >= ModelHealthOfflineRate
		out[r.Model] = st
	}
	byModel := map[string][]ModelHealthError{}
	for _, e := range errRows {
		if e.Message == "" || out[e.Model] == nil {
			continue
		}
		byModel[e.Model] = append(byModel[e.Model], ModelHealthError{Message: e.Message, Count: e.Count})
	}
	for name, list := range byModel {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Count > list[j].Count })
		if len(list) > modelHealthTopErrors {
			list = list[:modelHealthTopErrors]
		}
		out[name].TopErrors = list
	}
	return out
}

var (
	modelHealthMu     sync.Mutex
	modelHealthCache  map[string]*ModelHealthStat
	modelHealthCached time.Time
)

// CachedModelHealth 给匿名模型列表用的缓存版（7 天口径，5 分钟刷新）；查库失败返回上一份或空。
func CachedModelHealth() map[string]*ModelHealthStat {
	modelHealthMu.Lock()
	defer modelHealthMu.Unlock()
	if modelHealthCache != nil && time.Since(modelHealthCached) < modelHealthCacheTTL {
		return modelHealthCache
	}
	stats, err := BuildModelHealth(ModelHealthDays, time.Now())
	if err != nil {
		if modelHealthCache == nil {
			return map[string]*ModelHealthStat{}
		}
		return modelHealthCache
	}
	modelHealthCache, modelHealthCached = stats, time.Now()
	return stats
}

// InvalidateModelHealthCache 测试 / 运营改动后强制下次重算。
func InvalidateModelHealthCache() {
	modelHealthMu.Lock()
	modelHealthCache, modelHealthCached = nil, time.Time{}
	modelHealthMu.Unlock()
}
