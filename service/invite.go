package service

import (
	"fmt"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 双向邀请（T12）。
// 绑定：注册时 attr.ref → inviter_id（自邀 / 同设备 / 同 IP 当日超限不绑定）。
// 生效：被邀请人首条视频成功 → 双方各得积分，邀请人记 promoter_bill 注册返，完成「邀请第一位好友」任务。
// 返利：被邀请人首笔支付 → 邀请人得 实付 × 比例 × 积分汇率，累计封顶；退款冲回。

// bindInviterOnRegister 注册成功后绑定邀请人；返回绑定到的邀请人 ID（0 未绑定）。
func bindInviterOnRegister(user *model.User, attr model.Attribution, ip, device string) int {
	if attr.AffCode == "" {
		return 0
	}
	inviterId := model.GetUserIdByAffCode(attr.AffCode)
	if inviterId <= 0 || inviterId == user.Id {
		return 0
	}
	cfg := setting.GetGrowthConfig()
	if model.HasRegisterEventByDevice(device, user.Id) {
		common.SysLog(fmt.Sprintf("invite bind skipped: same device user=%d inviter=%d", user.Id, inviterId))
		return 0
	}
	if cfg.InviteDailyIpLimit > 0 {
		now := time.Now().In(model.CheckinLocation)
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, model.CheckinLocation).Unix()
		if model.CountRegisterEventsByIpToday(ip, dayStart) >= int64(cfg.InviteDailyIpLimit) {
			common.SysLog(fmt.Sprintf("invite bind skipped: ip limit user=%d inviter=%d ip=%s", user.Id, inviterId, ip))
			return 0
		}
	}
	if err := model.BindInviter(user.Id, inviterId); err != nil {
		common.SysError(fmt.Sprintf("bind inviter failed user=%d inviter=%d: %v", user.Id, inviterId, err))
		return 0
	}
	user.InviterId = inviterId
	model.DB.Model(&model.User{}).Where("id = ?", inviterId).Update("aff_count", gorm.Expr("aff_count + 1"))
	return inviterId
}

func inviteBlocked(userId int) bool {
	var blocked bool
	model.DB.Model(&model.User{}).Select("invite_blocked").Where("id = ?", userId).Scan(&blocked)
	return blocked
}

// onInviteeFirstVideo 被邀请人首条视频成功：双方发积分、记注册返、邀请人完成任务。
func onInviteeFirstVideo(inviteeId int) {
	inviterId := model.GetInviterOf(inviteeId)
	if inviterId <= 0 {
		return
	}
	if inviteBlocked(inviterId) || inviteBlocked(inviteeId) {
		return
	}
	cfg := setting.GetGrowthConfig()
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if model.HasRegisterRebate(tx, inviterId, inviteeId) {
			return nil
		}
		now := common.GetTimestamp()
		expires := now + int64(cfg.InviteValidDays)*86400
		if cfg.InviteInviterPoints > 0 {
			if _, err := model.GrantPoints(tx, inviterId, model.LotSourceGift, cfg.InviteInviterPoints, expires, 0, 0,
				model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonInviteInviter,
				fmt.Sprintf("邀请好友生效 +%d 积分", cfg.InviteInviterPoints), 0); err != nil {
				return err
			}
		}
		if cfg.InviteInviteePoints > 0 {
			if _, err := model.GrantPoints(tx, inviteeId, model.LotSourceGift, cfg.InviteInviteePoints, expires, 0, 0,
				model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonInviteInvitee,
				fmt.Sprintf("受邀注册奖励 +%d 积分", cfg.InviteInviteePoints), 0); err != nil {
				return err
			}
		}
		return tx.Create(&model.PromoterBill{
			UserId:     inviterId,
			BizType:    model.PromoterBillTypeRegisterRebate,
			Amount:     float64(cfg.InviteInviterPoints),
			FromUserId: inviteeId,
			Status:     model.PromoterBillStatusValid,
			CreatedAt:  now,
			Remark:     "被邀请人完成首条视频",
		}).Error
	})
	if err != nil {
		common.SysError(fmt.Sprintf("invite effective failed inviter=%d invitee=%d: %v", inviterId, inviteeId, err))
		return
	}
	CompleteUserTask(inviterId, setting.GrowthTaskKeyFirstInvite)
}

// onInviteePaid 被邀请人支付成功：首笔订单按比例返积分给邀请人，累计封顶。
func onInviteePaid(o *model.Order) {
	if o == nil || o.Price <= 0 {
		return
	}
	inviterId := model.GetInviterOf(o.UserId)
	if inviterId <= 0 || inviteBlocked(inviterId) || inviteBlocked(o.UserId) {
		return
	}
	cfg := setting.GetGrowthConfig()
	if cfg.InviteRechargeRate <= 0 {
		return
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if model.CountUserPaidOrders(tx, o.UserId, o.Id) > 0 {
			return nil // 只返首笔
		}
		if model.GetRechargeRebateByOrder(tx, o.OrderNo) != nil {
			return nil
		}
		points := cfg.YuanToPoints(o.Price * cfg.InviteRechargeRate)
		if cfg.InviteRechargeCap > 0 {
			remain := cfg.InviteRechargeCap - model.SumRechargeRebate(tx, inviterId)
			if remain <= 0 {
				return nil
			}
			if points > remain {
				points = remain
			}
		}
		if points <= 0 {
			return nil
		}
		now := common.GetTimestamp()
		if _, err := model.GrantPoints(tx, inviterId, model.LotSourceGift, points, now+int64(cfg.InviteValidDays)*86400, o.Id, 0,
			model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonInviteRebate,
			fmt.Sprintf("好友首充返利 +%d 积分", points), 0); err != nil {
			return err
		}
		return tx.Create(&model.PromoterBill{
			UserId:       inviterId,
			BizType:      model.PromoterBillTypeRechargeRebate,
			Amount:       float64(points),
			FromUserId:   o.UserId,
			SourceNo:     o.OrderNo,
			SourceAmount: o.Price,
			Rate:         cfg.InviteRechargeRate,
			RefId:        o.Id,
			Status:       model.PromoterBillStatusValid,
			CreatedAt:    now,
			Remark:       "好友首充返利",
		}).Error
	})
	if err != nil {
		common.SysError(fmt.Sprintf("invite rebate failed inviter=%d order=%s: %v", inviterId, o.OrderNo, err))
	}
}

// revokeInviteRebateOnRefund 订单退款：作废充值返并从邀请人账户扣回（余额不足时扣到 0 为止，差额记日志）。
func revokeInviteRebateOnRefund(orderNo string) {
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		b := model.GetRechargeRebateByOrder(tx, orderNo)
		if b == nil {
			return nil
		}
		if err := tx.Model(&model.PromoterBill{}).Where("id = ?", b.Id).Update("status", model.PromoterBillStatusRevoked).Error; err != nil {
			return err
		}
		points := int(b.Amount)
		if points > 0 {
			balance := model.GetPointsBalance(tx, b.UserId)
			deduct := points
			if deduct > balance {
				deduct = balance
				common.SysLog(fmt.Sprintf("invite rebate revoke partial: inviter=%d order=%s want=%d balance=%d", b.UserId, orderNo, points, balance))
			}
			if deduct > 0 {
				if err := model.ConsumePoints(tx, b.UserId, deduct, model.PointLogAdjust, model.PointsBizSystem, setting.GrowthReasonInviteRebate,
					"好友订单退款，冲回返利积分", 0); err != nil {
					return err
				}
			}
		}
		return tx.Create(&model.PromoterBill{
			UserId:     b.UserId,
			BizType:    model.PromoterBillTypeRebateRevoke,
			Amount:     -b.Amount,
			FromUserId: b.FromUserId,
			SourceNo:   orderNo,
			RefId:      b.Id,
			Status:     model.PromoterBillStatusValid,
			CreatedAt:  common.GetTimestamp(),
			Remark:     "好友订单退款冲回",
		}).Error
	})
	if err != nil {
		common.SysError(fmt.Sprintf("invite rebate revoke failed order=%s: %v", orderNo, err))
	}
}

// InviteSummary GET /user/invite/summary 响应。
type InviteSummary struct {
	AffCode      string               `json:"aff_code"`
	InviteUrl    string               `json:"invite_url"`
	Invited      int64                `json:"invited"`
	Effective    int64                `json:"effective"`
	PointsTotal  int                  `json:"points_total"`
	RebatePoints int                  `json:"rebate_points"`
	RebateCap    int                  `json:"rebate_cap"`
	RebateRate   float64              `json:"rebate_rate"`
	InviterGets  int                  `json:"inviter_points"`
	InviteeGets  int                  `json:"invitee_points"`
	Records      []model.InviteRecord `json:"records"`
}

// GetInviteSummary 邀请总览；没有邀请码的老用户在这里补发。
func GetInviteSummary(userId int) (*InviteSummary, error) {
	var u model.User
	if err := model.DB.Select("id, aff_code").Where("id = ?", userId).First(&u).Error; err != nil {
		return nil, err
	}
	if u.AffCode == "" || len(u.AffCode) < 6 {
		u.AffCode = model.NewAffCode()
		if err := model.DB.Model(&model.User{}).Where("id = ?", userId).Update("aff_code", u.AffCode).Error; err != nil {
			return nil, err
		}
	}
	cfg := setting.GetGrowthConfig()
	records, err := model.GetInviteRecords(userId, 20)
	if err != nil {
		return nil, err
	}
	t := model.GetInviteTotals(userId)
	return &InviteSummary{
		AffCode:      u.AffCode,
		InviteUrl:    strings.TrimRight(cfg.InviteBaseUrl, "/") + "/invite/" + u.AffCode,
		Invited:      t.Invited,
		Effective:    t.Effective,
		PointsTotal:  t.PointsTotal,
		RebatePoints: t.RebatePoints,
		RebateCap:    cfg.InviteRechargeCap,
		RebateRate:   cfg.InviteRechargeRate,
		InviterGets:  cfg.InviteInviterPoints,
		InviteeGets:  cfg.InviteInviteePoints,
		Records:      records,
	}, nil
}
