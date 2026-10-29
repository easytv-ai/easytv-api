package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Recharge 充值/支付订单表（完整复刻 easyapi model/recharge.go，含团队与开票字段）。
type Recharge struct {
	Id     int     `json:"id" gorm:"comment:充值ID"`
	UserId int     `json:"user_id" gorm:"index;comment:发起人（team_id>0 时为团队 owner/admin）"`
	TeamId int     `json:"team_id" gorm:"index;default:0;comment:团队 ID，>0 即团队充值：完成后入团队钱包"`
	Amount int64   `json:"amount" gorm:"comment:充值额度（quota）"`
	Money  float64 `json:"money" gorm:"comment:实付金额（元）"`
	// uniqueIndex 命名与原 easyapi_ai.sql 中的 idx_recharge_trade_no 对齐，
	// 现有库若已存在该唯一索引则 AutoMigrate 不会重建；非唯一索引命名为
	// idx_recharge_trade_no_nu，保留 LIKE 查询场景的二级索引。
	// 注意：uniqueIndex 与 index 同时出现时必须显式命名，否则 GORM 会生成
	// 同名索引并合并为同一条 UNIQUE INDEX，导致 (trade_no, trade_no) 重复列，
	// MySQL 报 1060 Duplicate column name。
	TradeNo         string `json:"trade_no" gorm:"uniqueIndex:idx_recharge_trade_no;type:varchar(32);index:idx_recharge_trade_no_nu;comment:商户订单号"`
	PaymentMethod   string `json:"payment_method" gorm:"type:varchar(50);comment:支付方式（wechat_native/alipay_f2f 等）"`
	PaymentProvider string `json:"payment_provider" gorm:"type:varchar(50);default:'';comment:支付渠道（epay/wechat_native 等）"`
	CreateTime      int64  `json:"create_time" gorm:"comment:创建时间（Unix 秒）"`
	CompleteTime    int64  `json:"complete_time" gorm:"comment:完成时间（Unix 秒）"`
	Status          string `json:"status" gorm:"type:varchar(20);comment:状态：pending=待支付 success=成功 failed=失败 expired=过期 refunded=已退款"`

	// 可开票余额：创建时等于实付 Money；开票按 FIFO 扣减，扣到 0 则 Invoiced=true。
	InvoiceableAmount float64 `json:"invoiceable_amount" gorm:"default:0;comment:可开票金额"`
	//是否已开票（可开票余额已扣完）
	Invoiced bool `json:"invoiced" gorm:"index;default:false;comment:是否已开票"`

	// 退款相关字段：累计已退金额（部分退款累加，达到 Money 后状态切到 refunded）。
	// OutRequestNo / RefundedTime / RefundReason 记录最近一次退款的支付宝 out_request_no、完成时间、备注。
	RefundedAmount float64 `json:"refunded_amount" gorm:"type:decimal(12,2);default:0;comment:累计已退金额"`
	OutRequestNo   string  `json:"out_request_no" gorm:"type:varchar(64);default:'';comment:最近退款支付宝 out_request_no"`
	RefundedTime   int64   `json:"refunded_time" gorm:"comment:最近退款完成时间"`
	RefundReason   string  `json:"refund_reason" gorm:"type:varchar(255);default:'';comment:最近退款备注"`
}

func (Recharge) TableName() string { return "recharge" }

const (
	PaymentMethodStripe       = "stripe"
	PaymentMethodCreem        = "creem"
	PaymentMethodWaffo        = "waffo"
	PaymentMethodWaffoPancake = "waffo_pancake"
	PaymentMethodAlipayF2F    = "alipay_f2f"
	PaymentMethodAlipayPCWeb  = "alipay_pc"
	PaymentMethodWechatNative = "wechat_native"
)

const (
	PaymentProviderEpay         = "epay"
	PaymentProviderStripe       = "stripe"
	PaymentProviderCreem        = "creem"
	PaymentProviderWaffo        = "waffo"
	PaymentProviderWaffoPancake = "waffo_pancake"
	PaymentProviderAlipayF2F    = "alipay_f2f"
	PaymentProviderAlipayPCWeb  = "alipay_pc"
	PaymentProviderWechatNative = "wechat_native"
)

var (
	ErrPaymentMethodMismatch = errors.New("payment method mismatch")
	ErrRechargeNotFound      = errors.New("recharge order not found")
	ErrRechargeStatusInvalid = errors.New("recharge order status invalid")
)

// EnsureRechargeInvoiceable 创建时把可开票金额写成实付（尚未写入才填，避免冲掉已扣减的余额）。
func EnsureRechargeInvoiceable(r *Recharge) {
	if r == nil {
		return
	}
	if r.InvoiceableAmount <= 0 && r.Money > 0 {
		r.InvoiceableAmount = r.Money
	}
}

// rechargeInvoiceableBackfillExpr 历史 pending 行还没写过可开票金额时，入账瞬间补成实付。
func rechargeInvoiceableBackfillExpr() interface{} {
	return gorm.Expr("CASE WHEN invoiceable_amount > 0 THEN invoiceable_amount ELSE money END")
}

// Insert 创建订单。
func (topUp *Recharge) Insert() error {
	EnsureRechargeInvoiceable(topUp)
	return DB.Create(topUp).Error
}

// Update 更新订单。
func (topUp *Recharge) Update() error {
	return DB.Save(topUp).Error
}

// GetRechargeById 按 ID 查询。
func GetRechargeById(id int) (*Recharge, error) {
	var topUp *Recharge
	if err := DB.Where("id = ?", id).First(&topUp).Error; err != nil {
		return nil, err
	}
	return topUp, nil
}

// GetRechargeByTradeNo 按商户订单号查询。
func GetRechargeByTradeNo(tradeNo string) (*Recharge, error) {
	var topUp *Recharge
	if err := DB.Where("trade_no = ?", tradeNo).First(&topUp).Error; err != nil {
		return nil, err
	}
	return topUp, nil
}

// GetUserRecharges 用户充值记录分页查询。
func GetUserRecharges(userId int, pageInfo *common.PageInfo) (recharges []*Recharge, total int64, err error) {
	query := DB.Model(&Recharge{}).Where("user_id = ?", userId)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&recharges).Error
	return recharges, total, err
}

// GetAllRecharges 管理端分页查询；status 为空查全部。
func GetAllRecharges(status string, pageInfo *common.PageInfo) (recharges []*Recharge, total int64, err error) {
	query := DB.Model(&Recharge{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&recharges).Error
	return recharges, total, err
}

// MarkRechargeSuccess 事务内将订单置为成功（幂等：仅 pending 可转成功）。
func MarkRechargeSuccess(tradeNo string, expectedPaymentProvider string, completeTime int64) error {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}
	result := DB.Model(&Recharge{}).
		Where("trade_no = ? AND status = ?", tradeNo, common.RechargeStatusPending).
		Updates(map[string]interface{}{
			"status":             common.RechargeStatusSuccess,
			"complete_time":      completeTime,
			"invoiceable_amount": rechargeInvoiceableBackfillExpr(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// 检查订单是否存在且 provider 匹配，用于返回区分错误
		topUp, err := GetRechargeByTradeNo(tradeNo)
		if err != nil {
			return ErrRechargeNotFound
		}
		if expectedPaymentProvider != "" && topUp.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		return ErrRechargeStatusInvalid
	}
	return nil
}

// PatchRechargeInvoiceable 复用支付单时补可开票金额（历史 pending 行可能是 0）。
func PatchRechargeInvoiceable(tradeNo string, extra map[string]any) error {
	fields := map[string]any{
		"invoiceable_amount": rechargeInvoiceableBackfillExpr(),
	}
	for k, v := range extra {
		fields[k] = v
	}
	return DB.Model(&Recharge{}).Where("trade_no = ?", tradeNo).Updates(fields).Error
}

// SumUserInvoiceableTopup 纯充值可开票实付：成功入账、有真实渠道、且没有对应套餐订单的充值单。
// 金额按 money − refunded_amount（全额退款后为 0）。套餐支付单与 order 共用 trade_no，已计入 SumUserInvoiceablePaid，这里排除以免重复。
func SumUserInvoiceableTopup(userId int) (float64, error) {
	var v struct{ Total float64 }
	sub := DB.Model(&Order{}).Select("order_no").Where("user_id = ? AND order_no <> ?", userId, "")
	err := DB.Model(&Recharge{}).
		Select("COALESCE(SUM(CASE WHEN money > IFNULL(refunded_amount,0) THEN money - refunded_amount ELSE 0 END),0) AS total").
		Where("user_id = ? AND status = ? AND money > 0 AND payment_method <> '' AND payment_method <> ?",
			userId, common.RechargeStatusSuccess, OrderPaymentMethodManual).
		Where("trade_no NOT IN (?)", sub).
		Scan(&v).Error
	return v.Total, err
}

// RechargeOriginalInvoiceable 该单原始可开票额：实付减去已退。
func RechargeOriginalInvoiceable(r *Recharge) float64 {
	if r == nil {
		return 0
	}
	v := r.Money - r.RefundedAmount
	if v < 0 {
		return 0
	}
	return v
}

// ListUserSuccessRecharges 成功充值单，按入账时间 FIFO（对账开票占用）。
func ListUserSuccessRecharges(userId int) ([]*Recharge, error) {
	var rows []*Recharge
	err := DB.Where("user_id = ? AND status = ?", userId, common.RechargeStatusSuccess).
		Order("complete_time ASC, id ASC").Find(&rows).Error
	return rows, err
}

// ListUserOpenInvoiceRecharges 尚未开完票的成功充值单，按入账时间 FIFO。
func ListUserOpenInvoiceRecharges(userId int) ([]*Recharge, error) {
	var rows []*Recharge
	err := DB.Where("user_id = ? AND status = ? AND invoiced = ?", userId, common.RechargeStatusSuccess, false).
		Order("complete_time ASC, id ASC").Find(&rows).Error
	return rows, err
}

// UpdateRechargeInvoice 写回开票扣减后的可开票余额与已开票标记。
func UpdateRechargeInvoice(id int, invoiceable float64, invoiced bool) error {
	return DB.Model(&Recharge{}).Where("id = ?", id).Updates(map[string]any{
		"invoiceable_amount": invoiceable,
		"invoiced":           invoiced,
	}).Error
}
