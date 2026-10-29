package model

import (
	"errors"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// BonusCode 口令红包 / 兑换码（T15）。一个码可同时带：立即到账积分、首充加赠权益、会员天数；kind 只是主类型标签。
type BonusCode struct {
	Id                     int     `json:"id"`
	Code                   string  `json:"code" gorm:"type:varchar(32);uniqueIndex;comment:口令（存大写，比较忽略大小写）"`
	Kind                   int     `json:"kind" gorm:"default:1;index;comment:1 首充加赠 2 定额积分 3 会员天数"`
	PartnerName            string  `json:"partner_name" gorm:"type:varchar(64);default:'';comment:归属渠道名（展示给用户）"`
	PartnerUserId          int     `json:"partner_user_id" gorm:"index;default:0;comment:归属渠道伙伴用户 ID（M12 结算用，可空）"`
	BonusPoints            int     `json:"bonus_points" gorm:"default:0;comment:领取立即到账积分"`
	BonusValidDays         int     `json:"bonus_valid_days" gorm:"default:30;comment:到账积分有效天数"`
	FirstRechargeRate      float64 `json:"first_recharge_rate" gorm:"type:decimal(5,4);default:0;comment:首充加赠比例，0.2 = 多送 20%"`
	FirstRechargeCapPoints int     `json:"first_recharge_cap_points" gorm:"default:0;comment:首充加赠封顶积分，0 不封顶"`
	RewardValidDays        int     `json:"reward_valid_days" gorm:"default:30;comment:首充权益有效天数，0 不过期"`
	MemberDays             int     `json:"member_days" gorm:"default:0;comment:赠送会员天数"`
	PlanCode               string  `json:"plan_code" gorm:"type:varchar(20);default:'';comment:无会员时按此计划开通试用"`
	MaxClaims              int     `json:"max_claims" gorm:"default:0;comment:可领取次数，0 不限"`
	ClaimedCount           int     `json:"claimed_count" gorm:"default:0"`
	StartsAt               int64   `json:"starts_at" gorm:"bigint;default:0"`
	ExpiresAt              int64   `json:"expires_at" gorm:"bigint;default:0;comment:0 不过期"`
	Status                 int     `json:"status" gorm:"default:1;index;comment:1 启用 0 停用"`
	Remark                 string  `json:"remark" gorm:"type:varchar(255);default:''"`
	CreatedAt              int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt              int64   `json:"updated_at" gorm:"bigint"`
}

func (BonusCode) TableName() string { return "bonus_code" }

const (
	BonusKindFirstRecharge = 1
	BonusKindPoints        = 2
	BonusKindMemberDays    = 3

	BonusBindResultNewBind   = 1
	BonusBindResultSameOwner = 2

	BonusRewardStatusPending    = 0
	BonusRewardStatusGranted    = 1
	BonusRewardStatusExpired    = -1
	BonusRewardStatusIneligible = -2
)

// BonusCodeClaim 领取记录（一人只能领一次口令），字段与 easytv-web app/api/bonus-code.ts 对齐（quota → points）。
type BonusCodeClaim struct {
	Id              int     `json:"id"`
	UserId          int     `json:"user_id" gorm:"uniqueIndex;comment:一人一次"`
	BonusCodeId     int     `json:"bonus_code_id" gorm:"index"`
	Code            string  `json:"code" gorm:"type:varchar(32);index"`
	PartnerUserId   int     `json:"partner_id" gorm:"default:0"`
	BindResult      int     `json:"bind_result" gorm:"default:1"`
	BonusPoints     int     `json:"bonus_points" gorm:"default:0"`
	RewardRate      float64 `json:"reward_rate" gorm:"type:decimal(5,4);default:0"`
	RewardCapPoints int     `json:"reward_cap_points" gorm:"default:0"`
	RewardExpireAt  int64   `json:"reward_expire_at" gorm:"bigint;default:0;comment:首充权益过期时间，0 不过期"`
	RewardStatus    int     `json:"reward_status" gorm:"index;default:0;comment:0 待生效 1 已发 -1 过期 -2 不符合"`
	RewardPoints    int     `json:"reward_points" gorm:"default:0"`
	RewardedAt      int64   `json:"rewarded_at" gorm:"bigint;default:0"`
	MemberDays      int     `json:"member_days" gorm:"default:0"`
	CreatedAt       int64   `json:"created_at" gorm:"bigint"`

	// PartnerName 归属渠道名（非表字段，GET /user/bonus-code/record 从 bonus_code 补上，前端成功态展示用）
	PartnerName string `json:"partner_name" gorm:"-"`
}

func (BonusCodeClaim) TableName() string { return "bonus_code_claim" }

// AttachPartnerName 补齐 partner_name。
func (c *BonusCodeClaim) AttachPartnerName(tx *gorm.DB) {
	if c == nil || c.BonusCodeId <= 0 {
		return
	}
	tx.Model(&BonusCode{}).Select("partner_name").Where("id = ?", c.BonusCodeId).Scan(&c.PartnerName)
}

// NormalizeBonusCode 去空格转大写。
func NormalizeBonusCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// ErrBonusCode* 口令校验错误（message 直接给前端）。
var (
	ErrBonusCodeNotFound   = errors.New("口令不存在")
	ErrBonusCodeNotStarted = errors.New("口令活动尚未开始")
	ErrBonusCodeExpired    = errors.New("口令已过期")
	ErrBonusCodeExhausted  = errors.New("口令已被领完")
	ErrBonusCodeClaimed    = errors.New("每个账号只能领取一次口令")
)

// FindBonusCode 按口令查（不校验状态）。
func FindBonusCode(tx *gorm.DB, code string) (*BonusCode, error) {
	code = NormalizeBonusCode(code)
	if code == "" {
		return nil, ErrBonusCodeNotFound
	}
	var b BonusCode
	if err := tx.Where("code = ?", code).First(&b).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBonusCodeNotFound
		}
		return nil, err
	}
	return &b, nil
}

// Usable 当前是否可领（不含用户维度）。
func (b *BonusCode) Usable(now int64) error {
	if b.Status != 1 {
		return ErrBonusCodeNotFound
	}
	if b.StartsAt > 0 && now < b.StartsAt {
		return ErrBonusCodeNotStarted
	}
	if b.ExpiresAt > 0 && now >= b.ExpiresAt {
		return ErrBonusCodeExpired
	}
	if b.MaxClaims > 0 && b.ClaimedCount >= b.MaxClaims {
		return ErrBonusCodeExhausted
	}
	return nil
}

// IncrBonusCodeClaimed 名额 +1，带上限检查；返回是否成功抢到名额。
func IncrBonusCodeClaimed(tx *gorm.DB, id int) (bool, error) {
	res := tx.Model(&BonusCode{}).Where("id = ? AND (max_claims = 0 OR claimed_count < max_claims)", id).
		Updates(map[string]any{"claimed_count": gorm.Expr("claimed_count + 1"), "updated_at": common.GetTimestamp()})
	return res.RowsAffected > 0, res.Error
}

// GetBonusClaimByUser 用户的领取记录（无则 nil）。
func GetBonusClaimByUser(tx *gorm.DB, userId int) *BonusCodeClaim {
	var c BonusCodeClaim
	if err := tx.Where("user_id = ?", userId).First(&c).Error; err != nil {
		return nil
	}
	return &c
}

// ExpireBonusRewards 把过期的待生效首充权益标记为过期，返回数量。
func ExpireBonusRewards(now int64) int64 {
	res := DB.Model(&BonusCodeClaim{}).
		Where("reward_status = ? AND reward_expire_at > 0 AND reward_expire_at < ?", BonusRewardStatusPending, now).
		Update("reward_status", BonusRewardStatusExpired)
	return res.RowsAffected
}

// BonusCodeFilter 管理端列表筛选。
type BonusCodeFilter struct {
	Kind          int
	Status        int // -1 全部
	PartnerName   string
	PartnerUserId int
	Keyword       string
}

// GetBonusCodes 管理端分页。
func GetBonusCodes(f BonusCodeFilter, pageInfo *common.PageInfo) (list []*BonusCode, total int64, err error) {
	q := DB.Model(&BonusCode{})
	if f.Kind > 0 {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.Status >= 0 {
		q = q.Where("status = ?", f.Status)
	}
	if f.PartnerName != "" {
		q = q.Where("partner_name = ?", f.PartnerName)
	}
	if f.PartnerUserId > 0 {
		q = q.Where("partner_user_id = ?", f.PartnerUserId)
	}
	if f.Keyword != "" {
		like := "%" + strings.TrimSpace(f.Keyword) + "%"
		q = q.Where("(code LIKE ? OR partner_name LIKE ? OR remark LIKE ?)", like, like, like)
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = q.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// BonusCodeStats 单码统计。
type BonusCodeStats struct {
	Claimed      int64   `json:"claimed"`
	Registered   int64   `json:"registered"`
	Paid         int64   `json:"paid"`
	Amount       float64 `json:"amount"`
	RewardPoints int     `json:"reward_points"`
	BonusPoints  int     `json:"bonus_points"`
}

// GetBonusCodeStats 领取数、带来的注册数、付费人数与金额、已发加赠与红包积分。
func GetBonusCodeStats(b *BonusCode) BonusCodeStats {
	var s BonusCodeStats
	DB.Model(&BonusCodeClaim{}).Where("bonus_code_id = ?", b.Id).Count(&s.Claimed)
	DB.Model(&User{}).Where("bonus_code = ?", b.Code).Count(&s.Registered)
	var pay struct {
		Paid   int64
		Amount float64
	}
	DB.Table("track_event").
		Select("COUNT(DISTINCT user_id) AS paid, COALESCE(SUM(JSON_EXTRACT(props, '$.amount')), 0) AS amount").
		Where("name = ? AND bonus_code = ?", TrackEventPaySuccess, b.Code).Scan(&pay)
	s.Paid, s.Amount = pay.Paid, pay.Amount
	var sums struct {
		Reward int
		Bonus  int
	}
	DB.Model(&BonusCodeClaim{}).Select("COALESCE(SUM(reward_points),0) AS reward, COALESCE(SUM(bonus_points),0) AS bonus").
		Where("bonus_code_id = ?", b.Id).Scan(&sums)
	s.RewardPoints, s.BonusPoints = sums.Reward, sums.Bonus
	return s
}

// GetBonusCodesByIds 导出用。
func GetBonusCodesByIds(ids []int) ([]*BonusCode, error) {
	var list []*BonusCode
	if len(ids) == 0 {
		return list, nil
	}
	err := DB.Where("id IN ?", ids).Order("id").Find(&list).Error
	return list, err
}
