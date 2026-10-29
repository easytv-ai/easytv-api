package model

import (
	"errors"
	"fmt"

	"easytv-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ===== 积分批次 / 流水（API.md 6.1）=====

// PointLot 积分批次：每笔入账一条，消耗按最早过期优先。
type PointLot struct {
	Id        int    `json:"id" gorm:"comment:批次ID"`
	UserId    int    `json:"user_id" gorm:"index:idx_lot_user_exp,priority:1"`
	Source    string `json:"source" gorm:"type:varchar(30);comment:membership_grant/add_on/pack/gift/admin/refund"`
	OrderId   int    `json:"order_id" gorm:"index;default:0"`
	TaskId    int    `json:"task_id" gorm:"default:0"`
	Points    int    `json:"points" gorm:"default:0;comment:入账数量"`
	Remaining int    `json:"remaining" gorm:"default:0;comment:剩余"`
	ExpiresAt int64  `json:"expires_at" gorm:"bigint;index:idx_lot_user_exp,priority:2"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

func (PointLot) TableName() string { return "point_lot" }

// PointLog 积分流水（只增）。
type PointLog struct {
	Id           int    `json:"id" gorm:"comment:流水ID"`
	UserId       int    `json:"user_id" gorm:"index"`
	Type         string `json:"type" gorm:"type:varchar(20);index;comment:grant/freeze/settle/unfreeze/expire/adjust"`
	Amount       int    `json:"amount" gorm:"default:0;comment:正入负出"`
	BalanceAfter int    `json:"balance_after" gorm:"default:0"`
	BizType      string `json:"biz_type" gorm:"type:varchar(20);default:'';comment:order/task/admin/system"`
	BizNo        string `json:"biz_no" gorm:"type:varchar(64);default:''"`
	Reason       string `json:"reason" gorm:"type:varchar(255);default:''"`
	OperatorId   int    `json:"operator_id" gorm:"default:0"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;index"`

	UserName string `json:"user_name,omitempty" gorm:"-"`
}

func (PointLog) TableName() string { return "point_log" }

const (
	LotSourceMembershipGrant = "membership_grant"
	LotSourceAddOn           = "add_on"
	LotSourcePack            = "pack"
	LotSourceGift            = "gift"
	LotSourceAdmin           = "admin"
	LotSourceRefund          = "refund"

	PointLogGrant    = "grant"
	PointLogFreeze   = "freeze"
	PointLogSettle   = "settle"
	PointLogUnfreeze = "unfreeze"
	PointLogExpire   = "expire"
	PointLogAdjust   = "adjust"

	PointsBizOrder  = "order"
	PointsBizTask   = "task"
	PointsBizAdmin  = "admin"
	PointsBizSystem = "system"

	// 各类批次默认有效期（秒）
	LotTTLMembershipGrant = 31 * 86400
	LotTTLRefund          = 365 * 86400
	LotTTLAdmin           = 365 * 86400
)

// ErrInsufficientPoints 积分不足，Need 为缺口。
type ErrInsufficientPoints struct{ Need int }

func (e ErrInsufficientPoints) Error() string {
	return fmt.Sprintf("积分不足，还需 %d 积分", e.Need)
}

// GetPointsBalance 可用余额 = 未过期批次剩余之和（tx 可为 nil）。
func GetPointsBalance(tx *gorm.DB, userId int) int {
	if tx == nil {
		tx = DB
	}
	var sum struct{ Total int }
	tx.Model(&PointLot{}).Select("COALESCE(SUM(remaining),0) AS total").
		Where("user_id = ? AND remaining > 0 AND expires_at > ?", userId, common.GetTimestamp()).Scan(&sum)
	return sum.Total
}

// GetPointsBalances 批量余额（管理端钱包列表）。
func GetPointsBalances(userIds []int) map[int]int {
	out := map[int]int{}
	if len(userIds) == 0 {
		return out
	}
	var rows []struct {
		UserId int
		Total  int
	}
	DB.Model(&PointLot{}).Select("user_id, COALESCE(SUM(remaining),0) AS total").
		Where("user_id IN ? AND remaining > 0 AND expires_at > ?", userIds, common.GetTimestamp()).
		Group("user_id").Scan(&rows)
	for _, r := range rows {
		out[r.UserId] = r.Total
	}
	return out
}

// GetExpiringPoints 未来 days 天内到期的积分数。
func GetExpiringPoints(userId, days int) int {
	now := common.GetTimestamp()
	var sum struct{ Total int }
	DB.Model(&PointLot{}).Select("COALESCE(SUM(remaining),0) AS total").
		Where("user_id = ? AND remaining > 0 AND expires_at > ? AND expires_at <= ?", userId, now, now+int64(days)*86400).Scan(&sum)
	return sum.Total
}

// GetUserLots 用户未过期且有剩余的批次。
func GetUserLots(userId int) (list []*PointLot, err error) {
	err = DB.Where("user_id = ? AND remaining > 0 AND expires_at > ?", userId, common.GetTimestamp()).
		Order("expires_at asc").Find(&list).Error
	return list, err
}

// GrantPoints 入账一个批次并写 grant 流水（在事务 tx 内）。
func GrantPoints(tx *gorm.DB, userId int, source string, points int, expiresAt int64, orderId, taskId int, logType, bizType, bizNo, reason string, operatorId int) (*PointLot, error) {
	if points <= 0 {
		return nil, errors.New("入账积分必须大于 0")
	}
	lot := &PointLot{
		UserId:    userId,
		Source:    source,
		OrderId:   orderId,
		TaskId:    taskId,
		Points:    points,
		Remaining: points,
		ExpiresAt: expiresAt,
		CreatedAt: common.GetTimestamp(),
	}
	if err := tx.Create(lot).Error; err != nil {
		return nil, err
	}
	if logType == "" {
		logType = PointLogGrant
	}
	if err := writePointLog(tx, userId, logType, points, bizType, bizNo, reason, operatorId); err != nil {
		return nil, err
	}
	return lot, nil
}

// ConsumePoints 按最早过期优先扣减 points（在事务 tx 内，行锁），不足返回 ErrInsufficientPoints。
func ConsumePoints(tx *gorm.DB, userId, points int, logType, bizType, bizNo, reason string, operatorId int) error {
	if points <= 0 {
		return errors.New("扣减积分必须大于 0")
	}
	var lots []*PointLot
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND remaining > 0 AND expires_at > ?", userId, common.GetTimestamp()).
		Order("expires_at asc, id asc").Find(&lots).Error
	if err != nil {
		return err
	}
	total := 0
	for _, lot := range lots {
		total += lot.Remaining
	}
	if total < points {
		return ErrInsufficientPoints{Need: points - total}
	}
	left := points
	for _, lot := range lots {
		if left == 0 {
			break
		}
		take := lot.Remaining
		if take > left {
			take = left
		}
		if err := tx.Model(&PointLot{}).Where("id = ?", lot.Id).Update("remaining", gorm.Expr("remaining - ?", take)).Error; err != nil {
			return err
		}
		left -= take
	}
	return writePointLog(tx, userId, logType, -points, bizType, bizNo, reason, operatorId)
}

// RevokeOrderLots 退款：回收该订单批次的未消耗积分，返回回收数量。
func RevokeOrderLots(tx *gorm.DB, userId, orderId int, reason string, operatorId int) (int, error) {
	var lots []*PointLot
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND order_id = ? AND remaining > 0", userId, orderId).Find(&lots).Error; err != nil {
		return 0, err
	}
	revoked := 0
	for _, lot := range lots {
		revoked += lot.Remaining
		if err := tx.Model(&PointLot{}).Where("id = ?", lot.Id).Update("remaining", 0).Error; err != nil {
			return 0, err
		}
	}
	if revoked > 0 {
		if err := writePointLog(tx, userId, PointLogAdjust, -revoked, PointsBizOrder, fmt.Sprintf("%d", orderId), reason, operatorId); err != nil {
			return 0, err
		}
	}
	return revoked, nil
}

// ExpireLots 过期批次剩余清零并按用户写 expire 流水，返回处理批次数。
func ExpireLots(limit int) (int, error) {
	now := common.GetTimestamp()
	var lots []*PointLot
	if err := DB.Where("remaining > 0 AND expires_at <= ?", now).Order("id asc").Limit(limit).Find(&lots).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, lot := range lots {
		err := DB.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&PointLot{}).Where("id = ? AND remaining > 0", lot.Id).Update("remaining", 0)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return nil
			}
			return writePointLog(tx, lot.UserId, PointLogExpire, -lot.Remaining, PointsBizSystem, fmt.Sprintf("lot:%d", lot.Id), "积分到期作废", 0)
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func writePointLog(tx *gorm.DB, userId int, logType string, amount int, bizType, bizNo, reason string, operatorId int) error {
	return tx.Create(&PointLog{
		UserId:       userId,
		Type:         logType,
		Amount:       amount,
		BalanceAfter: GetPointsBalance(tx, userId),
		BizType:      bizType,
		BizNo:        bizNo,
		Reason:       reason,
		OperatorId:   operatorId,
		CreatedAt:    common.GetTimestamp(),
	}).Error
}

// PointLogFilter 流水过滤。
type PointLogFilter struct {
	UserId  int
	Type    string
	BizType string
	// TeamTasks >0 时只取这个团队空间任务的流水（biz_type=task 且 biz_no 是 team_id=TeamTasks 的任务单号，API.md 20）
	TeamTasks int
}

func GetPointLogs(filter PointLogFilter, pageInfo *common.PageInfo) (list []*PointLog, total int64, err error) {
	query := DB.Model(&PointLog{})
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.BizType != "" {
		query = query.Where("biz_type = ?", filter.BizType)
	}
	if filter.TeamTasks > 0 {
		teamTaskNos := DB.Model(&GenerationTask{}).Select("task_no").Where("team_id = ?", filter.TeamTasks)
		query = query.Where("biz_type = ? AND biz_no IN (?)", PointsBizTask, teamTaskNos)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(list))
	for _, l := range list {
		ids = append(ids, l.UserId)
	}
	names := GetUserNicknames(ids)
	for _, l := range list {
		l.UserName = names[l.UserId]
	}
	return list, total, nil
}

// GetFrozenPoints 进行中任务的预扣总额（展示用）。
func GetFrozenPoints(userId int) int {
	var sum struct{ Total int }
	DB.Model(&GenerationTask{}).Select("COALESCE(SUM(points_frozen),0) AS total").
		Where("user_id = ? AND status IN ?", userId, []string{TaskStatusPending, TaskStatusRunning}).Scan(&sum)
	return sum.Total
}
