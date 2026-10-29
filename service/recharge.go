package service

import (
	"fmt"
	"time"

	"easytv-api/common"
	"easytv-api/model"
)

// CompleteRecharge 统一充值入账：校验订单 → 置成功 → 增加用户额度 → 写资金流水。
// 各支付渠道 webhook / 主动查单成功后调用，天然幂等（仅 pending 可成功）。
func CompleteRecharge(tradeNo string, expectedProvider string, remark string) error {
	if tradeNo == "" {
		return fmt.Errorf("未提供支付单号")
	}
	topUp, err := model.GetRechargeByTradeNo(tradeNo)
	if err != nil {
		return model.ErrRechargeNotFound
	}
	if expectedProvider != "" && topUp.PaymentProvider != expectedProvider {
		return model.ErrPaymentMethodMismatch
	}
	if topUp.Status != common.RechargeStatusPending {
		return model.ErrRechargeStatusInvalid
	}
	if err := model.MarkRechargeSuccess(tradeNo, expectedProvider, time.Now().Unix()); err != nil {
		return err
	}

	// 付费域：trade_no 命中订单则履约（发会员/发积分），不再加 quota（API.md 6.0 规则 5）
	if order, err := model.GetOrderByNo(tradeNo); err == nil {
		if err := FulfillOrder(order.Id, topUp.PaymentMethod); err != nil && err != model.ErrOrderStatus {
			common.SysError(fmt.Sprintf("CompleteRecharge fulfill order %s failed: %v", order.OrderNo, err))
			return err
		}
		common.SysLog(fmt.Sprintf("order paid: %s user_id=%d money=%.2f", order.OrderNo, order.UserId, topUp.Money))
		return nil
	}

	// 入账：金额（元）× QuotaPerUnit = 额度。
	// 团队充值（TeamId>0）计入团队钱包，即 owner 的积分池（对齐 easyapi 的团队入账语义）。
	creditUserId := topUp.UserId
	if topUp.TeamId > 0 {
		team, err := model.GetTeamById(topUp.TeamId)
		if err != nil {
			common.SysError(fmt.Sprintf("CompleteRecharge load team %d failed: %v", topUp.TeamId, err))
			return err
		}
		creditUserId = team.OwnerId
	}
	quotaToAdd := int(float64(topUp.Amount) * common.QuotaPerUnit)
	if err := model.IncreaseUserQuota(creditUserId, quotaToAdd); err != nil {
		common.SysError(fmt.Sprintf("CompleteRecharge increase quota failed: %v", err))
		return err
	}

	// 写资金流水（含变动后余额快照）
	balance, _ := model.GetUserQuota(creditUserId)
	bill := &model.PromoterBill{
		UserId:    creditUserId,
		BizType:   model.PromoterBillTypeRechargeRebate,
		Amount:    topUp.Money,
		Balance:   float64(balance) / common.QuotaPerUnit,
		SourceNo:  topUp.TradeNo,
		CreatedAt: time.Now().Unix(),
		Remark:    remark,
	}
	if err := bill.Insert(); err != nil {
		common.SysError(fmt.Sprintf("CompleteRecharge insert bill failed: %v", err))
	}
	common.SysLog(fmt.Sprintf("recharge success: trade_no=%s user_id=%d amount=%d money=%.2f", tradeNo, topUp.UserId, topUp.Amount, topUp.Money))
	return nil
}
