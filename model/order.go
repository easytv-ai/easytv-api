package model

import (
	"errors"
	"fmt"
	"time"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Order 订单（API.md 6.1）。order_no 同时作为 recharge.trade_no 走已有支付通道。
type Order struct {
	Id            int     `json:"id" gorm:"comment:订单ID"`
	OrderNo       string  `json:"order_no" gorm:"type:varchar(32);uniqueIndex;comment:订单号=支付单号"`
	UserId        int     `json:"user_id" gorm:"index;comment:用户ID"`
	Kind          string  `json:"kind" gorm:"type:varchar(20);index;comment:membership/points"`
	PlanId        int     `json:"plan_id" gorm:"default:0"`
	PackId        int     `json:"pack_id" gorm:"default:0"`
	Edition       string  `json:"edition" gorm:"type:varchar(20);default:'';comment:creator/team"`
	Cycle         string  `json:"cycle" gorm:"type:varchar(20);default:'';comment:month/quarter/year"`
	Months        int     `json:"months" gorm:"default:0"`
	Seats         int     `json:"seats" gorm:"default:1"`
	AddOn         bool    `json:"add_on" gorm:"default:false;comment:是否加购积分"`
	Title         string  `json:"title" gorm:"type:varchar(200);comment:快照标题"`
	Origin        float64 `json:"origin" gorm:"type:decimal(10,2);default:0;comment:原价"`
	Price         float64 `json:"price" gorm:"type:decimal(10,2);default:0;comment:应付"`
	Points        int     `json:"points" gorm:"default:0;comment:履约总积分"`
	ValidDays     int     `json:"valid_days" gorm:"default:0;comment:积分包有效期"`
	Status        string  `json:"status" gorm:"type:varchar(20);index;default:'pending';comment:pending/paid/closed/refunded"`
	PaymentMethod string  `json:"payment_method" gorm:"type:varchar(50);default:''"`
	PaidAt        int64   `json:"paid_at" gorm:"bigint;default:0"`
	ClosedAt      int64   `json:"closed_at" gorm:"bigint;default:0"`
	RefundedAt    int64   `json:"refunded_at" gorm:"bigint;default:0"`
	RefundReason  string  `json:"refund_reason" gorm:"type:varchar(255);default:''"`
	RefundNo      string  `json:"refund_no" gorm:"type:varchar(40);default:'';comment:退款请求号（渠道 out_refund_no / out_request_no）"`
	RefundChannel string  `json:"refund_channel" gorm:"type:varchar(255);default:'';comment:渠道退款结果"`
	Remark        string  `json:"remark" gorm:"type:varchar(255);default:''"`
	CreatedAt     int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt     int64   `json:"updated_at" gorm:"bigint"`

	// UserName 管理端列表附带
	UserName string `json:"user_name,omitempty" gorm:"-"`
}

func (Order) TableName() string { return "order" }

const (
	OrderKindMembership = "membership"
	OrderKindPoints     = "points"

	OrderStatusPending  = "pending"
	OrderStatusPaid     = "paid"
	OrderStatusClosed   = "closed"
	OrderStatusRefunded = "refunded"

	EditionCreator = "creator"
	EditionTeam    = "team"

	CycleMonth   = "month"
	CycleQuarter = "quarter"
	CycleYear    = "year"

	// OrderPendingTTL 待支付订单超时。
	OrderPendingTTL = 30 * time.Minute
)

var ErrOrderStatus = errors.New("订单当前状态不允许该操作")

// CycleMonths 周期 → 月数；无效返回 0。
func CycleMonths(cycle string) int {
	switch cycle {
	case CycleMonth:
		return 1
	case CycleQuarter:
		return 3
	case CycleYear:
		return 12
	}
	return 0
}

// NewOrderNo 生成订单号 ET + 日期 + 时间戳尾 + 随机。
func NewOrderNo() string {
	now := time.Now()
	return fmt.Sprintf("ET%s%d%s", now.Format("20060102"), now.Unix()%100000, common.GetRandomString(4))
}

func (o *Order) Insert() error {
	now := common.GetTimestamp()
	o.CreatedAt, o.UpdatedAt = now, now
	if o.OrderNo == "" {
		o.OrderNo = NewOrderNo()
	}
	if o.Status == "" {
		o.Status = OrderStatusPending
	}
	if o.Seats <= 0 {
		o.Seats = 1
	}
	return DB.Create(o).Error
}

func GetOrderById(id int) (*Order, error) {
	o := &Order{}
	if err := DB.Where("id = ?", id).First(o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

func GetUserOrder(userId, id int) (*Order, error) {
	o := &Order{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

func GetOrderByNo(orderNo string) (*Order, error) {
	o := &Order{}
	if err := DB.Where("order_no = ?", orderNo).First(o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

// OrderFilter 订单列表过滤。
type OrderFilter struct {
	UserId  int
	Kind    string
	Status  string
	Keyword string // 订单号
}

func GetOrders(filter OrderFilter, pageInfo *common.PageInfo) (list []*Order, total int64, err error) {
	query := DB.Model(&Order{})
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Kind != "" {
		query = query.Where("kind = ?", filter.Kind)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Keyword != "" {
		query = query.Where("order_no LIKE ? OR title LIKE ?", "%"+filter.Keyword+"%", "%"+filter.Keyword+"%")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(list))
	for _, o := range list {
		ids = append(ids, o.UserId)
	}
	names := GetUserNicknames(ids)
	for _, o := range list {
		o.UserName = names[o.UserId]
	}
	return list, total, nil
}

// SetOrderPaid pending → paid（在事务 tx 内调用）。
func SetOrderPaid(tx *gorm.DB, id int, paymentMethod string) error {
	fields := map[string]any{
		"status":     OrderStatusPaid,
		"paid_at":    common.GetTimestamp(),
		"updated_at": common.GetTimestamp(),
	}
	if paymentMethod != "" {
		fields["payment_method"] = paymentMethod
	}
	result := tx.Model(&Order{}).Where("id = ? AND status = ?", id, OrderStatusPending).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrderStatus
	}
	return nil
}

// SetOrderPaymentMethod 下单发起支付时记录渠道。
func SetOrderPaymentMethod(id int, paymentMethod string) error {
	return DB.Model(&Order{}).Where("id = ?", id).Updates(map[string]any{
		"payment_method": paymentMethod,
		"updated_at":     common.GetTimestamp(),
	}).Error
}

// CloseOrder pending → closed；userId>0 时限定归属。
func CloseOrder(userId, id int, remark string) error {
	query := DB.Model(&Order{}).Where("id = ? AND status = ?", id, OrderStatusPending)
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
	}
	result := query.Updates(map[string]any{
		"status":     OrderStatusClosed,
		"closed_at":  common.GetTimestamp(),
		"remark":     remark,
		"updated_at": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrderStatus
	}
	return nil
}

// ExpirePendingOrders 关闭超时待支付订单，返回关闭数。
func ExpirePendingOrders() int64 {
	cutoff := time.Now().Add(-OrderPendingTTL).Unix()
	result := DB.Model(&Order{}).Where("status = ? AND created_at < ?", OrderStatusPending, cutoff).Updates(map[string]any{
		"status":     OrderStatusClosed,
		"closed_at":  common.GetTimestamp(),
		"remark":     "超时未支付自动关闭",
		"updated_at": common.GetTimestamp(),
	})
	return result.RowsAffected
}

// SetOrderRefunded paid → refunded（在事务 tx 内调用）。
func SetOrderRefunded(tx *gorm.DB, id int, reason string) error {
	result := tx.Model(&Order{}).Where("id = ? AND status = ?", id, OrderStatusPaid).Updates(map[string]any{
		"status":        OrderStatusRefunded,
		"refunded_at":   common.GetTimestamp(),
		"refund_reason": reason,
		"updated_at":    common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrderStatus
	}
	return nil
}

// SetOrderRefundInfo 记录渠道退款请求号与结果。
func SetOrderRefundInfo(id int, refundNo, channelMsg string) error {
	return DB.Model(&Order{}).Where("id = ?", id).Updates(map[string]any{
		"refund_no":      refundNo,
		"refund_channel": channelMsg,
		"updated_at":     common.GetTimestamp(),
	}).Error
}

// OrderPaymentMethodManual 管理端「标记已支付」的渠道标记（测试 / 补单，不产生实收）。
const OrderPaymentMethodManual = "manual"

// SumUserInvoiceablePaid 用户套餐/积分包可开票实付（元，API.md 21）：
// status=paid 且 price>0，排除 payment_method 为 manual（管理端标记已支付）或空（无渠道实收）的订单。
// 已退款订单状态是 refunded，自然不计。纯充值见 SumUserInvoiceableTopup，两者相加才是额度里的 paid。
func SumUserInvoiceablePaid(userId int) (float64, error) {
	var v struct{ Total float64 }
	err := DB.Model(&Order{}).Select("COALESCE(SUM(price),0) AS total").
		Where("user_id = ? AND status = ? AND price > 0 AND payment_method NOT IN ?", userId, OrderStatusPaid, []string{OrderPaymentMethodManual, ""}).
		Scan(&v).Error
	return v.Total, err
}
