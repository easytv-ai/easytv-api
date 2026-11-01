package service

import (
	"errors"
	"sort"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 代理商后台（easytv-partner，API.md 9.3.1）：在 M12 渠道伙伴之上加一层「伙伴自己看」的聚合接口。
// 数据边界：只返回归属客户的聚合与脱敏昵称，不含客户的手机号 / 邮箱 / 订单明细。

var (
	ErrPartnerNotApplied  = errors.New("尚未申请成为渠道伙伴")
	ErrPartnerNotApproved = errors.New("伙伴资格尚未通过审核")
)

// PartnerConsoleMonth 本月汇总。
type PartnerConsoleMonth struct {
	NewCustomers int64   `json:"new_customers"`
	PaidUsers    int64   `json:"paid_users"`
	Orders       int     `json:"orders"`
	Amount       float64 `json:"amount"`
	Rebate       float64 `json:"rebate"`
}

// PartnerConsoleOverview GET /user/partner/console/overview 响应。
type PartnerConsoleOverview struct {
	*PartnerDashboard
	RebateMonths int                 `json:"rebate_months"`
	Month        PartnerConsoleMonth `json:"month"`
	// ServerTime 服务器当前时间（前端算「本月」区间用）
	ServerTime int64 `json:"server_time"`
}

// GetPartnerConsoleOverview 看板总览；未申请返回 ErrPartnerNotApplied（前端据此进入申请页）。
func GetPartnerConsoleOverview(userId int) (*PartnerConsoleOverview, error) {
	d := GetPartnerDashboard(userId)
	if d == nil || d.Partner == nil {
		return nil, ErrPartnerNotApplied
	}
	out := &PartnerConsoleOverview{
		PartnerDashboard: d,
		RebateMonths:     setting.GetGrowthConfig().PartnerRebateMonths,
		ServerTime:       common.GetTimestamp(),
	}
	if d.Partner.Status != model.PartnerStatusApproved {
		return out, nil
	}
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	joins, _ := model.GetPartnerCustomerJoinsSince(d.Partner.Id, monthStart)
	out.Month.NewCustomers = int64(len(joins))
	out.Month.PaidUsers = model.CountPartnerPaidCustomersSince(d.Partner.Id, monthStart)
	rows, _ := model.GetPartnerRebateRowsSince(d.Partner.UserId, monthStart)
	for _, r := range rows {
		out.Month.Orders++
		out.Month.Amount += r.SourceAmount
		out.Month.Rebate += r.Amount
	}
	out.Month.Amount = round2(out.Month.Amount)
	out.Month.Rebate = round2(out.Month.Rebate)
	return out, nil
}

// PartnerStatRow 趋势图一天。
type PartnerStatRow struct {
	Date         string  `json:"date"` // YYYY-MM-DD（服务器时区）
	NewCustomers int     `json:"new_customers"`
	Orders       int     `json:"orders"`
	Amount       float64 `json:"amount"`
	Rebate       float64 `json:"rebate"`
}

// PartnerConsoleStats GET /user/partner/console/stats 响应。
type PartnerConsoleStats struct {
	Rows      []PartnerStatRow `json:"rows"`
	StartDate string           `json:"start_date"`
	EndDate   string           `json:"end_date"`
	Totals    PartnerStatRow   `json:"totals"`
}

const partnerStatsMaxDays = 90

// GetPartnerConsoleStats 最近 days 天（含今天）的日趋势；缺的日期补 0。
func GetPartnerConsoleStats(userId int, days int) (*PartnerConsoleStats, error) {
	p, err := requireApprovedPartner(userId)
	if err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 30
	}
	if days > partnerStatsMaxDays {
		days = partnerStatsMaxDays
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	joins, err := model.GetPartnerCustomerJoinsSince(p.Id, start.Unix())
	if err != nil {
		return nil, err
	}
	rows, err := model.GetPartnerRebateRowsSince(p.UserId, start.Unix())
	if err != nil {
		return nil, err
	}
	joinTs := make([]int64, 0, len(joins))
	for _, j := range joins {
		joinTs = append(joinTs, j.CreatedAt)
	}
	return buildPartnerStats(start, now, joinTs, rows), nil
}

// buildPartnerStats 把归属时间点与返佣流水按天聚合，区间内每天都有一行（纯函数，便于测试）。
func buildPartnerStats(start, end time.Time, joinTs []int64, rebates []model.PartnerRebateRow) *PartnerConsoleStats {
	const layout = "2006-01-02"
	loc := start.Location()
	byDate := map[string]*PartnerStatRow{}
	dates := []string{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		key := d.Format(layout)
		byDate[key] = &PartnerStatRow{Date: key}
		dates = append(dates, key)
	}
	for _, ts := range joinTs {
		if r, ok := byDate[time.Unix(ts, 0).In(loc).Format(layout)]; ok {
			r.NewCustomers++
		}
	}
	for _, b := range rebates {
		if r, ok := byDate[time.Unix(b.CreatedAt, 0).In(loc).Format(layout)]; ok {
			r.Orders++
			r.Amount += b.SourceAmount
			r.Rebate += b.Amount
		}
	}
	sort.Strings(dates)
	out := &PartnerConsoleStats{Rows: make([]PartnerStatRow, 0, len(dates))}
	if len(dates) > 0 {
		out.StartDate, out.EndDate = dates[0], dates[len(dates)-1]
	}
	for _, key := range dates {
		r := byDate[key]
		r.Amount, r.Rebate = round2(r.Amount), round2(r.Rebate)
		out.Rows = append(out.Rows, *r)
		out.Totals.NewCustomers += r.NewCustomers
		out.Totals.Orders += r.Orders
		out.Totals.Amount += r.Amount
		out.Totals.Rebate += r.Rebate
	}
	out.Totals.Amount, out.Totals.Rebate = round2(out.Totals.Amount), round2(out.Totals.Rebate)
	return out
}

// PartnerConsoleCustomer 客户列表行（昵称脱敏）。
type PartnerConsoleCustomer struct {
	Id          int     `json:"id"`
	UserId      int     `json:"user_id"`
	Nickname    string  `json:"nickname"`
	Source      string  `json:"source"`
	FirstPaidAt int64   `json:"first_paid_at"`
	Amount      float64 `json:"amount"`
	Rebate      float64 `json:"rebate"`
	CreatedAt   int64   `json:"created_at"`
}

// GetPartnerConsoleCustomers 我的归属客户分页。
func GetPartnerConsoleCustomers(userId int, f model.PartnerCustomerFilter, pageInfo *common.PageInfo) ([]PartnerConsoleCustomer, int64, error) {
	p, err := requireApprovedPartner(userId)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := model.GetPartnerCustomersByFilter(p.Id, f, pageInfo)
	if err != nil {
		return nil, 0, err
	}
	items := make([]PartnerConsoleCustomer, 0, len(list))
	for _, c := range list {
		items = append(items, PartnerConsoleCustomer{
			Id: c.Id, UserId: c.UserId, Nickname: model.MaskNickname(c.Nickname), Source: c.Source,
			FirstPaidAt: c.FirstPaidAt, Amount: c.Amount, Rebate: c.Rebate, CreatedAt: c.CreatedAt,
		})
	}
	return items, total, nil
}

// PartnerConsoleBonusCode 我的口令及效果。
type PartnerConsoleBonusCode struct {
	*model.BonusCode
	Stats model.BonusCodeStats `json:"stats"`
}

// GetPartnerConsoleBonusCodes 归属到我（partner_user_id）的口令红包（只读；创建仍在运营后台）。
func GetPartnerConsoleBonusCodes(userId int, pageInfo *common.PageInfo) ([]PartnerConsoleBonusCode, int64, error) {
	p, err := requireApprovedPartner(userId)
	if err != nil {
		return nil, 0, err
	}
	list, total, err := model.GetBonusCodes(model.BonusCodeFilter{Status: -1, PartnerUserId: p.UserId}, pageInfo)
	if err != nil {
		return nil, 0, err
	}
	items := make([]PartnerConsoleBonusCode, 0, len(list))
	for _, b := range list {
		items = append(items, PartnerConsoleBonusCode{BonusCode: b, Stats: model.GetBonusCodeStats(b)})
	}
	return items, total, nil
}

// UpdatePartnerProfile 伙伴自助改资料：
// 待审 / 已驳回可改全部字段（已驳回改完自动重新进入待审）；已通过只能改联系方式与介绍；已停用不可改。
func UpdatePartnerProfile(userId int, name, kind, contact, intro string) (*model.Partner, error) {
	p := model.GetPartnerByUserId(userId)
	if p == nil {
		return nil, ErrPartnerNotApplied
	}
	if p.Status == model.PartnerStatusDisabled {
		return nil, errors.New("伙伴资格已停用，如需恢复请联系我们")
	}
	name = strings.TrimSpace(name)
	contact = strings.TrimSpace(contact)
	intro = strings.TrimSpace(intro)
	if contact == "" {
		return nil, errors.New("请留一个联系方式，方便我们联系你")
	}
	fields := map[string]any{
		"contact": truncate(contact, 120),
		"intro":   truncate(intro, 500),
	}
	if p.Status == model.PartnerStatusApproved {
		if (name != "" && name != p.Name) || (kind != "" && kind != p.Kind) {
			return nil, errors.New("审核通过后名称与类型不可修改，如需变更请联系我们")
		}
	} else {
		if name == "" {
			name = p.Name
		}
		if n := len([]rune(name)); n < 2 || n > 32 {
			return nil, errors.New("请填写 2~32 字的机构或个人名称")
		}
		if kind == "" {
			kind = p.Kind
		}
		if !model.PartnerKinds[kind] {
			kind = "other"
		}
		fields["name"], fields["kind"] = name, kind
		if p.Status == model.PartnerStatusRejected {
			// 重新申请：回到待审，清掉上次驳回原因
			fields["status"] = model.PartnerStatusPending
			fields["applied_at"] = common.GetTimestamp()
			fields["reviewed_at"] = 0
			fields["reviewed_by"] = 0
			fields["remark"] = ""
			Track(userId, "partner_reapply", map[string]any{"partner_id": p.Id, "kind": kind})
		}
	}
	if err := model.UpdatePartnerFields(p.Id, fields); err != nil {
		return nil, err
	}
	return model.GetPartnerById(p.Id)
}

// requireApprovedPartner 取当前用户已通过审核的伙伴记录。
func requireApprovedPartner(userId int) (*model.Partner, error) {
	p := model.GetPartnerByUserId(userId)
	if p == nil {
		return nil, ErrPartnerNotApplied
	}
	if p.Status != model.PartnerStatusApproved {
		return nil, ErrPartnerNotApproved
	}
	return p, nil
}
