package model

import (
	"easytv-api/common"

	"gorm.io/gorm"
)

// UserTask 新手任务完成记录（T7）：只记已完成项，未完成的由 setting.GrowthTaskKeys 补齐展示。
type UserTask struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"uniqueIndex:idx_user_task,priority:1"`
	TaskKey   string `json:"task_key" gorm:"type:varchar(32);uniqueIndex:idx_user_task,priority:2"`
	Points    int    `json:"points" gorm:"default:0;comment:发放积分"`
	DoneAt    int64  `json:"done_at" gorm:"bigint"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

func (UserTask) TableName() string { return "user_task" }

// GetUserTasks 用户已完成的任务，按 task_key 索引。
func GetUserTasks(userId int) (map[string]*UserTask, error) {
	var list []*UserTask
	if err := DB.Where("user_id = ?", userId).Find(&list).Error; err != nil {
		return nil, err
	}
	m := make(map[string]*UserTask, len(list))
	for _, t := range list {
		m[t.TaskKey] = t
	}
	return m, nil
}

// InsertUserTaskOnce 在事务里写完成记录；已存在返回 (false, nil)，靠唯一索引保证并发下只成功一次。
func InsertUserTaskOnce(tx *gorm.DB, userId int, key string, points int) (bool, error) {
	now := common.GetTimestamp()
	res := tx.Exec(
		"INSERT IGNORE INTO user_task (user_id, task_key, points, done_at, created_at) VALUES (?, ?, ?, ?, ?)",
		userId, key, points, now, now,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
