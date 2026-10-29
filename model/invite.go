package model

import (
	"strconv"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// 邀请关系查询（T12）。奖励流水记在 promoter_bill：
// biz_type 1（注册返）= 被邀请人完成首条视频后给邀请人的积分，from_user_id 为被邀请人；
// biz_type 2（充值返）= 被邀请人首充后按比例给邀请人的积分，source_no 为订单号；
// biz_type 6（返利作废）= 退款冲回，amount 为负。amount 单位一律为积分。

// BindInviter 写入邀请人（仅当尚未绑定）。
func BindInviter(userId, inviterId int) error {
	return DB.Model(&User{}).Where("id = ? AND inviter_id = 0", userId).Update("inviter_id", inviterId).Error
}

// GetInviterOf 被邀请人 → 邀请人 ID（0 表示无）。
func GetInviterOf(userId int) int {
	var id int
	DB.Model(&User{}).Select("inviter_id").Where("id = ?", userId).Scan(&id)
	return id
}

// CountRegisterEventsByIpToday 今日同 IP 注册数（防刷）。
func CountRegisterEventsByIpToday(ip string, dayStart int64) int64 {
	if ip == "" {
		return 0
	}
	var n int64
	DB.Model(&TrackEvent{}).Where("name = ? AND ip = ? AND created_at >= ?", TrackEventRegister, ip, dayStart).Count(&n)
	return n
}

// HasRegisterEventByDevice 该设备指纹是否已注册过（同设备只算一次邀请）。
func HasRegisterEventByDevice(device string, excludeUserId int) bool {
	if device == "" {
		return false
	}
	var n int64
	DB.Model(&TrackEvent{}).Where("name = ? AND device = ? AND user_id <> ?", TrackEventRegister, device, excludeUserId).Limit(1).Count(&n)
	return n > 0
}

// HasRegisterRebate 邀请人是否已为该被邀请人记过注册返（幂等）。
func HasRegisterRebate(tx *gorm.DB, inviterId, inviteeId int) bool {
	var n int64
	tx.Model(&PromoterBill{}).Where("user_id = ? AND from_user_id = ? AND biz_type = ?", inviterId, inviteeId, PromoterBillTypeRegisterRebate).Limit(1).Count(&n)
	return n > 0
}

// SumRechargeRebate 邀请人累计有效充值返积分。
func SumRechargeRebate(tx *gorm.DB, inviterId int) int {
	var v struct{ Total float64 }
	tx.Model(&PromoterBill{}).Select("COALESCE(SUM(amount),0) AS total").
		Where("user_id = ? AND biz_type = ? AND status = ?", inviterId, PromoterBillTypeRechargeRebate, PromoterBillStatusValid).Scan(&v)
	return int(v.Total)
}

// GetRechargeRebateByOrder 按订单号找有效的充值返流水（退款冲回用）。
func GetRechargeRebateByOrder(tx *gorm.DB, orderNo string) *PromoterBill {
	var b PromoterBill
	if err := tx.Where("biz_type = ? AND source_no = ? AND status = ?", PromoterBillTypeRechargeRebate, orderNo, PromoterBillStatusValid).First(&b).Error; err != nil {
		return nil
	}
	return &b
}

// CountUserPaidOrders 用户已支付（含已退款）的订单数，排除指定订单。
func CountUserPaidOrders(tx *gorm.DB, userId, excludeOrderId int) int64 {
	var n int64
	tx.Model(&Order{}).Where("user_id = ? AND id <> ? AND status IN ?", userId, excludeOrderId, []string{OrderStatusPaid, OrderStatusRefunded}).Count(&n)
	return n
}

// InviteRecord 邀请记录一行（GET /user/invite/summary）。
type InviteRecord struct {
	UserId    int    `json:"user_id"`
	Nickname  string `json:"nickname"`
	Status    string `json:"status"` // registered / effective / paid
	Points    int    `json:"points"`
	CreatedAt int64  `json:"created_at"`
}

// GetInviteRecords 邀请人名下最近 limit 个被邀请人及其状态与带来的积分。
func GetInviteRecords(inviterId, limit int) ([]InviteRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, _, err := ListInviteRecords(inviterId, "", &common.PageInfo{Page: 1, PageSize: limit})
	return rows, err
}

// ListInviteRecords 分页邀请记录，keyword 按被邀请人 ID 或昵称筛。
func ListInviteRecords(inviterId int, keyword string, page *common.PageInfo) ([]InviteRecord, int64, error) {
	q := DB.Model(&User{}).Where("inviter_id = ?", inviterId)
	if kw := strings.TrimSpace(keyword); kw != "" {
		like := "%" + kw + "%"
		if id, err := strconv.Atoi(kw); err == nil && id > 0 {
			q = q.Where("id = ? OR nickname LIKE ?", id, like)
		} else {
			q = q.Where("nickname LIKE ?", like)
		}
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	type row struct {
		Id        int
		Nickname  string
		CreatedAt int64
	}
	var users []row
	if err := q.Select("id, nickname, created_at").Order("id desc").
		Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Scan(&users).Error; err != nil {
		return nil, 0, err
	}
	if len(users) == 0 {
		return []InviteRecord{}, total, nil
	}
	ids := make([]int, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.Id)
	}
	type billRow struct {
		FromUserId int
		BizType    int
		Total      float64
	}
	var bills []billRow
	DB.Model(&PromoterBill{}).Select("from_user_id, biz_type, COALESCE(SUM(amount),0) AS total").
		Where("user_id = ? AND from_user_id IN ? AND status = ? AND biz_type IN ?", inviterId, ids, PromoterBillStatusValid,
			[]int{PromoterBillTypeRegisterRebate, PromoterBillTypeRechargeRebate}).
		Group("from_user_id, biz_type").Scan(&bills)
	points := map[int]int{}
	status := map[int]string{}
	for _, b := range bills {
		points[b.FromUserId] += int(b.Total)
		if b.BizType == PromoterBillTypeRechargeRebate {
			status[b.FromUserId] = "paid"
		} else if status[b.FromUserId] == "" {
			status[b.FromUserId] = "effective"
		}
	}
	out := make([]InviteRecord, 0, len(users))
	for _, u := range users {
		st := status[u.Id]
		if st == "" {
			st = "registered"
		}
		out = append(out, InviteRecord{UserId: u.Id, Nickname: MaskNickname(u.Nickname), Status: st, Points: points[u.Id], CreatedAt: u.CreatedAt})
	}
	return out, total, nil
}

// InviteTotals 邀请总览数字。
type InviteTotals struct {
	Invited      int64 `json:"invited"`
	Effective    int64 `json:"effective"`
	PointsTotal  int   `json:"points_total"`
	RebatePoints int   `json:"rebate_points"`
}

// GetInviteTotals 邀请人数、生效人数、累计积分、其中充值返积分。
func GetInviteTotals(inviterId int) InviteTotals {
	var t InviteTotals
	DB.Model(&User{}).Where("inviter_id = ?", inviterId).Count(&t.Invited)
	DB.Model(&PromoterBill{}).Where("user_id = ? AND biz_type = ? AND status = ?", inviterId, PromoterBillTypeRegisterRebate, PromoterBillStatusValid).Count(&t.Effective)
	var v struct{ Total float64 }
	DB.Model(&PromoterBill{}).Select("COALESCE(SUM(amount),0) AS total").
		Where("user_id = ? AND biz_type IN ? AND status = ?", inviterId, []int{PromoterBillTypeRegisterRebate, PromoterBillTypeRechargeRebate}, PromoterBillStatusValid).Scan(&v)
	t.PointsTotal = int(v.Total)
	t.RebatePoints = SumRechargeRebate(DB, inviterId)
	return t
}

// MaskNickname 昵称脱敏：保留首尾字符。
func MaskNickname(s string) string {
	r := []rune(s)
	switch {
	case len(r) == 0:
		return "用户"
	case len(r) <= 2:
		return string(r[:1]) + "*"
	default:
		return string(r[:1]) + "**" + string(r[len(r)-1:])
	}
}
