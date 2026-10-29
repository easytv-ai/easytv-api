package model

import (
	"errors"
	"time"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Membership 用户当前会员状态（每用户一条，API.md 6.1）。
type Membership struct {
	Id            int    `json:"id" gorm:"comment:ID"`
	UserId        int    `json:"user_id" gorm:"uniqueIndex;comment:用户ID"`
	PlanId        int    `json:"plan_id" gorm:"default:0"`
	PlanCode      string `json:"plan_code" gorm:"type:varchar(20);default:''"`
	PlanName      string `json:"plan_name" gorm:"type:varchar(50);default:''"`
	Edition       string `json:"edition" gorm:"type:varchar(20);default:'creator'"`
	Seats         int    `json:"seats" gorm:"default:1"`
	MonthlyPoints int    `json:"monthly_points" gorm:"default:0;comment:每月发放（已乘席位）"`
	StartedAt     int64  `json:"started_at" gorm:"bigint;default:0"`
	ExpiresAt     int64  `json:"expires_at" gorm:"bigint;index;default:0"`
	NextGrantAt   int64  `json:"next_grant_at" gorm:"bigint;index;default:0;comment:下次发积分"`
	Status        string `json:"status" gorm:"type:varchar(20);index;default:'active';comment:active/expired"`
	SourceOrderId int    `json:"source_order_id" gorm:"default:0"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt     int64  `json:"updated_at" gorm:"bigint"`
}

func (Membership) TableName() string { return "membership" }

const (
	MembershipStatusActive  = "active"
	MembershipStatusExpired = "expired"
)

// GetMembership 用户会员记录（无论状态）。
func GetMembership(userId int) (*Membership, error) {
	m := &Membership{}
	if err := DB.Where("user_id = ?", userId).First(m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// IsActive 有效期内且状态 active。
func (m *Membership) IsActive() bool {
	return m != nil && m.Status == MembershipStatusActive && m.ExpiresAt > common.GetTimestamp()
}

// GetActiveMembership 有效会员，没有返回 nil。
func GetActiveMembership(userId int) *Membership {
	m, err := GetMembership(userId)
	if err != nil || !m.IsActive() {
		return nil
	}
	return m
}

// AddMonths 加 n 个自然月。
func AddMonths(ts int64, months int) int64 {
	return time.Unix(ts, 0).AddDate(0, months, 0).Unix()
}

// UpsertMembership 开通/续费/换计划（在事务 tx 内）。返回是否为续费（同计划延期）。
func UpsertMembership(tx *gorm.DB, userId int, plan *MembershipPlan, edition string, seats, months, orderId int) (renewed bool, m *Membership, err error) {
	now := common.GetTimestamp()
	m = &Membership{}
	findErr := tx.Where("user_id = ?", userId).First(m).Error
	if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return false, nil, findErr
	}
	exists := findErr == nil
	monthly := plan.MonthlyPoints * seats

	if exists && m.IsActive() && m.PlanId == plan.Id && m.Edition == edition && m.Seats == seats {
		// 同计划续费：延期
		m.ExpiresAt = AddMonths(m.ExpiresAt, months)
		m.SourceOrderId = orderId
		m.UpdatedAt = now
		return true, m, tx.Save(m).Error
	}

	// 新开 / 换计划 / 过期后重开：从现在起算
	m.UserId = userId
	m.PlanId = plan.Id
	m.PlanCode = plan.Code
	m.PlanName = plan.Name
	m.Edition = edition
	m.Seats = seats
	m.MonthlyPoints = monthly
	m.StartedAt = now
	m.ExpiresAt = AddMonths(now, months)
	m.NextGrantAt = AddMonths(now, 1)
	m.Status = MembershipStatusActive
	m.SourceOrderId = orderId
	m.UpdatedAt = now
	if !exists {
		m.CreatedAt = now
		return false, m, tx.Create(m).Error
	}
	return false, m, tx.Save(m).Error
}

// ExpireMemberships 到期置 expired，返回数量。
func ExpireMemberships() int64 {
	now := common.GetTimestamp()
	result := DB.Model(&Membership{}).
		Where("status = ? AND expires_at <= ?", MembershipStatusActive, now).
		Updates(map[string]any{"status": MembershipStatusExpired, "updated_at": now})
	return result.RowsAffected
}

// GetMembershipsDueForGrant 到了月发放时间且仍有效的会员。
func GetMembershipsDueForGrant(limit int) (list []*Membership, err error) {
	now := common.GetTimestamp()
	err = DB.Where("status = ? AND next_grant_at > 0 AND next_grant_at <= ? AND next_grant_at < expires_at", MembershipStatusActive, now).
		Order("next_grant_at asc").Limit(limit).Find(&list).Error
	return list, err
}

// AdvanceMembershipGrant 推进下次发放时间（乐观：仅当 next_grant_at 未变）。
func AdvanceMembershipGrant(tx *gorm.DB, id int, current int64) (bool, error) {
	result := tx.Model(&Membership{}).Where("id = ? AND next_grant_at = ?", id, current).Updates(map[string]any{
		"next_grant_at": AddMonths(current, 1),
		"updated_at":    common.GetTimestamp(),
	})
	return result.RowsAffected == 1, result.Error
}

// TerminateMembershipByOrder 退款：来源于该订单的会员立即失效。
func TerminateMembershipByOrder(tx *gorm.DB, orderId int) error {
	now := common.GetTimestamp()
	return tx.Model(&Membership{}).Where("source_order_id = ? AND status = ?", orderId, MembershipStatusActive).
		Updates(map[string]any{"status": MembershipStatusExpired, "expires_at": now, "updated_at": now}).Error
}

// GetMembershipsByUsers 批量取会员（管理端钱包列表）。
func GetMembershipsByUsers(userIds []int) map[int]*Membership {
	out := map[int]*Membership{}
	if len(userIds) == 0 {
		return out
	}
	var list []*Membership
	DB.Where("user_id IN ?", userIds).Find(&list)
	for _, m := range list {
		out[m.UserId] = m
	}
	return out
}
