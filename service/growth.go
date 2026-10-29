package service

import (
	"fmt"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 增长域积分发放（T7）：礼包与任务。数值全部来自 setting.GetGrowthConfig()。
// point_log 的 biz_no 记固定键（setting.GrowthReason*），reason 记给用户看的中文。

// GiftPointsWithBiz 系统赠送，biz_no 可指定（GiftPoints 的 "gift" 太粗，增长域按键筛选）。
func GiftPointsWithBiz(userId, points, validDays int, bizNo, reason string) error {
	if userId <= 0 || points <= 0 {
		return nil
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.GrantPoints(tx, userId, model.LotSourceGift, points, common.GetTimestamp()+int64(validDays)*86400, 0, 0,
			model.PointLogGrant, model.PointsBizSystem, bizNo, reason, 0)
		return err
	})
}

// GrantSignupGift 注册礼包；GrowthSignupPoints 为 0 时不发。
func GrantSignupGift(userId int) {
	cfg := setting.GetGrowthConfig()
	if cfg.SignupPoints <= 0 {
		return
	}
	if err := GiftPointsWithBiz(userId, cfg.SignupPoints, cfg.SignupValidDays, setting.GrowthReasonSignupGift,
		fmt.Sprintf("新人礼包 %d 积分", cfg.SignupPoints)); err != nil {
		common.SysError(fmt.Sprintf("signup gift failed user=%d: %v", userId, err))
	}
}

// 任务中文名（给 point_log.reason 与 GET /user/tasks 用）。
var growthTaskTitles = map[string]string{
	setting.GrowthTaskKeyFirstCanvas: "创建第一个画布",
	setting.GrowthTaskKeyFirstVideo:  "生成第一条视频",
	setting.GrowthTaskKeyFirstWork:   "发布第一个作品",
	setting.GrowthTaskKeyFirstInvite: "邀请第一位好友",
}

// GrowthTaskTitle 任务展示名。
func GrowthTaskTitle(key string) string {
	if t, ok := growthTaskTitles[key]; ok {
		return t
	}
	return key
}

// CompleteUserTask 标记任务完成并发积分；幂等（重复调用不重复发），返回本次是否发放。
func CompleteUserTask(userId int, key string) bool {
	if userId <= 0 {
		return false
	}
	cfg := setting.GetGrowthConfig()
	points, ok := cfg.TaskPoints[key]
	if !ok {
		return false
	}
	granted := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		inserted, err := model.InsertUserTaskOnce(tx, userId, key, points)
		if err != nil || !inserted {
			return err
		}
		granted = true
		if points <= 0 {
			return nil
		}
		_, err = model.GrantPoints(tx, userId, model.LotSourceGift, points, common.GetTimestamp()+int64(cfg.TaskValidDays)*86400, 0, 0,
			model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonTaskPrefix+key,
			fmt.Sprintf("新手任务「%s」奖励 %d 积分", GrowthTaskTitle(key), points), 0)
		return err
	})
	if err != nil {
		common.SysError(fmt.Sprintf("complete user task failed user=%d key=%s: %v", userId, key, err))
		return false
	}
	return granted
}

// 事件 → 任务键：first_* 事件首次写入时自动完成对应任务（TrackOnce 里调用）。
var eventTaskKeys = map[string]string{
	model.TrackEventFirstCanvas: setting.GrowthTaskKeyFirstCanvas,
	model.TrackEventFirstVideo:  setting.GrowthTaskKeyFirstVideo,
	model.TrackEventFirstWork:   setting.GrowthTaskKeyFirstWork,
}

func onFirstEvent(userId int, name string) {
	if key, ok := eventTaskKeys[name]; ok {
		CompleteUserTask(userId, key)
	}
	if name == model.TrackEventFirstVideo {
		onInviteeFirstVideo(userId)
	}
}

// UserTaskView GET /user/tasks 的一行。
type UserTaskView struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Points int    `json:"points"`
	Status int    `json:"status"` // 0 未完成 1 已完成
	DoneAt int64  `json:"done_at"`
}

// UserTasksSummary GET /user/tasks 响应。
type UserTasksSummary struct {
	Tasks        []UserTaskView `json:"tasks"`
	TotalPoints  int            `json:"total_points"`
	EarnedPoints int            `json:"earned_points"`
	Done         int            `json:"done"`
	ValidDays    int            `json:"valid_days"`
}

// GetUserTasksSummary 任务列表（按 setting.GrowthTaskKeys 顺序，未完成项用当前配置的积分）。
func GetUserTasksSummary(userId int) (*UserTasksSummary, error) {
	cfg := setting.GetGrowthConfig()
	done, err := model.GetUserTasks(userId)
	if err != nil {
		return nil, err
	}
	out := &UserTasksSummary{ValidDays: cfg.TaskValidDays}
	for _, key := range setting.GrowthTaskKeys {
		v := UserTaskView{Key: key, Title: GrowthTaskTitle(key), Points: cfg.TaskPoints[key]}
		if t := done[key]; t != nil {
			v.Status, v.DoneAt, v.Points = 1, t.DoneAt, t.Points
			out.Done++
			out.EarnedPoints += t.Points
		}
		out.TotalPoints += v.Points
		out.Tasks = append(out.Tasks, v)
	}
	return out, nil
}
