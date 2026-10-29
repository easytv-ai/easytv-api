package service

import (
	"encoding/json"
	"gorm.io/gorm"
	"math"
	"sort"
	"time"

	"easytv-api/model"
)

// ===== 创作指标（API.md 17）=====
//
// 按真实交付衡量：生成成功率、首次成片成功率、每镜重试次数、引用丢失、退款补偿积压、长时间无进展的任务。
// 数据直接取任务 / 合成 / 积分流水表，只有「引用丢失」靠 ref_missing 事件（丢失发生时没有别的落库痕迹）。
// 查询是 MySQL 语法；聚合、分桶、判定放在纯函数里单测。

const (
	CreationMetricsMaxDays      = 90
	CreationMetricsDefaultIdle  = 10
	creationMetricsActiveScan   = 500
	creationMetricsStuckItems   = 20
	creationMetricsRefEventScan = 20000
)

var creationMediaTypes = []string{model.TaskTypeImage, model.TaskTypeVideo, model.TaskTypeAudio}

// Rate 百分比，保留一位小数；分母为 0 时为 nil（前端显示 —）。
func Rate(num, den int64) *float64 {
	if den <= 0 {
		return nil
	}
	v := math.Round(float64(num)*1000/float64(den)) / 10
	return &v
}

type TaskTypeStat struct {
	Type        string   `json:"type"`
	Total       int64    `json:"total"`
	Succeeded   int64    `json:"succeeded"`
	Failed      int64    `json:"failed"`
	Cancelled   int64    `json:"cancelled"`
	Active      int64    `json:"active"`
	SuccessRate *float64 `json:"success_rate"`
}

type RetryBucket struct {
	Label string `json:"label"`
	Nodes int64  `json:"nodes"`
}

type RetryTypeStat struct {
	Type       string  `json:"type"`
	Nodes      int64   `json:"nodes"`
	Tasks      int64   `json:"tasks"`
	AvgPerNode float64 `json:"avg_per_node"`
}

type RefMissingSource struct {
	Source string `json:"source"`
	Events int64  `json:"events"`
	Items  int64  `json:"items"`
	Users  int64  `json:"users"`
}

type StuckTask struct {
	Id          int    `json:"id"`
	UserId      int    `json:"user_id"`
	Type        string `json:"type"`
	Model       string `json:"model"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	CreatedAt   int64  `json:"created_at"`
	LastAt      int64  `json:"last_at"`
	IdleMinutes int64  `json:"idle_minutes"`
}

type ModelFailStat struct {
	Model    string   `json:"model"`
	Type     string   `json:"type"`
	Total    int64    `json:"total"`
	Failed   int64    `json:"failed"`
	FailRate *float64 `json:"fail_rate"`
}

type ErrorStat struct {
	Message string `json:"message"`
	Count   int64  `json:"count"`
}

type CreationDay struct {
	Day           string `json:"day"`
	Tasks         int64  `json:"tasks"`
	TasksFailed   int64  `json:"tasks_failed"`
	Compose       int64  `json:"compose"`
	ComposeDone   int64  `json:"compose_done"`
	ComposeFailed int64  `json:"compose_failed"`
}

type CreationMetrics struct {
	From        int64 `json:"from"`
	To          int64 `json:"to"`
	Days        int   `json:"days"`
	IdleMinutes int   `json:"idle_minutes"`

	// Tasks 窗口内创建的图片 / 视频 / 音频任务（不含文本对话）
	Tasks struct {
		Total         int64          `json:"total"`
		Succeeded     int64          `json:"succeeded"`
		Failed        int64          `json:"failed"`
		Cancelled     int64          `json:"cancelled"`
		Active        int64          `json:"active"`
		SuccessRate   *float64       `json:"success_rate"`
		TimeoutFailed int64          `json:"timeout_failed"`
		ByType        []TaskTypeStat `json:"by_type"`
	} `json:"tasks"`

	Compose struct {
		Jobs        int64    `json:"jobs"`
		Done        int64    `json:"done"`
		Failed      int64    `json:"failed"`
		Active      int64    `json:"active"`
		SuccessRate *float64 `json:"success_rate"`
		// 窗口内第一次合成的用户：首次就成功 / 首次失败 / 至今做成过
		FirstUsers       int64    `json:"first_users"`
		FirstSucceeded   int64    `json:"first_succeeded"`
		FirstFailed      int64    `json:"first_failed"`
		FirstSuccessRate *float64 `json:"first_success_rate"`
		EventuallyDone   int64    `json:"eventually_done"`
		EventualRate     *float64 `json:"eventual_rate"`
	} `json:"compose"`

	// Retries 每个画布节点（镜头）窗口内提交的生成次数
	Retries struct {
		Nodes        int64           `json:"nodes"`
		Tasks        int64           `json:"tasks"`
		AvgPerNode   float64         `json:"avg_per_node"`
		RetriedNodes int64           `json:"retried_nodes"`
		RetriedRate  *float64        `json:"retried_rate"`
		Buckets      []RetryBucket   `json:"buckets"`
		ByType       []RetryTypeStat `json:"by_type"`
	} `json:"retries"`

	RefMissing struct {
		Events int64 `json:"events"`
		Items  int64 `json:"items"`
		Users  int64 `json:"users"`
		// PerHundredTasks 每 100 个生成任务发生多少次引用丢失（合成镜头丢失不计入分子）
		PerHundredTasks *float64           `json:"per_hundred_tasks"`
		BySource        []RefMissingSource `json:"by_source"`
	} `json:"ref_missing"`

	Refund struct {
		BacklogTasks   int64 `json:"backlog_tasks"`
		BacklogPoints  int64 `json:"backlog_points"`
		BacklogOldest  int64 `json:"backlog_oldest"`
		RefundedTasks  int64 `json:"refunded_tasks"`
		RefundedPoints int64 `json:"refunded_points"`
	} `json:"refund"`

	Stuck struct {
		Tasks        int64       `json:"tasks"`
		ComposeJobs  int64       `json:"compose_jobs"`
		ActiveTasks  int64       `json:"active_tasks"`
		Items        []StuckTask `json:"items"`
		ScanLimitHit bool        `json:"scan_limit_hit"`
	} `json:"stuck"`

	Failures struct {
		Models []ModelFailStat `json:"models"`
		Errors []ErrorStat     `json:"errors"`
	} `json:"failures"`

	Daily []CreationDay `json:"daily"`
}

// BuildCreationMetrics 最近 days 天（含今天）的创作指标。
func BuildCreationMetrics(days, idleMinutes int, now time.Time) (*CreationMetrics, error) {
	if days <= 0 {
		days = 7
	}
	if days > CreationMetricsMaxDays {
		days = CreationMetricsMaxDays
	}
	if idleMinutes <= 0 {
		idleMinutes = CreationMetricsDefaultIdle
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := today.AddDate(0, 0, -(days - 1)).Unix()
	to := now.Unix() + 1
	_, offset := now.Zone()

	m := &CreationMetrics{From: from, To: to, Days: days, IdleMinutes: idleMinutes}
	db := model.DB

	// ---- 生成任务 ----
	var statusRows []struct {
		Type   string
		Status string
		N      int64
	}
	if err := db.Model(&model.GenerationTask{}).Select("type, status, COUNT(*) AS n").
		Scopes(countableTasks).Where("created_at >= ? AND created_at < ? AND type IN ?", from, to, creationMediaTypes).
		Group("type, status").Scan(&statusRows).Error; err != nil {
		return nil, err
	}
	byType := map[string]*TaskTypeStat{}
	for _, t := range creationMediaTypes {
		byType[t] = &TaskTypeStat{Type: t}
	}
	for _, r := range statusRows {
		st := byType[r.Type]
		if st == nil {
			continue
		}
		addTaskStatus(st, r.Status, r.N)
	}
	for _, t := range creationMediaTypes {
		st := byType[t]
		st.SuccessRate = Rate(st.Succeeded, st.Succeeded+st.Failed)
		m.Tasks.ByType = append(m.Tasks.ByType, *st)
		m.Tasks.Total += st.Total
		m.Tasks.Succeeded += st.Succeeded
		m.Tasks.Failed += st.Failed
		m.Tasks.Cancelled += st.Cancelled
		m.Tasks.Active += st.Active
	}
	// 成功率只看已出结果的（成功 + 失败）；用户主动取消不算失败
	m.Tasks.SuccessRate = Rate(m.Tasks.Succeeded, m.Tasks.Succeeded+m.Tasks.Failed)
	db.Model(&model.GenerationTask{}).
		Where("created_at >= ? AND created_at < ? AND type IN ? AND status = ? AND error_msg LIKE ?", from, to, creationMediaTypes, model.TaskStatusFailed, "%超时%").
		Count(&m.Tasks.TimeoutFailed)

	// ---- 合成 ----
	var composeRows []struct {
		Status int
		N      int64
	}
	if err := db.Model(&model.ComposeJob{}).Select("status, COUNT(*) AS n").
		Where("created_at >= ? AND created_at < ?", from, to).Group("status").Scan(&composeRows).Error; err != nil {
		return nil, err
	}
	for _, r := range composeRows {
		m.Compose.Jobs += r.N
		switch r.Status {
		case model.ComposeStatusDone:
			m.Compose.Done += r.N
		case model.ComposeStatusFailed:
			m.Compose.Failed += r.N
		default:
			m.Compose.Active += r.N
		}
	}
	m.Compose.SuccessRate = Rate(m.Compose.Done, m.Compose.Done+m.Compose.Failed)

	var firstRows []FirstComposeRow
	if err := db.Raw(`SELECT fj.user_id AS user_id, fj.status AS first_status,
		MAX(CASE WHEN j.status = ? THEN 1 ELSE 0 END) AS ever_done
		FROM (SELECT user_id, MIN(id) AS id FROM compose_job GROUP BY user_id) f
		JOIN compose_job fj ON fj.id = f.id
		JOIN compose_job j ON j.user_id = f.user_id
		WHERE fj.created_at >= ? AND fj.created_at < ?
		GROUP BY fj.user_id, fj.status`, model.ComposeStatusDone, from, to).Scan(&firstRows).Error; err != nil {
		return nil, err
	}
	fc := SummarizeFirstCompose(firstRows)
	m.Compose.FirstUsers, m.Compose.FirstSucceeded, m.Compose.FirstFailed, m.Compose.EventuallyDone = fc.Users, fc.Succeeded, fc.Failed, fc.EventuallyDone
	m.Compose.FirstSuccessRate = Rate(fc.Succeeded, fc.Succeeded+fc.Failed)
	m.Compose.EventualRate = Rate(fc.EventuallyDone, fc.Users)

	// ---- 每镜重试 ----
	var nodeRows []NodeTaskCount
	if err := db.Model(&model.GenerationTask{}).Select("type, COUNT(*) AS n").
		Scopes(countableTasks).Where("created_at >= ? AND created_at < ? AND canvas_id > 0 AND node_id <> '' AND type IN ?", from, to, creationMediaTypes).
		Group("canvas_id, node_id, type").Scan(&nodeRows).Error; err != nil {
		return nil, err
	}
	rs := SummarizeRetries(nodeRows)
	m.Retries.Nodes, m.Retries.Tasks, m.Retries.AvgPerNode, m.Retries.RetriedNodes = rs.Nodes, rs.Tasks, rs.AvgPerNode, rs.RetriedNodes
	m.Retries.RetriedRate = Rate(rs.RetriedNodes, rs.Nodes)
	m.Retries.Buckets, m.Retries.ByType = rs.Buckets, rs.ByType

	// ---- 引用丢失 ----
	var refRows []RefMissingRow
	if err := db.Model(&model.TrackEvent{}).Select("user_id, props").
		Where("name = ? AND created_at >= ? AND created_at < ?", model.TrackEventRefMissing, from, to).
		Limit(creationMetricsRefEventScan).Scan(&refRows).Error; err != nil {
		return nil, err
	}
	ref := SummarizeRefMissing(refRows)
	m.RefMissing.Events, m.RefMissing.Items, m.RefMissing.Users, m.RefMissing.BySource = ref.Events, ref.Items, ref.Users, ref.BySource
	if m.Tasks.Total > 0 {
		v := math.Round(float64(ref.TaskEvents)*1000/float64(m.Tasks.Total)) / 10
		m.RefMissing.PerHundredTasks = &v
	}

	// ---- 退款补偿 ----
	var backlog struct {
		N      int64
		Points int64
		Oldest int64
	}
	db.Model(&model.GenerationTask{}).
		Select("COUNT(*) AS n, COALESCE(SUM(points_frozen), 0) AS points, COALESCE(MIN(NULLIF(finished_at, 0)), 0) AS oldest").
		Where("status IN ? AND points_frozen > 0", []string{model.TaskStatusFailed, model.TaskStatusCancelled}).Scan(&backlog)
	m.Refund.BacklogTasks, m.Refund.BacklogPoints, m.Refund.BacklogOldest = backlog.N, backlog.Points, backlog.Oldest
	var refunded struct {
		N      int64
		Points int64
	}
	db.Model(&model.PointLog{}).Select("COUNT(*) AS n, COALESCE(SUM(amount), 0) AS points").
		Where("type = ? AND biz_type = ? AND created_at >= ? AND created_at < ?", model.PointLogUnfreeze, model.PointsBizTask, from, to).Scan(&refunded)
	m.Refund.RefundedTasks, m.Refund.RefundedPoints = refunded.N, refunded.Points

	// ---- 长时间无进展 ----
	var active []*model.GenerationTask
	if err := db.Select("id, user_id, type, model, status, progress, created_at, started_at, progress_at").
		Where("status IN ?", []string{model.TaskStatusPending, model.TaskStatusRunning}).
		Order("id asc").Limit(creationMetricsActiveScan).Find(&active).Error; err != nil {
		return nil, err
	}
	m.Stuck.ActiveTasks = int64(len(active))
	m.Stuck.ScanLimitHit = len(active) >= creationMetricsActiveScan
	stuck := FindStuckTasks(active, now.Unix(), idleMinutes)
	m.Stuck.Tasks = int64(len(stuck))
	if len(stuck) > creationMetricsStuckItems {
		stuck = stuck[:creationMetricsStuckItems]
	}
	m.Stuck.Items = stuck
	db.Model(&model.ComposeJob{}).
		Where("(status = ? AND created_at < ?) OR (status = ? AND started_at > 0 AND started_at < ?)",
			model.ComposeStatusQueued, now.Unix()-int64(idleMinutes)*60, model.ComposeStatusRunning, now.Unix()-int64(idleMinutes)*60).
		Count(&m.Stuck.ComposeJobs)

	// ---- 失败分布 ----
	var modelRows []ModelFailStat
	if err := db.Model(&model.GenerationTask{}).
		Select("model, type, COUNT(*) AS total, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed", model.TaskStatusFailed).
		Scopes(countableTasks).Where("created_at >= ? AND created_at < ? AND type IN ?", from, to, creationMediaTypes).
		Group("model, type").Scan(&modelRows).Error; err != nil {
		return nil, err
	}
	m.Failures.Models = TopFailingModels(modelRows, 10)
	m.Failures.Errors = []ErrorStat{}
	db.Model(&model.GenerationTask{}).Select("LEFT(error_msg, 80) AS message, COUNT(*) AS count").
		Scopes(countableTasks).Where("created_at >= ? AND created_at < ? AND type IN ? AND status = ? AND error_msg <> ''", from, to, creationMediaTypes, model.TaskStatusFailed).
		Group("LEFT(error_msg, 80)").Order("count DESC").Limit(8).Scan(&m.Failures.Errors)

	// ---- 按天 ----
	var taskDays, composeDays []DayCount
	db.Model(&model.GenerationTask{}).
		Select("FLOOR((created_at + ?) / 86400) AS day, COUNT(*) AS total, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed, 0 AS done", offset, model.TaskStatusFailed).
		Scopes(countableTasks).Where("created_at >= ? AND created_at < ? AND type IN ?", from, to, creationMediaTypes).
		Group("day").Scan(&taskDays)
	db.Model(&model.ComposeJob{}).
		Select("FLOOR((created_at + ?) / 86400) AS day, COUNT(*) AS total, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS done",
			offset, model.ComposeStatusFailed, model.ComposeStatusDone).
		Where("created_at >= ? AND created_at < ?", from, to).
		Group("day").Scan(&composeDays)
	m.Daily = BuildCreationDays(from, days, now.Location(), offset, taskDays, composeDays)
	return m, nil
}

func addTaskStatus(st *TaskTypeStat, status string, n int64) {
	st.Total += n
	switch status {
	case model.TaskStatusSucceeded:
		st.Succeeded += n
	case model.TaskStatusFailed:
		st.Failed += n
	case model.TaskStatusCancelled:
		st.Cancelled += n
	default:
		st.Active += n
	}
}

type FirstComposeRow struct {
	UserId      int
	FirstStatus int
	EverDone    int
}

type FirstComposeSummary struct {
	Users, Succeeded, Failed, EventuallyDone int64
}

func SummarizeFirstCompose(rows []FirstComposeRow) FirstComposeSummary {
	var s FirstComposeSummary
	for _, r := range rows {
		s.Users++
		switch r.FirstStatus {
		case model.ComposeStatusDone:
			s.Succeeded++
		case model.ComposeStatusFailed:
			s.Failed++
		}
		if r.EverDone > 0 {
			s.EventuallyDone++
		}
	}
	return s
}

type NodeTaskCount struct {
	Type string
	N    int64
}

type RetrySummary struct {
	Nodes, Tasks, RetriedNodes int64
	AvgPerNode                 float64
	Buckets                    []RetryBucket
	ByType                     []RetryTypeStat
}

// SummarizeRetries 每个节点的生成次数分桶：1 次、2 次、3 次、4-5 次、6 次及以上。
func SummarizeRetries(rows []NodeTaskCount) RetrySummary {
	labels := []string{"1", "2", "3", "4-5", "6+"}
	buckets := make([]RetryBucket, len(labels))
	for i, l := range labels {
		buckets[i].Label = l
	}
	types := map[string]*RetryTypeStat{}
	var s RetrySummary
	for _, r := range rows {
		if r.N <= 0 {
			continue
		}
		s.Nodes++
		s.Tasks += r.N
		if r.N > 1 {
			s.RetriedNodes++
		}
		idx := 4
		switch {
		case r.N == 1:
			idx = 0
		case r.N == 2:
			idx = 1
		case r.N == 3:
			idx = 2
		case r.N <= 5:
			idx = 3
		}
		buckets[idx].Nodes++
		ts := types[r.Type]
		if ts == nil {
			ts = &RetryTypeStat{Type: r.Type}
			types[r.Type] = ts
		}
		ts.Nodes++
		ts.Tasks += r.N
	}
	if s.Nodes > 0 {
		s.AvgPerNode = math.Round(float64(s.Tasks)*100/float64(s.Nodes)) / 100
	}
	s.Buckets = buckets
	s.ByType = []RetryTypeStat{}
	for _, t := range creationMediaTypes {
		if ts := types[t]; ts != nil {
			ts.AvgPerNode = math.Round(float64(ts.Tasks)*100/float64(ts.Nodes)) / 100
			s.ByType = append(s.ByType, *ts)
		}
	}
	return s
}

type RefMissingRow struct {
	UserId int
	Props  string
}

type RefMissingSummary struct {
	Events, Items, Users int64
	// TaskEvents 生成任务相关的丢失（不含合成镜头），用来算每百任务丢失次数
	TaskEvents int64
	BySource   []RefMissingSource
}

func SummarizeRefMissing(rows []RefMissingRow) RefMissingSummary {
	var s RefMissingSummary
	users := map[int]bool{}
	sourceUsers := map[string]map[int]bool{}
	bySource := map[string]*RefMissingSource{}
	for _, r := range rows {
		var p struct {
			Source string  `json:"source"`
			Count  float64 `json:"count"`
		}
		_ = json.Unmarshal([]byte(r.Props), &p)
		if p.Source == "" {
			p.Source = "unknown"
		}
		count := int64(p.Count)
		if count <= 0 {
			count = 1
		}
		s.Events++
		s.Items += count
		if p.Source != "compose_clip" {
			s.TaskEvents++
		}
		users[r.UserId] = true
		src := bySource[p.Source]
		if src == nil {
			src = &RefMissingSource{Source: p.Source}
			bySource[p.Source] = src
			sourceUsers[p.Source] = map[int]bool{}
		}
		src.Events++
		src.Items += count
		sourceUsers[p.Source][r.UserId] = true
	}
	s.Users = int64(len(users))
	s.BySource = []RefMissingSource{}
	for k, v := range bySource {
		v.Users = int64(len(sourceUsers[k]))
		s.BySource = append(s.BySource, *v)
	}
	sort.Slice(s.BySource, func(i, j int) bool {
		if s.BySource[i].Events != s.BySource[j].Events {
			return s.BySource[i].Events > s.BySource[j].Events
		}
		return s.BySource[i].Source < s.BySource[j].Source
	})
	return s
}

// FindStuckTasks 进行中且超过 idleMinutes 没有进展的任务，按空闲时间倒序。
// 最近进展时间 = max(进度前进时间, 开始时间, 创建时间)。
func FindStuckTasks(tasks []*model.GenerationTask, now int64, idleMinutes int) []StuckTask {
	out := []StuckTask{}
	limit := int64(idleMinutes) * 60
	for _, t := range tasks {
		last := t.CreatedAt
		if t.StartedAt > last {
			last = t.StartedAt
		}
		if t.ProgressAt > last {
			last = t.ProgressAt
		}
		idle := now - last
		if idle < limit {
			continue
		}
		out = append(out, StuckTask{Id: t.Id, UserId: t.UserId, Type: t.Type, Model: t.Model, Status: t.Status, Progress: t.Progress, CreatedAt: t.CreatedAt, LastAt: last, IdleMinutes: idle / 60})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].IdleMinutes > out[j].IdleMinutes })
	return out
}

// TopFailingModels 失败次数最多的模型（有失败的才列），并列时失败率高的在前。
func TopFailingModels(rows []ModelFailStat, n int) []ModelFailStat {
	out := []ModelFailStat{}
	for _, r := range rows {
		if r.Failed <= 0 {
			continue
		}
		r.FailRate = Rate(r.Failed, r.Total)
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Failed != out[j].Failed {
			return out[i].Failed > out[j].Failed
		}
		return *out[i].FailRate > *out[j].FailRate
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

type DayCount struct {
	Day    int64
	Total  int64
	Failed int64
	Done   int64
}

// BuildCreationDays 补齐每一天（没数据的天也要有一行，图表才连续）。day 为 (unix + 时区偏移) / 86400。
func BuildCreationDays(from int64, days int, loc *time.Location, offset int, tasks, compose []DayCount) []CreationDay {
	taskBy := map[int64]DayCount{}
	for _, d := range tasks {
		taskBy[d.Day] = d
	}
	composeBy := map[int64]DayCount{}
	for _, d := range compose {
		composeBy[d.Day] = d
	}
	out := make([]CreationDay, 0, days)
	start := time.Unix(from, 0).In(loc)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		key := (day.Unix() + int64(offset)) / 86400
		t, c := taskBy[key], composeBy[key]
		out = append(out, CreationDay{
			Day: day.Format("01-02"), Tasks: t.Total, TasksFailed: t.Failed,
			Compose: c.Total, ComposeDone: c.Done, ComposeFailed: c.Failed,
		})
	}
	return out
}

// countableTasks 生成统计口径：排除扣积分前就被拒的任务（积分不足时任务行已插入、直接标失败，
// 从没提交给模型），否则会被算成模型失败、拉低成功率。
func countableTasks(db *gorm.DB) *gorm.DB {
	return db.Where("NOT (status = ? AND COALESCE(started_at, 0) = 0 AND COALESCE(error_msg, '') LIKE ?)", model.TaskStatusFailed, "%积分不足%")
}
