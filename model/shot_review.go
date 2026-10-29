package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ShotReview 镜头候选的审片结论（API.md 25.5）：某个候选视频通过 / 退回，带退回原因与批注。
// 按 (canvas_id, asset_id) 唯一：一个候选只有一条结论，改就覆盖。
type ShotReview struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"index;comment:审片人"`
	CanvasId  int    `json:"canvas_id" gorm:"uniqueIndex:uk_shot_review"`
	NodeId    string `json:"node_id" gorm:"type:varchar(64);index"`
	AssetId   int    `json:"asset_id" gorm:"uniqueIndex:uk_shot_review"`
	TaskId    int    `json:"task_id" gorm:"default:0"`
	Status    int    `json:"status" gorm:"comment:1 通过 -1 退回"`
	Reason    string `json:"reason" gorm:"type:varchar(32);default:'';comment:退回原因：identity/performance/lipsync/composition/continuity/duration/technical"`
	Note      string `json:"note" gorm:"type:varchar(500);default:''"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}

func (ShotReview) TableName() string { return "shot_review" }

const (
	ShotReviewApproved = 1
	ShotReviewRejected = -1
)

// ShotReviewReasons 退回原因白名单（复审 G05 / G07）。
var ShotReviewReasons = []string{"identity", "performance", "lipsync", "composition", "continuity", "duration", "technical"}

func IsShotReviewReason(v string) bool {
	for _, r := range ShotReviewReasons {
		if r == v {
			return true
		}
	}
	return false
}

// UpsertShotReview 写入 / 覆盖一条结论。
func UpsertShotReview(r *ShotReview) error {
	now := common.GetTimestamp()
	r.UpdatedAt = now
	if r.CreatedAt == 0 {
		r.CreatedAt = now
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "canvas_id"}, {Name: "asset_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "node_id", "task_id", "status", "reason", "note", "updated_at"}),
	}).Create(r).Error
}

// DeleteShotReview 撤销结论（回到待审）。
func DeleteShotReview(canvasId, assetId int) error {
	return DB.Where("canvas_id = ? AND asset_id = ?", canvasId, assetId).Delete(&ShotReview{}).Error
}

// GetShotReviews 画布下全部结论，按候选资产 id 索引。
func GetShotReviews(canvasId int) (map[int]*ShotReview, error) {
	var list []*ShotReview
	if err := DB.Where("canvas_id = ?", canvasId).Find(&list).Error; err != nil {
		return nil, err
	}
	out := make(map[int]*ShotReview, len(list))
	for _, r := range list {
		out[r.AssetId] = r
	}
	return out, nil
}

// GetShotReview 单条结论；没有返回 ErrNotFound。
func GetShotReview(canvasId, assetId int) (*ShotReview, error) {
	r := &ShotReview{}
	if err := DB.Where("canvas_id = ? AND asset_id = ?", canvasId, assetId).First(r).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}
