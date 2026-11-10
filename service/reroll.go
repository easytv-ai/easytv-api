package service

import (
	"sort"
	"strings"
	"sync"
	"time"

	"easytv-api/model"
)

// 少抽卡（API.md 39）：记下「这次生成顶替了哪条成功结果、用户嫌它哪里不好」，
// 据此统计一次留用率——用户在意的不是单价，而是几次才能出一条能用的。

const (
	// RerollKeepDays 留用率统计窗口
	RerollKeepDays = 30
	// RerollKeepMinTasks 样本少于这个数不给留用率
	RerollKeepMinTasks = 10
	rerollKeepCacheTTL = 30 * time.Minute
	rerollReasonScan   = 5000
)

// NormalizeRerollReasons 只留白名单里的原因，去重，保持白名单顺序。
func NormalizeRerollReasons(in []string) []string {
	seen := map[string]bool{}
	for _, v := range in {
		seen[strings.TrimSpace(v)] = true
	}
	out := []string{}
	for _, r := range model.RerollReasons {
		if seen[r] {
			out = append(out, r)
		}
	}
	return out
}

// MarkReroll 提交前调用：节点上已有同类型成功结果时，记下被顶替的任务与用户给的原因。
// 续写（ParentTaskId）是往后接一段，不算重抽；文本任务不算。
func MarkReroll(task *model.GenerationTask, reasons []string) {
	if task == nil || task.ParentTaskId > 0 || task.CanvasId <= 0 || task.NodeId == "" {
		return
	}
	if task.Type != model.TaskTypeImage && task.Type != model.TaskTypeVideo && task.Type != model.TaskTypeAudio {
		return
	}
	if task.RerollOf == 0 {
		task.RerollOf = model.LatestSucceededNodeTaskId(task.CanvasId, task.NodeId, task.Type)
	}
	if task.RerollOf > 0 {
		task.RerollReason = strings.Join(NormalizeRerollReasons(reasons), ",")
	}
}

// ---- 统计 ----

// RerollNodeRow 一个节点窗口内的成功任务：条数、总积分、最后一条（留用的那条）的积分。
type RerollNodeRow struct {
	Type     string
	N        int64
	Points   int64
	LastCost int64
}

// RerollModelRow 一个模型窗口内成功的节点任务数，与其中后来又被同节点新结果顶替的条数。
type RerollModelRow struct {
	Model    string
	Type     string
	Total    int64
	Rerolled int64
}

type RerollTypeStat struct {
	Type           string   `json:"type"`
	Nodes          int64    `json:"nodes"`
	FirstPassNodes int64    `json:"first_pass_nodes"`
	FirstPassRate  *float64 `json:"first_pass_rate"`
	AvgPerNode     float64  `json:"avg_per_node"`
}

type RerollModelStat struct {
	Model    string `json:"model"`
	Type     string `json:"type"`
	Total    int64  `json:"total"`
	Rerolled int64  `json:"rerolled"`
	// KeepRate 留用率百分比：成功结果里没有再被同节点新结果顶替的占比
	KeepRate *float64 `json:"keep_rate"`
	// PullsPerKeep 平均几条成功结果换一条留用（1 / 留用率），留用数为 0 时为 nil
	PullsPerKeep *float64 `json:"pulls_per_keep"`
}

type RerollReasonStat struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// RerollMetrics 创作指标里的「抽卡」块（API.md 39.3）。只看成功任务：失败会退分，不是用户掏钱的抽卡。
type RerollMetrics struct {
	Nodes          int64    `json:"nodes"`
	Succeeded      int64    `json:"succeeded"`
	FirstPassNodes int64    `json:"first_pass_nodes"`
	FirstPassRate  *float64 `json:"first_pass_rate"`
	AvgPerNode     float64  `json:"avg_per_node"`
	// SpentPoints 这些成功任务一共花的积分；WastedPoints 其中被顶替掉的结果花的积分
	SpentPoints  int64              `json:"spent_points"`
	WastedPoints int64              `json:"wasted_points"`
	WastedRate   *float64           `json:"wasted_rate"`
	ByType       []RerollTypeStat   `json:"by_type"`
	ByModel      []RerollModelStat  `json:"by_model"`
	Reasons      []RerollReasonStat `json:"reasons"`
	// ReasonTasks 窗口内带原因的重抽任务数
	ReasonTasks int64 `json:"reason_tasks"`
}

// SummarizeRerolls 纯函数：节点行 / 模型行 / 原因串 → 抽卡指标。
func SummarizeRerolls(nodes []RerollNodeRow, models []RerollModelRow, reasons []string) RerollMetrics {
	m := RerollMetrics{ByType: []RerollTypeStat{}, ByModel: []RerollModelStat{}, Reasons: []RerollReasonStat{}}
	types := map[string]*RerollTypeStat{}
	typeTasks := map[string]int64{}
	for _, r := range nodes {
		if r.N <= 0 {
			continue
		}
		m.Nodes++
		m.Succeeded += r.N
		m.SpentPoints += r.Points
		if wasted := r.Points - r.LastCost; wasted > 0 {
			m.WastedPoints += wasted
		}
		ts := types[r.Type]
		if ts == nil {
			ts = &RerollTypeStat{Type: r.Type}
			types[r.Type] = ts
		}
		ts.Nodes++
		typeTasks[r.Type] += r.N
		if r.N == 1 {
			m.FirstPassNodes++
			ts.FirstPassNodes++
		}
	}
	m.FirstPassRate = Rate(m.FirstPassNodes, m.Nodes)
	m.WastedRate = Rate(m.WastedPoints, m.SpentPoints)
	if m.Nodes > 0 {
		m.AvgPerNode = round2(float64(m.Succeeded) / float64(m.Nodes))
	}
	for _, t := range creationMediaTypes {
		if ts := types[t]; ts != nil {
			ts.FirstPassRate = Rate(ts.FirstPassNodes, ts.Nodes)
			ts.AvgPerNode = round2(float64(typeTasks[t]) / float64(ts.Nodes))
			m.ByType = append(m.ByType, *ts)
		}
	}
	for _, r := range models {
		if r.Model == "" || r.Total <= 0 {
			continue
		}
		st := RerollModelStat{Model: r.Model, Type: r.Type, Total: r.Total, Rerolled: r.Rerolled}
		kept := r.Total - r.Rerolled
		st.KeepRate = Rate(kept, r.Total)
		if kept > 0 {
			v := round2(float64(r.Total) / float64(kept))
			st.PullsPerKeep = &v
		}
		m.ByModel = append(m.ByModel, st)
	}
	sort.SliceStable(m.ByModel, func(i, j int) bool { return m.ByModel[i].Total > m.ByModel[j].Total })
	counts := map[string]int64{}
	for _, raw := range reasons {
		if raw == "" {
			continue
		}
		m.ReasonTasks++
		for _, r := range strings.Split(raw, ",") {
			if r != "" {
				counts[r]++
			}
		}
	}
	for _, r := range model.RerollReasons {
		if counts[r] > 0 {
			m.Reasons = append(m.Reasons, RerollReasonStat{Reason: r, Count: counts[r]})
		}
	}
	sort.SliceStable(m.Reasons, func(i, j int) bool { return m.Reasons[i].Count > m.Reasons[j].Count })
	return m
}

// queryRerollModels [from, to) 内每个模型成功的节点任务数，以及其中又被同节点更新的成功结果顶替的条数。
func queryRerollModels(from, to int64) ([]RerollModelRow, error) {
	var rows []RerollModelRow
	err := model.DB.Raw(`SELECT t.model AS model, t.type AS type, COUNT(*) AS total,
		SUM(CASE WHEN EXISTS (SELECT 1 FROM generation_task n
			WHERE n.canvas_id = t.canvas_id AND n.node_id = t.node_id AND n.type = t.type
			AND n.status = ? AND n.id > t.id) THEN 1 ELSE 0 END) AS rerolled
		FROM generation_task t
		WHERE t.created_at >= ? AND t.created_at < ? AND t.status = ? AND t.canvas_id > 0 AND t.node_id <> ''
		AND t.parent_task_id = 0 AND t.type IN ?
		GROUP BY t.model, t.type`,
		model.TaskStatusSucceeded, from, to, model.TaskStatusSucceeded, creationMediaTypes).Scan(&rows).Error
	return rows, err
}

// BuildRerollMetrics [from, to) 的抽卡指标。
func BuildRerollMetrics(from, to int64) (*RerollMetrics, error) {
	var nodes []RerollNodeRow
	if err := model.DB.Raw(`SELECT type, COUNT(*) AS n, COALESCE(SUM(cost_points), 0) AS points,
		CAST(SUBSTRING_INDEX(GROUP_CONCAT(cost_points ORDER BY id DESC), ',', 1) AS SIGNED) AS last_cost
		FROM generation_task
		WHERE created_at >= ? AND created_at < ? AND status = ? AND canvas_id > 0 AND node_id <> ''
		AND parent_task_id = 0 AND type IN ?
		GROUP BY canvas_id, node_id, type`, from, to, model.TaskStatusSucceeded, creationMediaTypes).Scan(&nodes).Error; err != nil {
		return nil, err
	}
	models, err := queryRerollModels(from, to)
	if err != nil {
		return nil, err
	}
	var reasons []string
	if err := model.DB.Model(&model.GenerationTask{}).
		Where("created_at >= ? AND created_at < ? AND reroll_reason <> ''", from, to).
		Limit(rerollReasonScan).Pluck("reroll_reason", &reasons).Error; err != nil {
		return nil, err
	}
	m := SummarizeRerolls(nodes, models, reasons)
	return &m, nil
}

// ---- 创作台模型列表上的留用率 ----

// ModelKeepStat 模型近 30 天的留用情况，给创作台选模型时参考。
type ModelKeepStat struct {
	Total        int64
	KeepRate     float64
	PullsPerKeep float64
}

// assembleModelKeep 纯函数：样本不够或一条都没留用的模型不给数。
func assembleModelKeep(rows []RerollModelRow) map[string]*ModelKeepStat {
	total, rerolled := map[string]int64{}, map[string]int64{}
	for _, r := range rows {
		total[r.Model] += r.Total
		rerolled[r.Model] += r.Rerolled
	}
	out := map[string]*ModelKeepStat{}
	for name, n := range total {
		kept := n - rerolled[name]
		if name == "" || n < RerollKeepMinTasks || kept <= 0 {
			continue
		}
		out[name] = &ModelKeepStat{Total: n, KeepRate: *Rate(kept, n), PullsPerKeep: round2(float64(n) / float64(kept))}
	}
	return out
}

var (
	rerollKeepMu     sync.Mutex
	rerollKeepCache  map[string]*ModelKeepStat
	rerollKeepCached time.Time
)

// CachedModelKeep 匿名模型列表用（30 天口径，30 分钟刷新）；查库失败返回上一份或空。
func CachedModelKeep() map[string]*ModelKeepStat {
	rerollKeepMu.Lock()
	defer rerollKeepMu.Unlock()
	if rerollKeepCache != nil && time.Since(rerollKeepCached) < rerollKeepCacheTTL {
		return rerollKeepCache
	}
	now := time.Now()
	rows, err := queryRerollModels(now.Add(-RerollKeepDays*24*time.Hour).Unix(), now.Unix()+1)
	if err != nil {
		if rerollKeepCache == nil {
			return map[string]*ModelKeepStat{}
		}
		return rerollKeepCache
	}
	rerollKeepCache, rerollKeepCached = assembleModelKeep(rows), now
	return rerollKeepCache
}
