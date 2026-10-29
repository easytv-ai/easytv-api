package model

import (
	"easytv-api/common"
)

// PromoterBill 账单流水（资金流水 + 返利明细合一），完整复刻 easyapi model/promoter.go。
// 返利入账行（BizType 1/2）带来源字段；界面「返利记录」= biz_type IN (1,2)。
type PromoterBill struct {
	Id           int     `json:"id" gorm:"comment:流水ID"`
	UserId       int     `json:"user_id" gorm:"index:idx_promoter_bill_user_biz,priority:1;comment:用户 ID"`
	BizType      int     `json:"biz_type" gorm:"index:idx_promoter_bill_user_biz,priority:2;comment:流水类型：1=注册返 2=充值返 3=提现出账 4=提现退回 5=转额度 6=返利作废"`
	Amount       float64 `json:"amount" gorm:"type:decimal(12,2);default:0;comment:变动金额（正入负出）"`
	Balance      float64 `json:"balance" gorm:"type:decimal(12,2);default:0;comment:变动后余额快照"`
	FromUserId   int     `json:"from_user_id" gorm:"index;default:0;comment:来源用户（返利下行）"`
	SourceNo     string  `json:"source_no" gorm:"type:varchar(64);default:'';comment:来源单号（充值/提现单号）"`
	SourceAmount float64 `json:"source_amount" gorm:"type:decimal(12,2);default:0;comment:返利基数金额"`
	Rate         float64 `json:"rate" gorm:"type:decimal(6,4);default:0;comment:返利比例"`
	RefType      int     `json:"ref_type" gorm:"default:0;comment:关联类型"`
	RefId        int     `json:"ref_id" gorm:"default:0;comment:关联 ID"`
	Status       int     `json:"status" gorm:"default:1;comment:状态：1=有效 3=已作废"`
	CreatedAt    int64   `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	Remark       string  `json:"remark" gorm:"type:varchar(255);default:'';comment:备注"`
}

func (PromoterBill) TableName() string { return "promoter_bill" }

const (
	PromoterBillTypeRegisterRebate  = 1  // 注册返
	PromoterBillTypeRechargeRebate  = 2  // 充值返
	PromoterBillTypeWithdraw        = 3  // 提现出账
	PromoterBillTypeWithdrawRefund  = 4  // 提现退回
	PromoterBillTypeConvertQuota    = 5  // 转额度出账
	PromoterBillTypeRebateRevoke    = 6  // 返利作废冲回
	PromoterBillTypeSkillRevenue    = 7  // 技能分成入账（T26，amount 记积分数）
	PromoterBillTypeSkillExchange   = 8  // 技能收益兑换积分出账（负数）
	PromoterBillTypePartnerRebate   = 9  // 渠道伙伴充值返佣（M12，amount 记人民币元，入 quota 可提现）
	PromoterBillTypeSkillCashOut    = 10 // 技能收益提现冻结（负数积分数，source_amount 记人民币）
	PromoterBillTypeSkillCashRefund = 11 // 技能收益提现退回（正数积分数）

	PromoterBillStatusValid   = 1
	PromoterBillStatusRevoked = 3

	PromoterBillRefTypeWithdraw = 1
	PromoterBillRefTypeTask     = 2
)

// Insert 创建流水。
func (b *PromoterBill) Insert() error {
	return DB.Create(b).Error
}

// GetPromoterBills 分页查询账单流水；bizTypes 为空查全部，传 [1,2] 即「返利记录」。
func GetPromoterBills(userId int, bizTypes []int, pageInfo *common.PageInfo) (bills []*PromoterBill, total int64, err error) {
	query := DB.Model(&PromoterBill{}).Where("user_id = ?", userId)
	if len(bizTypes) > 0 {
		query = query.Where("biz_type IN ?", bizTypes)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&bills).Error
	return bills, total, err
}

// GetAllPromoterBills 管理端分页查询全部流水；userId>0 时按用户过滤。
func GetAllPromoterBills(userId int, pageInfo *common.PageInfo) (bills []*PromoterBill, total int64, err error) {
	query := DB.Model(&PromoterBill{})
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&bills).Error
	return bills, total, err
}
