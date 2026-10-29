package model

import (
	"errors"
	"fmt"
	"time"

	"easytv-api/common"

	"gorm.io/gorm"
)

// GenerationTask 生成任务：一次模型调用的完整生命周期。见 API.md 1.4。
type GenerationTask struct {
	Id             int    `json:"id" gorm:"comment:任务ID"`
	TaskNo         string `json:"task_no" gorm:"type:varchar(32);uniqueIndex;comment:对外任务单号"`
	UserId         int    `json:"user_id" gorm:"index;comment:发起用户ID"`
	ProjectId      int    `json:"project_id" gorm:"default:0;comment:所属项目ID"`
	CanvasId       int    `json:"canvas_id" gorm:"index;index:idx_task_canvas_node,priority:1;default:0;comment:所属画布ID"`
	NodeId         string `json:"node_id" gorm:"type:varchar(64);index:idx_task_canvas_node,priority:2;default:'';comment:画布节点ID"`
	SkillId        int    `json:"skill_id" gorm:"index;default:0;comment:使用的技能 ID（T26 作者分成）"`
	ParentTaskId   int    `json:"parent_task_id" gorm:"index;default:0;comment:续写来源任务（F4）"`
	RetakeOf       int    `json:"retake_of" gorm:"index;default:0;comment:重拍 / 超分来源任务（F4）"`
	Lang           string `json:"lang" gorm:"type:varchar(10);default:'';comment:多语言配音 / 字幕语种（F5）"`
	Type           string `json:"type" gorm:"type:varchar(20);index;comment:类型：text/image/video/audio"`
	Provider       string `json:"provider" gorm:"type:varchar(50);default:'';comment:厂商"`
	Model          string `json:"model" gorm:"type:varchar(100);comment:模型标识"`
	Prompt         string `json:"prompt" gorm:"type:text;comment:提示词"`
	Params         string `json:"-" gorm:"type:text;comment:模型参数 JSON"`
	Inputs         string `json:"-" gorm:"type:text;comment:输入素材 JSON"`
	Status         string `json:"status" gorm:"type:varchar(20);index;default:'pending';comment:pending/running/succeeded/failed/cancelled"`
	Progress       int    `json:"progress" gorm:"default:0;comment:进度 0-100"`
	ProviderTaskId string `json:"provider_task_id" gorm:"size:100;index;comment:网关侧任务 ID，重启后据此恢复轮询"`
	ResultAssetId  int    `json:"result_asset_id" gorm:"default:0;comment:结果资产ID"`
	ResultUrl      string `json:"result_url" gorm:"type:varchar(1000);default:'';comment:结果地址"`
	ResultText     string `json:"result_text" gorm:"type:text;comment:文本结果（text 任务）"`
	ErrorMsg       string `json:"error_msg" gorm:"type:varchar(500);default:'';comment:失败原因"`
	PointsFrozen   int    `json:"points_frozen" gorm:"default:0;comment:预扣积分（创建时扣，失败退回）"`
	PayerId        int    `json:"payer_id" gorm:"default:0;comment:实际扣积分的账户（团队成员为 owner）"`
	TeamId         int    `json:"team_id" gorm:"index;default:0;comment:所在团队空间（0 个人空间，API.md 20），扣费与产物归属按它"`
	CostPoints     int    `json:"cost_points" gorm:"default:0;comment:消耗积分"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	StartedAt      int64  `json:"started_at" gorm:"bigint;default:0;comment:开始时间"`
	FinishedAt     int64  `json:"finished_at" gorm:"bigint;default:0;comment:结束时间"`
	// ProgressAt 最近一次开始执行或进度前进的时间，创作指标据此找「长时间没有进展」的任务（API.md 17）
	ProgressAt int64 `json:"progress_at" gorm:"bigint;default:0;comment:最近一次进度前进时间"`
}

func (GenerationTask) TableName() string { return "generation_task" }

const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusSucceeded = "succeeded"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"

	TaskTypeText  = "text"
	TaskTypeImage = "image"
	TaskTypeVideo = "video"
	TaskTypeAudio = "audio"
)

var ErrTaskStatus = errors.New("任务当前状态不允许该操作")

// IsValidTaskType 校验任务类型。
func IsValidTaskType(t string) bool {
	switch t {
	case TaskTypeText, TaskTypeImage, TaskTypeVideo, TaskTypeAudio:
		return true
	}
	return false
}

// IsValidTaskStatus 校验状态过滤参数。
func IsValidTaskStatus(s string) bool {
	switch s {
	case TaskStatusPending, TaskStatusRunning, TaskStatusSucceeded, TaskStatusFailed, TaskStatusCancelled:
		return true
	}
	return false
}

// NewTaskNo 生成对外任务单号。
func NewTaskNo() string {
	return fmt.Sprintf("GT%d%s", common.GetTimestamp(), common.GetRandomString(6))
}

// Insert 创建任务（初始 pending）。
func (t *GenerationTask) Insert() error {
	t.CreatedAt = common.GetTimestamp()
	if t.TaskNo == "" {
		t.TaskNo = NewTaskNo()
	}
	if t.Status == "" {
		t.Status = TaskStatusPending
	}
	if t.Params == "" {
		t.Params = "{}"
	}
	if t.Inputs == "" {
		t.Inputs = "[]"
	}
	return DB.Create(t).Error
}

// GetUserTask 按用户 + ID 查询。
func GetUserTask(userId, id int) (*GenerationTask, error) {
	t := &GenerationTask{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// GetTaskById 按 ID 查询（provider 回调用）。
func GetTaskById(id int) (*GenerationTask, error) {
	t := &GenerationTask{}
	if err := DB.Where("id = ?", id).First(t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// TaskFilter 任务列表过滤条件。
type TaskFilter struct {
	UserId   int
	CanvasId int
	Status   string
	Type     string
	Keyword  string
	// Active 只取 pending / running
	Active bool
	// 用户侧空间（API.md 20）：Space=true 时加 team_id = TeamId（0 即个人空间）
	Space  bool
	TeamId int
}

// GetTasks 任务列表（用户侧 UserId > 0；管理端可 0）。
func GetTasks(filter TaskFilter, pageInfo *common.PageInfo) (list []*GenerationTask, total int64, err error) {
	query := DB.Model(&GenerationTask{})
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Space {
		query = query.Where("team_id = ?", filter.TeamId)
	}
	if filter.CanvasId > 0 {
		query = query.Where("canvas_id = ?", filter.CanvasId)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		query = query.Where("(prompt LIKE ? OR model LIKE ? OR task_no LIKE ?)", like, like, like)
	}
	if filter.Active {
		query = query.Where("status IN ?", []string{TaskStatusPending, TaskStatusRunning})
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// NodeCandidate 某个画布节点生成成功过的产物（镜头候选，见 API.md 12.1）。
type NodeCandidate struct {
	TaskId    int     `json:"task_id"`
	NodeId    string  `json:"node_id"`
	AssetId   int     `json:"asset_id"`
	Url       string  `json:"url"`
	CoverUrl  string  `json:"cover_url"`
	Duration  float64 `json:"duration"`
	Model     string  `json:"model"`
	Prompt    string  `json:"prompt"`
	CreatedAt int64   `json:"created_at"`
	// Review 审片结论（API.md 25.5），nil = 待审
	Review *ShotReview `json:"review,omitempty" gorm:"-"`
}

// GetNodeCandidates 画布里这些节点生成成功、产物资产仍在的任务，按节点分组、新的在前，每个节点最多 perNode 条。
func GetNodeCandidates(userIds []int, canvasId int, nodeIds []string, perNode int) (map[string][]NodeCandidate, error) {
	out := map[string][]NodeCandidate{}
	if canvasId <= 0 || len(nodeIds) == 0 {
		return out, nil
	}
	var rows []NodeCandidate
	err := DB.Table("generation_task AS t").
		Select("t.id AS task_id, t.node_id, t.result_asset_id AS asset_id, a.url, a.cover_url, a.duration, t.model, t.prompt, t.finished_at AS created_at").
		Joins("JOIN asset AS a ON a.id = t.result_asset_id AND a.deleted_at IS NULL").
		Where("t.user_id IN ? AND t.canvas_id = ? AND t.node_id IN ? AND t.status = ? AND t.result_asset_id > 0", userIds, canvasId, nodeIds, TaskStatusSucceeded).
		Order("t.id desc").Limit(len(nodeIds) * perNode * 2).Scan(&rows).Error
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		if len(out[r.NodeId]) < perNode {
			out[r.NodeId] = append(out[r.NodeId], r)
		}
	}
	return out, nil
}

// TaskSummary 任务中心概览。
type TaskSummary struct {
	Total        int64 `json:"total"`
	Active       int64 `json:"active"`
	Succeeded    int64 `json:"succeeded"`
	Failed       int64 `json:"failed"`
	Cancelled    int64 `json:"cancelled"`
	PointsToday  int64 `json:"points_today"`
	Points30d    int64 `json:"points_30d"`
	PointsFrozen int64 `json:"points_frozen"`
}

// GetTaskSummary 各状态数量与积分消耗（成功任务的 cost_points），只算 teamId 这个空间（API.md 20，0 个人空间）。
func GetTaskSummary(userId, teamId int) (*TaskSummary, error) {
	s := &TaskSummary{}
	mine := func() *gorm.DB {
		return DB.Model(&GenerationTask{}).Where("user_id = ? AND team_id = ?", userId, teamId)
	}
	var rows []struct {
		Status string
		N      int64
	}
	if err := mine().Select("status, COUNT(*) AS n").Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		s.Total += r.N
		switch r.Status {
		case TaskStatusPending, TaskStatusRunning:
			s.Active += r.N
		case TaskStatusSucceeded:
			s.Succeeded += r.N
		case TaskStatusFailed:
			s.Failed += r.N
		case TaskStatusCancelled:
			s.Cancelled += r.N
		}
	}
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	monthAgo := now.AddDate(0, 0, -30).Unix()
	mine().Select("COALESCE(SUM(cost_points), 0)").
		Where("status = ? AND finished_at >= ?", TaskStatusSucceeded, dayStart).Scan(&s.PointsToday)
	mine().Select("COALESCE(SUM(cost_points), 0)").
		Where("status = ? AND finished_at >= ?", TaskStatusSucceeded, monthAgo).Scan(&s.Points30d)
	mine().Select("COALESCE(SUM(points_frozen), 0)").
		Where("status IN ?", []string{TaskStatusPending, TaskStatusRunning}).Scan(&s.PointsFrozen)
	return s, nil
}

// SetTaskRunning pending → running。
func SetTaskRunning(id int) error {
	now := common.GetTimestamp()
	return transitTask(id, []string{TaskStatusPending}, map[string]any{
		"status":      TaskStatusRunning,
		"started_at":  now,
		"progress_at": now,
	})
}

// SetTaskProgress 更新进度（仅 running）。
// SetTaskProviderRef 记录网关侧任务 ID（视频等异步任务创建成功后写入）。
func SetTaskProviderRef(id int, ref string) error {
	return DB.Model(&GenerationTask{}).Where("id = ?", id).Update("provider_task_id", ref).Error
}

// GetRunningTasksWithProviderRef 仍在网关执行、且记录了网关任务 ID 的 running 任务（重启后恢复轮询用）。
func GetRunningTasksWithProviderRef(taskType string) ([]*GenerationTask, error) {
	var list []*GenerationTask
	err := DB.Where("type = ? AND status = ? AND provider_task_id <> ''", taskType, TaskStatusRunning).Find(&list).Error
	return list, err
}

func SetTaskProgress(id, progress int) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	// 只有进度真的往前走才刷新时间：网关轮询常常反复回报同一个进度，那不算有进展
	DB.Model(&GenerationTask{}).Where("id = ? AND status = ? AND progress < ?", id, TaskStatusRunning, progress).
		Update("progress_at", common.GetTimestamp())
	return transitTask(id, []string{TaskStatusRunning}, map[string]any{"progress": progress})
}

// SetTaskSucceeded running/pending → succeeded，写入结果（媒体任务给 resultUrl，文本任务给 resultText）。
func SetTaskSucceeded(id, resultAssetId int, resultUrl, resultText string, costPoints int) error {
	return SetTaskSucceededTx(DB, id, resultAssetId, resultUrl, resultText, costPoints)
}

// SetTaskSucceededTx 同 SetTaskSucceeded，在调用方事务里执行（资产入库与置成功要一起提交）。
func SetTaskSucceededTx(tx *gorm.DB, id, resultAssetId int, resultUrl, resultText string, costPoints int) error {
	return transitTaskTx(tx, id, []string{TaskStatusPending, TaskStatusRunning}, map[string]any{
		"status":          TaskStatusSucceeded,
		"progress":        100,
		"result_asset_id": resultAssetId,
		"result_url":      resultUrl,
		"result_text":     resultText,
		"cost_points":     costPoints,
		"finished_at":     common.GetTimestamp(),
	})
}

// SetTaskFailed running/pending → failed。
func SetTaskFailed(id int, errMsg string) error {
	return SetTaskFailedTx(DB, id, errMsg)
}

// SetTaskFailedTx 同 SetTaskFailed，在调用方事务里执行（置失败与退积分要一起提交）。
func SetTaskFailedTx(tx *gorm.DB, id int, errMsg string) error {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	return transitTaskTx(tx, id, []string{TaskStatusPending, TaskStatusRunning}, map[string]any{
		"status":      TaskStatusFailed,
		"error_msg":   errMsg,
		"finished_at": common.GetTimestamp(),
	})
}

// CancelUserTask 用户取消（仅 pending/running）。
func CancelUserTask(userId, id int) error {
	return CancelUserTaskTx(DB, userId, id)
}

// CancelUserTaskTx 同 CancelUserTask，在调用方事务里执行。
func CancelUserTaskTx(tx *gorm.DB, userId, id int) error {
	result := tx.Model(&GenerationTask{}).
		Where("id = ? AND user_id = ? AND status IN ?", id, userId, []string{TaskStatusPending, TaskStatusRunning}).
		Updates(map[string]any{
			"status":      TaskStatusCancelled,
			"finished_at": common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskStatus
	}
	return nil
}

func transitTask(id int, from []string, fields map[string]any) error {
	return transitTaskTx(DB, id, from, fields)
}

func transitTaskTx(tx *gorm.DB, id int, from []string, fields map[string]any) error {
	result := tx.Model(&GenerationTask{}).Where("id = ? AND status IN ?", id, from).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskStatus
	}
	return nil
}

// GetStaleTasks 超时任务：pending 早于 pendingBefore、running 早于 runningBefore（按创建/开始时间）。
func GetStaleTasks(pendingBefore, runningBefore int64, limit int) (list []*GenerationTask, err error) {
	err = DB.Where("(status = ? AND created_at < ?) OR (status = ? AND started_at > 0 AND started_at < ?) OR (status = ? AND started_at = 0 AND created_at < ?)",
		TaskStatusPending, pendingBefore, TaskStatusRunning, runningBefore, TaskStatusRunning, runningBefore).
		Order("id asc").Limit(limit).Find(&list).Error
	return list, err
}

// GetUnrefundedTasks 已失败 / 已取消但预扣积分还没退回的任务（退款补偿用）。
// 置终态与退款现在同一事务提交，这里兜的是历史数据与进程在两步之间崩溃的情况。
func GetUnrefundedTasks(limit int) (list []*GenerationTask, err error) {
	err = DB.Where("status IN ? AND points_frozen > 0", []string{TaskStatusFailed, TaskStatusCancelled}).
		Order("id asc").Limit(limit).Find(&list).Error
	return list, err
}

// SumMemberReservedPoints 团队成员进行中任务已预扣、但还没结算进 points_used 的团队积分（F15 月度额度校验用）。
func SumMemberReservedPoints(tx *gorm.DB, userId int) int {
	var n int64
	tx.Model(&GenerationTask{}).Select("COALESCE(SUM(points_frozen), 0)").
		Where("user_id = ? AND payer_id <> user_id AND payer_id > 0 AND status IN ?", userId, []string{TaskStatusPending, TaskStatusRunning}).
		Scan(&n)
	return int(n)
}

// CountActiveTasks 一组用户进行中的任务数（pending + running），套餐并发校验用。
func CountActiveTasks(userIds []int) int64 {
	if len(userIds) == 0 {
		return 0
	}
	var n int64
	DB.Model(&GenerationTask{}).Where("user_id IN ? AND status IN ?", userIds, []string{TaskStatusPending, TaskStatusRunning}).Count(&n)
	return n
}

// MemberUsageRow 团队用量页：单个成员近 N 天用量。
type MemberUsageRow struct {
	Tasks30d     int64
	Points30d    int64
	StorageBytes int64
	ActiveTasks  int64
}

// MemberUsage 按用户汇总：since 之后的任务数、成功任务消耗积分、进行中任务数、资产占用。
func MemberUsage(userIds []int, since int64) map[int]MemberUsageRow {
	out := map[int]MemberUsageRow{}
	if len(userIds) == 0 {
		return out
	}
	var tasks []struct {
		UserId int
		N      int64
		Points int64
		Active int64
	}
	DB.Model(&GenerationTask{}).
		Select("user_id, COUNT(*) AS n, COALESCE(SUM(CASE WHEN status = ? THEN cost_points ELSE 0 END), 0) AS points, COALESCE(SUM(CASE WHEN status IN (?, ?) THEN 1 ELSE 0 END), 0) AS active",
			TaskStatusSucceeded, TaskStatusPending, TaskStatusRunning).
		Where("user_id IN ? AND created_at >= ?", userIds, since).
		Group("user_id").Scan(&tasks)
	for _, t := range tasks {
		row := out[t.UserId]
		row.Tasks30d, row.Points30d, row.ActiveTasks = t.N, t.Points, t.Active
		out[t.UserId] = row
	}
	var sizes []struct {
		UserId int
		Bytes  int64
	}
	DB.Model(&Asset{}).Select("user_id, COALESCE(SUM(size), 0) AS bytes").Where("user_id IN ?", userIds).Group("user_id").Scan(&sizes)
	for _, s := range sizes {
		row := out[s.UserId]
		row.StorageBytes = s.Bytes
		out[s.UserId] = row
	}
	return out
}

// SetTaskResultText 给已完成的任务补文本结果（音频分段配音写入各段起止，API.md 25.1）。
func SetTaskResultText(id int, text string) error {
	return DB.Model(&GenerationTask{}).Where("id = ?", id).Update("result_text", text).Error
}

// NodeCost 某个画布节点上的任务成本汇总（制片报表，API.md 26.3）。
type NodeCost struct {
	NodeId string `json:"node_id"`
	Points int    `json:"points"`
	Tasks  int    `json:"tasks"`
	Failed int    `json:"failed"`
}

// SumTaskCostByNode 画布里每个节点成功任务的净结算积分与次数，以及失败次数（积分已退，不计成本）。
func SumTaskCostByNode(userIds []int, canvasId int) map[string]NodeCost {
	out := map[string]NodeCost{}
	if canvasId <= 0 {
		return out
	}
	var rows []struct {
		NodeId string
		Status string
		Points int
		N      int
	}
	DB.Table("generation_task").
		Select("node_id, status, COALESCE(SUM(cost_points),0) AS points, COUNT(*) AS n").
		Where("user_id IN ? AND canvas_id = ? AND status IN ?", userIds, canvasId, []string{TaskStatusSucceeded, TaskStatusFailed}).
		Group("node_id, status").Scan(&rows)
	for _, r := range rows {
		c := out[r.NodeId]
		c.NodeId = r.NodeId
		if r.Status == TaskStatusSucceeded {
			c.Points += r.Points
			c.Tasks += r.N
		} else {
			c.Failed += r.N
		}
		out[r.NodeId] = c
	}
	return out
}
