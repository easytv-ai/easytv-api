package service

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 口令红包（T15）。领取：立即到账积分 + 建立首充加赠权益 + 赠会员天数；首充：支付履约后按比例加赠，封顶；过期由 RunBillingJobs 标记。

// BonusCodePreview GET /bonus-code/preview 响应。
type BonusCodePreview struct {
	Code                   string  `json:"code"`
	Kind                   int     `json:"kind"`
	PartnerName            string  `json:"partner_name"`
	BonusPoints            int     `json:"bonus_points"`
	FirstRechargeRate      float64 `json:"first_recharge_rate"`
	FirstRechargeCapPoints int     `json:"first_recharge_cap_points"`
	RewardValidDays        int     `json:"reward_valid_days"`
	MemberDays             int     `json:"member_days"`
}

// PreviewBonusCode 校验口令并返回展示信息（无副作用）。
func PreviewBonusCode(code string) (*BonusCodePreview, error) {
	b, err := model.FindBonusCode(model.DB, code)
	if err != nil {
		return nil, err
	}
	if err := b.Usable(common.GetTimestamp()); err != nil {
		return nil, err
	}
	return &BonusCodePreview{
		Code: b.Code, Kind: b.Kind, PartnerName: b.PartnerName, BonusPoints: b.BonusPoints,
		FirstRechargeRate: b.FirstRechargeRate, FirstRechargeCapPoints: b.FirstRechargeCapPoints,
		RewardValidDays: b.RewardValidDays, MemberDays: b.MemberDays,
	}, nil
}

// BonusClaimResult POST /user/bonus-code/claim 响应。
type BonusClaimResult struct {
	BonusPoints     int     `json:"bonus_points"`
	PartnerName     string  `json:"partner_name"`
	BindResult      int     `json:"bind_result"`
	RewardRate      float64 `json:"reward_rate"`
	RewardCapPoints int     `json:"reward_cap_points"`
	RewardExpireAt  int64   `json:"reward_expire_at"`
	RewardStatus    int     `json:"reward_status"`
	MemberDays      int     `json:"member_days"`
}

// ClaimBonusCode 领取口令：一人一次；名额原子扣减；积分、会员、首充权益在同一事务里。
func ClaimBonusCode(userId int, code string) (*BonusClaimResult, error) {
	if userId <= 0 {
		return nil, errors.New("未登录")
	}
	now := common.GetTimestamp()
	out := &BonusClaimResult{}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if model.GetBonusClaimByUser(tx, userId) != nil {
			return model.ErrBonusCodeClaimed
		}
		b, err := model.FindBonusCode(tx, code)
		if err != nil {
			return err
		}
		if err := b.Usable(now); err != nil {
			return err
		}
		ok, err := model.IncrBonusCodeClaimed(tx, b.Id)
		if err != nil {
			return err
		}
		if !ok {
			return model.ErrBonusCodeExhausted
		}
		claim := &model.BonusCodeClaim{
			UserId: userId, BonusCodeId: b.Id, Code: b.Code, PartnerUserId: b.PartnerUserId,
			BindResult: model.BonusBindResultNewBind, BonusPoints: b.BonusPoints, MemberDays: b.MemberDays,
			RewardRate: b.FirstRechargeRate, RewardCapPoints: b.FirstRechargeCapPoints, CreatedAt: now,
		}
		// 首充权益：已付过费的用户不符合；否则待生效（可设过期）
		if b.FirstRechargeRate > 0 {
			if model.CountUserPaidOrders(tx, userId, 0) > 0 {
				claim.RewardStatus = model.BonusRewardStatusIneligible
			} else if b.RewardValidDays > 0 {
				claim.RewardExpireAt = now + int64(b.RewardValidDays)*86400
			}
		} else {
			claim.RewardStatus = model.BonusRewardStatusIneligible
		}
		if err := tx.Create(claim).Error; err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				return model.ErrBonusCodeClaimed
			}
			return err
		}
		if b.BonusPoints > 0 {
			validDays := b.BonusValidDays
			if validDays <= 0 {
				validDays = 30
			}
			if _, err := model.GrantPoints(tx, userId, model.LotSourceGift, b.BonusPoints, now+int64(validDays)*86400, 0, 0,
				model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonBonusCode,
				fmt.Sprintf("口令红包 %s +%d 积分", b.Code, b.BonusPoints), 0); err != nil {
				return err
			}
		}
		if b.MemberDays > 0 {
			if err := grantMemberDays(tx, userId, b.MemberDays, b.PlanCode); err != nil {
				return err
			}
		}
		// 用户表回填口令（注册时未带口令、事后在设置页输入的情况）
		tx.Model(&model.User{}).Where("id = ? AND bonus_code = ''", userId).Update("bonus_code", b.Code)
		out.BonusPoints, out.PartnerName, out.BindResult = b.BonusPoints, b.PartnerName, claim.BindResult
		out.RewardRate, out.RewardCapPoints, out.RewardExpireAt, out.RewardStatus = claim.RewardRate, claim.RewardCapPoints, claim.RewardExpireAt, claim.RewardStatus
		out.MemberDays = b.MemberDays
		return nil
	})
	if err != nil {
		return nil, err
	}
	Track(userId, model.TrackEventBonusClaim, map[string]any{"code": model.NormalizeBonusCode(code), "points": out.BonusPoints, "member_days": out.MemberDays})
	return out, nil
}

// grantMemberDays 有会员则延期，无会员且给了计划编码则按该计划开通指定天数（不发月积分）。
func grantMemberDays(tx *gorm.DB, userId, days int, planCode string) error {
	m := &model.Membership{}
	err := tx.Where("user_id = ?", userId).First(m).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	now := common.GetTimestamp()
	if err == nil && m.IsActive() {
		m.ExpiresAt += int64(days) * 86400
		m.UpdatedAt = now
		return tx.Save(m).Error
	}
	if planCode == "" {
		return nil
	}
	plan, perr := model.GetMembershipPlanByCode(planCode)
	if perr != nil || plan == nil {
		return nil // 计划不存在不阻断领取
	}
	if _, m, err = model.UpsertMembership(tx, userId, plan, "creator", 1, 1, 0); err != nil {
		return err
	}
	m.ExpiresAt = now + int64(days)*86400
	m.NextGrantAt = m.ExpiresAt + 1 // 试用期内不发月积分
	m.UpdatedAt = now
	return tx.Save(m).Error
}

// onBonusRecharge 支付履约后：待生效的首充加赠按实付比例发积分（只对首笔订单）。
func onBonusRecharge(o *model.Order) {
	if o == nil || o.Price <= 0 {
		return
	}
	cfg := setting.GetGrowthConfig()
	now := common.GetTimestamp()
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		claim := model.GetBonusClaimByUser(tx, o.UserId)
		if claim == nil || claim.RewardStatus != model.BonusRewardStatusPending || claim.RewardRate <= 0 {
			return nil
		}
		if claim.RewardExpireAt > 0 && now > claim.RewardExpireAt {
			return tx.Model(claim).Update("reward_status", model.BonusRewardStatusExpired).Error
		}
		if model.CountUserPaidOrders(tx, o.UserId, o.Id) > 0 {
			return tx.Model(claim).Update("reward_status", model.BonusRewardStatusIneligible).Error
		}
		points := cfg.YuanToPoints(o.Price * claim.RewardRate)
		if claim.RewardCapPoints > 0 && points > claim.RewardCapPoints {
			points = claim.RewardCapPoints
		}
		if points <= 0 {
			return tx.Model(claim).Update("reward_status", model.BonusRewardStatusIneligible).Error
		}
		validDays := cfg.BonusDefaultValidDays
		if validDays <= 0 {
			validDays = 30
		}
		if _, err := model.GrantPoints(tx, o.UserId, model.LotSourceGift, points, now+int64(validDays)*86400, o.Id, 0,
			model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonBonusRecharge,
			fmt.Sprintf("口令 %s 首充加赠 +%d 积分", claim.Code, points), 0); err != nil {
			return err
		}
		return tx.Model(claim).Updates(map[string]any{
			"reward_status": model.BonusRewardStatusGranted, "reward_points": points, "rewarded_at": now,
		}).Error
	})
	if err != nil {
		common.SysError(fmt.Sprintf("bonus recharge reward failed user=%d order=%s: %v", o.UserId, o.OrderNo, err))
	}
}

// ExpireBonusRewards 定时：过期的首充权益标记过期。
func ExpireBonusRewards() {
	if n := model.ExpireBonusRewards(common.GetTimestamp()); n > 0 {
		common.SysLog(fmt.Sprintf("bonus: expired %d pending rewards", n))
	}
}

// GenerateBonusCodes 批量生成：prefix + 6 位随机（去易混字符），保证不与库内重复。
func GenerateBonusCodes(prefix string, count int, tpl model.BonusCode) ([]*model.BonusCode, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	prefix = model.NormalizeBonusCode(prefix)
	if count <= 0 || count > 500 {
		return nil, errors.New("数量需在 1~500 之间")
	}
	if len(prefix) > 20 {
		return nil, errors.New("前缀过长")
	}
	now := common.GetTimestamp()
	list := make([]*model.BonusCode, 0, count)
	seen := map[string]bool{}
	for len(list) < count {
		b := make([]byte, 6)
		for i := range b {
			b[i] = alphabet[rand.Intn(len(alphabet))]
		}
		code := prefix + string(b)
		if seen[code] {
			continue
		}
		var n int64
		model.DB.Model(&model.BonusCode{}).Where("code = ?", code).Count(&n)
		if n > 0 {
			continue
		}
		seen[code] = true
		item := tpl
		item.Id, item.Code, item.ClaimedCount, item.CreatedAt, item.UpdatedAt = 0, code, 0, now, now
		list = append(list, &item)
	}
	if err := model.DB.CreateInBatches(list, 100).Error; err != nil {
		return nil, err
	}
	return list, nil
}
