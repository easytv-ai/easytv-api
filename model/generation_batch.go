package model

import (
	"encoding/json"
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// GenerationBatch 服务端批量生成（API.md 13）：一次提交整张画布多层节点，按依赖顺序在并发空出来时逐个提交，
// 浏览器关掉也继续跑；支持暂停 / 继续 / 取消 / 只重跑失败 / 预算上限。
type GenerationBatch struct {
	Id        int `json:"id"`
	UserId    int `json:"user_id" gorm:"index"`
	CanvasId  int `json:"canvas_id" gorm:"index"`
	ProjectId int `json:"project_id" gorm:"default:0"`
	TeamId    int `json:"team_id" gorm:"index;default:0;comment:创建时所在团队空间（API.md 20），worker 提交的任务沿用它扣费"`
	// WorkerTag 联调实例标签（环境变量 WORKER_TAG，见 common.WorkerTag）：worker 只领同标签的作业，生产为空串
	WorkerTag      string `json:"-" gorm:"type:varchar(32);index;default:'';comment:联调实例标签，worker 只领同标签作业"`
	Status         string `json:"status" gorm:"type:varchar(20);index;default:'running';comment:running/paused/done/cancelled"`
	BudgetPoints   int    `json:"budget_points" gorm:"default:0;comment:预算上限（积分），0 不限"`
	EstimatePoints int    `json:"estimate_points" gorm:"default:0;comment:创建时估算的总积分"`
	SpentPoints    int    `json:"spent_points" gorm:"default:0;comment:已提交任务预扣的积分"`
	Total          int    `json:"total" gorm:"default:0"`
	Succeeded      int    `json:"succeeded" gorm:"default:0"`
	Failed         int    `json:"failed" gorm:"default:0"`
	PauseReason    string `json:"pause_reason" gorm:"type:varchar(255);default:''"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt      int64  `json:"updated_at" gorm:"bigint"`
	FinishedAt     int64  `json:"finished_at" gorm:"bigint;default:0"`

	Items []*GenerationBatchItem `json:"items,omitempty" gorm:"-"`
}

func (GenerationBatch) TableName() string { return "generation_batch" }

// GenerationBatchItem 批次里的一个节点任务。
type GenerationBatchItem struct {
	Id       int    `json:"id"`
	BatchId  int    `json:"batch_id" gorm:"index"`
	NodeId   string `json:"node_id" gorm:"type:varchar(64)"`
	Layer    string `json:"layer" gorm:"type:varchar(20);default:''"`
	Type     string `json:"type" gorm:"type:varchar(20)"`
	Model    string `json:"model" gorm:"type:varchar(100)"`
	Prompt   string `json:"prompt" gorm:"type:text"`
	Params   string `json:"-" gorm:"type:text"`
	Inputs   string `json:"-" gorm:"type:text;comment:静态输入 [{asset_id,role}]"`
	Depends  string `json:"-" gorm:"type:text;comment:依赖 [{node_id,role}]，提交时取依赖节点的产物作为输入"`
	Status   string `json:"status" gorm:"type:varchar(20);index;default:'waiting';comment:waiting/submitted/succeeded/failed/skipped/cancelled"`
	Estimate int    `json:"estimate" gorm:"default:0"`
	TaskId   int    `json:"task_id" gorm:"default:0"`
	AssetId  int    `json:"asset_id" gorm:"default:0"`
	Attempts int    `json:"attempts" gorm:"default:0"`
	Error    string `json:"error" gorm:"type:varchar(500);default:''"`
	Seq      int    `json:"seq" gorm:"default:0;comment:提交顺序"`

	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (GenerationBatchItem) TableName() string { return "generation_batch_item" }

// BatchDepend 依赖：取 node_id 在本批次（或画布里已有）的产物，以 role 作为输入。
type BatchDepend struct {
	NodeId string `json:"node_id"`
	Role   string `json:"role"`
}

const (
	BatchRunning   = "running"
	BatchPaused    = "paused"
	BatchDone      = "done"
	BatchCancelled = "cancelled"

	BatchItemWaiting   = "waiting"
	BatchItemSubmitted = "submitted"
	BatchItemSucceeded = "succeeded"
	BatchItemFailed    = "failed"
	BatchItemSkipped   = "skipped"
	BatchItemCancelled = "cancelled"
	// BatchItemBlocked 被审批门禁拦下（API.md 26.1）：通过审片或撤销退回后可「只重跑失败的节点」
	BatchItemBlocked = "blocked"

	BatchMaxItems = 80
)

var ErrBatchStatus = errors.New("批量任务当前状态不允许该操作")

func (i *GenerationBatchItem) ParseDepends() []BatchDepend {
	var list []BatchDepend
	_ = json.Unmarshal([]byte(i.Depends), &list)
	return list
}

// CreateBatch 批次与条目一起入库。
func CreateBatch(b *GenerationBatch, items []*GenerationBatchItem) error {
	now := common.GetTimestamp()
	b.CreatedAt, b.UpdatedAt, b.Total = now, now, len(items)
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(b).Error; err != nil {
			return err
		}
		for i, it := range items {
			it.BatchId, it.Seq, it.UpdatedAt = b.Id, i, now
		}
		if len(items) == 0 {
			return nil
		}
		return tx.CreateInBatches(items, 50).Error
	})
}

// GetActiveBatchByCanvas 画布上进行中 / 暂停中的批次。
func GetActiveBatchByCanvas(userId, canvasId int) (*GenerationBatch, error) {
	b := &GenerationBatch{}
	err := DB.Where("user_id = ? AND canvas_id = ? AND status IN ?", userId, canvasId, []string{BatchRunning, BatchPaused}).Order("id desc").First(b).Error
	return b, wrapNotFound(err)
}

// GetLatestBatchByCanvas 画布最近一个批次（任意状态）。
func GetLatestBatchByCanvas(userId, canvasId int) (*GenerationBatch, error) {
	b := &GenerationBatch{}
	err := DB.Where("user_id = ? AND canvas_id = ?", userId, canvasId).Order("id desc").First(b).Error
	return b, wrapNotFound(err)
}

func GetUserBatch(userId, id int) (*GenerationBatch, error) {
	b := &GenerationBatch{}
	err := DB.Where("id = ? AND user_id = ?", id, userId).First(b).Error
	return b, wrapNotFound(err)
}

func GetBatchItems(batchId int) ([]*GenerationBatchItem, error) {
	var list []*GenerationBatchItem
	err := DB.Where("batch_id = ?", batchId).Order("seq asc").Find(&list).Error
	return list, err
}

// GetRunningOrPausedBatches worker 每轮处理的批次（暂停中的也要同步已提交任务的结果）。
func GetRunningOrPausedBatches(limit int) ([]*GenerationBatch, error) {
	var list []*GenerationBatch
	err := DB.Where("status IN ? AND worker_tag = ?", []string{BatchRunning, BatchPaused}, common.WorkerTag()).Order("id asc").Limit(limit).Find(&list).Error
	return list, err
}

// UpdateBatchItem 按当前状态条件更新（状态机：避免并发的暂停 / 取消与 worker 互相覆盖）。
func UpdateBatchItem(id int, from []string, fields map[string]any) bool {
	fields["updated_at"] = common.GetTimestamp()
	res := DB.Model(&GenerationBatchItem{}).Where("id = ? AND status IN ?", id, from).Updates(fields)
	return res.Error == nil && res.RowsAffected > 0
}

// TransitBatch 批次状态转换（带来源状态条件）。
func TransitBatch(id int, from []string, fields map[string]any) bool {
	fields["updated_at"] = common.GetTimestamp()
	res := DB.Model(&GenerationBatch{}).Where("id = ? AND status IN ?", id, from).Updates(fields)
	return res.Error == nil && res.RowsAffected > 0
}

// UpdateBatchCounters 刷新计数与已花积分。
func UpdateBatchCounters(id int, fields map[string]any) {
	fields["updated_at"] = common.GetTimestamp()
	DB.Model(&GenerationBatch{}).Where("id = ?", id).Updates(fields)
}

// SetBatchItemsStatus 批量改条目状态（取消 / 重跑失败）。
func SetBatchItemsStatus(batchId int, from []string, fields map[string]any) int64 {
	fields["updated_at"] = common.GetTimestamp()
	res := DB.Model(&GenerationBatchItem{}).Where("batch_id = ? AND status IN ?", batchId, from).Updates(fields)
	return res.RowsAffected
}
