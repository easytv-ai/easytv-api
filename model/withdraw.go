package model

import (
	"errors"

	"easytv-api/common"
)

// Withdraw 统一提现单（promoter + partner），见 easyapi docs/partner-team-schema.sql。
// 资金扣减/退回由各 owner 的业务函数负责，本表只记录提现单本身。
type Withdraw struct {
	Id            int     `json:"id" gorm:"comment:提现ID"`
	WithdrawNo    string  `json:"withdraw_no" gorm:"type:varchar(32);uniqueIndex;comment:提现单号"`
	OwnerType     int     `json:"owner_type" gorm:"index:idx_withdraw_owner,priority:1;default:0;comment:提现主体类型：1=promoter（平台用户）2=partner（伙伴）3=creator（技能分成）"`
	OwnerId       int     `json:"owner_id" gorm:"index:idx_withdraw_owner,priority:2;comment:owner_type=1→user.id；owner_type=2→partner.id"`
	Amount        float64 `json:"amount" gorm:"type:decimal(12,2);default:0;comment:提现金额（元）"`
	Fee           float64 `json:"fee" gorm:"type:decimal(12,2);default:0;comment:手续费（元）"`
	ActualAmount  float64 `json:"actual_amount" gorm:"type:decimal(12,2);default:0;comment:实际到账金额（元）"`
	Way           string  `json:"way" gorm:"type:varchar(20);default:'';comment:提现方式：alipay/wechat/bank"`
	AccountInfo   string  `json:"account_info" gorm:"type:text;comment:收款账户 JSON"`
	Status        int     `json:"status" gorm:"index;default:0;comment:状态：0=待审核 1=已打款 2=打款中 -1=打款失败 -9=已驳回"`
	AuditAdminId  int     `json:"audit_admin_id" gorm:"default:0;comment:审核管理员 ID"`
	AuditTime     int64   `json:"audit_time" gorm:"bigint;default:0;comment:审核时间"`
	AuditRemark   string  `json:"audit_remark" gorm:"type:varchar(255);default:'';comment:审核备注"`
	CreatedAt     int64   `json:"created_at" gorm:"bigint;comment:申请时间（Unix 秒）"`
	CompleteTime  int64   `json:"complete_time" gorm:"bigint;default:0;comment:完成时间（Unix 秒）"`
	TransactionNo string  `json:"transaction_no" gorm:"type:varchar(64);default:'';comment:第三方交易流水号"`
	FailureMsg    string  `json:"failure_msg" gorm:"type:varchar(255);default:'';comment:失败原因"`
}

func (Withdraw) TableName() string { return "withdraw" }

const (
	WithdrawOwnerTypePromoter = 1 // 平台用户（推广员）
	WithdrawOwnerTypePartner  = 2 // 伙伴
	WithdrawOwnerTypeCreator  = 3 // 技能分成（冻结的是积分，不是 quota）

	WithdrawStatusPending   = 0  // 待审核
	WithdrawStatusPaid      = 1  // 已打款
	WithdrawStatusPaying    = 2  // 打款中
	WithdrawStatusPayFailed = -1 // 打款失败
	WithdrawStatusRejected  = -9 // 已驳回
)

var ErrWithdrawStatus = errors.New("提现单状态不允许该操作")

// Insert 创建提现单。
func (w *Withdraw) Insert() error {
	return DB.Create(w).Error
}

// GetWithdrawById 按 ID 查询。
func GetWithdrawById(id int) (*Withdraw, error) {
	w := &Withdraw{}
	if err := DB.Where("id = ?", id).First(w).Error; err != nil {
		return nil, err
	}
	return w, nil
}

// GetWithdrawByNo 按提现单号查询。
func GetWithdrawByNo(no string) (*Withdraw, error) {
	w := &Withdraw{}
	if err := DB.Where("withdraw_no = ?", no).First(w).Error; err != nil {
		return nil, err
	}
	return w, nil
}

// GetUserWithdraws 用户（推广员）提现记录分页查询。
func GetUserWithdraws(userId int, pageInfo *common.PageInfo) (list []*Withdraw, total int64, err error) {
	return listWithdrawsByOwner(WithdrawOwnerTypePromoter, userId, pageInfo)
}

// GetCreatorWithdraws 技能分成提现记录分页查询。
func GetCreatorWithdraws(userId int, pageInfo *common.PageInfo) (list []*Withdraw, total int64, err error) {
	return listWithdrawsByOwner(WithdrawOwnerTypeCreator, userId, pageInfo)
}

func listWithdrawsByOwner(ownerType, ownerId int, pageInfo *common.PageInfo) (list []*Withdraw, total int64, err error) {
	query := DB.Model(&Withdraw{}).Where("owner_type = ? AND owner_id = ?", ownerType, ownerId)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// GetAllWithdraws 管理端分页查询；status 为 nil 查全部。
func GetAllWithdraws(status *int, pageInfo *common.PageInfo) (list []*Withdraw, total int64, err error) {
	query := DB.Model(&Withdraw{})
	if status != nil {
		query = query.Where("status = ?", *status)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// SetWithdrawPaying 审核通过进入打款中（待审核 → 打款中）。
func SetWithdrawPaying(id int, adminId int, remark string) error {
	result := DB.Model(&Withdraw{}).
		Where("id = ? AND status = ?", id, WithdrawStatusPending).
		Updates(map[string]interface{}{
			"status":         WithdrawStatusPaying,
			"audit_admin_id": adminId,
			"audit_time":     common.GetTimestamp(),
			"audit_remark":   remark,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWithdrawStatus
	}
	return nil
}

// SetWithdrawPaid 打款完成（打款中 → 已打款）。
func SetWithdrawPaid(id int, transactionNo string) error {
	result := DB.Model(&Withdraw{}).
		Where("id = ? AND status = ?", id, WithdrawStatusPaying).
		Updates(map[string]interface{}{
			"status":         WithdrawStatusPaid,
			"complete_time":  common.GetTimestamp(),
			"transaction_no": transactionNo,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWithdrawStatus
	}
	return nil
}

// SetWithdrawRejected 驳回（待审核 → 已驳回）。
func SetWithdrawRejected(id int, adminId int, remark string) error {
	result := DB.Model(&Withdraw{}).
		Where("id = ? AND status = ?", id, WithdrawStatusPending).
		Updates(map[string]interface{}{
			"status":         WithdrawStatusRejected,
			"audit_admin_id": adminId,
			"audit_time":     common.GetTimestamp(),
			"audit_remark":   remark,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWithdrawStatus
	}
	return nil
}

// SetWithdrawFailed 打款失败（打款中 → 打款失败）。
func SetWithdrawFailed(id int, failureMsg string) error {
	result := DB.Model(&Withdraw{}).
		Where("id = ? AND status = ?", id, WithdrawStatusPaying).
		Updates(map[string]interface{}{
			"status":        WithdrawStatusPayFailed,
			"failure_msg":   failureMsg,
			"complete_time": common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWithdrawStatus
	}
	return nil
}
