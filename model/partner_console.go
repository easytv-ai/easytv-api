package model

import (
	"easytv-api/common"
)

// 代理商后台（easytv-partner）用到的查询：归属客户筛选、日趋势取数、本月汇总。
// 真相表仍是 partner / partner_customer / promoter_bill，这里只加读接口，不改写路径。

// PartnerCustomerFilter 代理商后台客户列表筛选。
type PartnerCustomerFilter struct {
	// Paid nil 不限；true 只看已付费（first_paid_at > 0）；false 只看未付费
	Paid *bool
	// Sort recent（默认，按归属时间倒序）/ amount（按累计实付倒序）
	Sort string
}

// GetPartnerCustomersByFilter 代理商后台客户分页（附昵称，调用方负责脱敏）。
func GetPartnerCustomersByFilter(partnerId int, f PartnerCustomerFilter, pageInfo *common.PageInfo) (list []*PartnerCustomer, total int64, err error) {
	q := DB.Model(&PartnerCustomer{}).Where("partner_id = ?", partnerId)
	if f.Paid != nil {
		if *f.Paid {
			q = q.Where("first_paid_at > 0")
		} else {
			q = q.Where("first_paid_at = 0")
		}
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "id desc"
	if f.Sort == "amount" {
		order = "amount desc, id desc"
	}
	if err = q.Order(order).Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.UserId)
	}
	names := GetUserNicknames(ids)
	for _, c := range list {
		c.Nickname = names[c.UserId]
	}
	return list, total, nil
}

// PartnerCustomerJoin 归属时间点（趋势图「新增客户」用）。
type PartnerCustomerJoin struct {
	UserId      int
	CreatedAt   int64
	FirstPaidAt int64
}

// GetPartnerCustomerJoinsSince 某时间之后归属的客户（只取时间列，量小）。
func GetPartnerCustomerJoinsSince(partnerId int, since int64) ([]PartnerCustomerJoin, error) {
	var rows []PartnerCustomerJoin
	err := DB.Model(&PartnerCustomer{}).
		Select("user_id, created_at, first_paid_at").
		Where("partner_id = ? AND created_at >= ?", partnerId, since).
		Scan(&rows).Error
	return rows, err
}

// PartnerRebateRow 返佣流水（趋势图 / 本月汇总用）。
type PartnerRebateRow struct {
	FromUserId   int
	Amount       float64 // 返佣（元）
	SourceAmount float64 // 客户实付（元）
	CreatedAt    int64
}

// GetPartnerRebateRowsSince 伙伴用户某时间之后的有效返佣流水（biz_type 9，已冲回的不算）。
func GetPartnerRebateRowsSince(partnerUserId int, since int64) ([]PartnerRebateRow, error) {
	var rows []PartnerRebateRow
	err := DB.Model(&PromoterBill{}).
		Select("from_user_id, amount, source_amount, created_at").
		Where("user_id = ? AND biz_type = ? AND status = ? AND created_at >= ?",
			partnerUserId, PromoterBillTypePartnerRebate, PromoterBillStatusValid, since).
		Scan(&rows).Error
	return rows, err
}

// CountPartnerPaidCustomersSince 某时间之后首次付费的归属客户数。
func CountPartnerPaidCustomersSince(partnerId int, since int64) int64 {
	var n int64
	DB.Model(&PartnerCustomer{}).Where("partner_id = ? AND first_paid_at >= ?", partnerId, since).Count(&n)
	return n
}
