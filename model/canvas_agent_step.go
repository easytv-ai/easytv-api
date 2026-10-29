package model

import "easytv-api/common"

// CanvasAgentStep 一轮工具调用的留痕（排障与后续 UI 展示用）。
//
// ⚠️ OK 字段绝对不能带 `gorm:"default:true"`（实测复现，gorm v1.25.2）：
// 带 default 标签时 GORM 在 INSERT 里直接省略该列、让库默认值生效，于是 OK=false 与 OK=true 生成同一条 SQL，
// 所有失败步都会被记成 ok=true。去掉 default 标签后零值 false 会被显式写进 INSERT。
// 备选（同样实测通过）是 `OK *bool`，但零值语义更简单，这里用前者。
type CanvasAgentStep struct {
	Id            int    `json:"id"`
	TaskId        int    `json:"task_id" gorm:"index"`
	CanvasId      int    `json:"canvas_id" gorm:"index;default:0"`
	UserId        int    `json:"user_id" gorm:"index;default:0"`
	Round         int    `json:"round" gorm:"default:0"`
	Tool          string `json:"tool" gorm:"type:varchar(32);default:''"`
	ArgsSummary   string `json:"args_summary" gorm:"type:varchar(500);default:''"`
	ResultSummary string `json:"result_summary" gorm:"type:varchar(1000);default:''"`
	OK            bool   `json:"ok"`
	Error         string `json:"error" gorm:"type:varchar(300);default:''"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint"`
}

func (CanvasAgentStep) TableName() string { return "canvas_agent_step" }

// InsertCanvasAgentStep 落一条步骤留痕（CreatedAt 缺省补当前时间）。
func InsertCanvasAgentStep(s *CanvasAgentStep) error {
	if s.CreatedAt == 0 {
		s.CreatedAt = common.GetTimestamp()
	}
	return DB.Create(s).Error
}

// GetCanvasAgentSteps 某个任务的全部步骤，按轮次与写入顺序。
func GetCanvasAgentSteps(taskId int) ([]*CanvasAgentStep, error) {
	var list []*CanvasAgentStep
	err := DB.Where("task_id = ?", taskId).Order("round asc, id asc").Find(&list).Error
	return list, err
}
