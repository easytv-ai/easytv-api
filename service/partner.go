package service

import (
	"errors"
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"github.com/shopspring/decimal"
)

// 渠道伙伴计划（M12）：申请与审核、注册归因、充值返佣（记人民币入 quota，复用现有提现）。

// ApplyPartner 提交申请；已申请过返回当前记录。
func ApplyPartner(userId int, name, kind, contact, intro string) (*model.Partner, error) {
	if exist := model.GetPartnerByUserId(userId); exist != nil {
		return exist, nil
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 2 || len([]rune(name)) > 32 {
		return nil, errors.New("请填写 2~32 字的机构或个人名称")
	}
	if !model.PartnerKinds[kind] {
		kind = "other"
	}
	if strings.TrimSpace(contact) == "" {
		return nil, errors.New("请留一个联系方式，方便我们联系你")
	}
	p := &model.Partner{
		UserId: userId, Name: name, Kind: kind,
		Contact: truncate(strings.TrimSpace(contact), 120), Intro: truncate(strings.TrimSpace(intro), 500),
		Status: model.PartnerStatusPending,
	}
	if err := p.Insert(); err != nil {
		return nil, err
	}
	Track(userId, "partner_apply", map[string]any{"partner_id": p.Id, "kind": kind})
	return p, nil
}

// ReviewPartner 审核：通过时生成专属码并发通知。
func ReviewPartner(id, adminId int, action string, rebateRate *float64, remark string) (*model.Partner, error) {
	p, err := model.GetPartnerById(id)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"reviewed_at": common.GetTimestamp(), "reviewed_by": adminId, "remark": truncate(strings.TrimSpace(remark), 255)}
	if rebateRate != nil {
		if *rebateRate < 0 || *rebateRate > 1 {
			return nil, errors.New("返佣比例需在 0~1 之间")
		}
		fields["rebate_rate"] = *rebateRate
	}
	title, content := "", ""
	switch action {
	case "approve":
		fields["status"] = model.PartnerStatusApproved
		if p.Code == "" {
			code := model.NewPartnerCode()
			if code == "" {
				return nil, errors.New("生成专属码失败，请重试")
			}
			fields["code"] = code
			p.Code = code
		}
		rate := p.RebateRate
		if rebateRate != nil {
			rate = *rebateRate
		}
		if rate <= 0 {
			rate = setting.GetGrowthConfig().PartnerRebateRate
		}
		title = "渠道伙伴申请已通过"
		content = fmt.Sprintf("你的专属码是 %s，客户充值按 %.0f%% 返佣，可在设置里查看看板与提现。", p.Code, rate*100)
	case "reject":
		if strings.TrimSpace(remark) == "" {
			return nil, errors.New("驳回请填写原因")
		}
		fields["status"] = model.PartnerStatusRejected
		title, content = "渠道伙伴申请未通过", truncate(remark, 200)
	case "disable":
		fields["status"] = model.PartnerStatusDisabled
		title, content = "渠道伙伴资格已停用", "如有疑问请联系我们。已归属的客户关系保留，但不再产生新的返佣。"
	default:
		return nil, errors.New("无效的操作")
	}
	if err := model.UpdatePartnerFields(id, fields); err != nil {
		return nil, err
	}
	model.PushNotification(p.UserId, model.NotificationTypeSystem, title, content, "/settings?tab=partner-program")
	return model.GetPartnerById(id)
}

// BindPartnerOnRegister 注册时按 pcode 绑定归属（AfterRegister 调用）。
func BindPartnerOnRegister(userId int, pcode string) {
	p := model.GetPartnerByCode(pcode)
	if p == nil || p.UserId == userId {
		return
	}
	if model.BindPartnerCustomer(p.Id, userId, "code") {
		Track(userId, "partner_bind", map[string]any{"partner_id": p.Id, "code": p.Code})
	}
}

// onPartnerPaid 订单履约后给归属伙伴返佣（记人民币，入 quota，可提现）。
func onPartnerPaid(o *model.Order) {
	if o == nil || o.Price <= 0 {
		return
	}
	pc := model.GetPartnerCustomerByUser(o.UserId)
	if pc == nil {
		return
	}
	p, err := model.GetPartnerById(pc.PartnerId)
	if err != nil || p.Status != model.PartnerStatusApproved || p.UserId == o.UserId {
		return
	}
	cfg := setting.GetGrowthConfig()
	// 归属满 N 个月后不再返佣
	if cfg.PartnerRebateMonths > 0 {
		deadline := model.AddMonths(pc.CreatedAt, cfg.PartnerRebateMonths)
		if common.GetTimestamp() > deadline {
			return
		}
	}
	rate := p.RebateRate
	if rate <= 0 {
		rate = cfg.PartnerRebateRate
	}
	if rate <= 0 {
		return
	}
	// 同一订单只返一次
	var dup int64
	model.DB.Model(&model.PromoterBill{}).
		Where("user_id = ? AND biz_type = ? AND source_no = ?", p.UserId, model.PromoterBillTypePartnerRebate, o.OrderNo).Count(&dup)
	if dup > 0 {
		return
	}
	rebate := decimal.NewFromFloat(o.Price).Mul(decimal.NewFromFloat(rate)).Round(2)
	if rebate.LessThanOrEqual(decimal.Zero) {
		return
	}
	rebateYuan, _ := rebate.Float64()
	quota := int(rebate.Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	if err := model.IncreaseUserQuota(p.UserId, quota); err != nil {
		common.SysError(fmt.Sprintf("partner rebate quota failed partner=%d order=%s: %v", p.Id, o.OrderNo, err))
		return
	}
	balance := 0.0
	if q, err := model.GetUserQuota(p.UserId); err == nil {
		balance, _ = decimal.NewFromInt(int64(q)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Float64()
	}
	bill := &model.PromoterBill{
		UserId: p.UserId, BizType: model.PromoterBillTypePartnerRebate, Amount: rebateYuan, Balance: balance,
		FromUserId: o.UserId, SourceNo: o.OrderNo, SourceAmount: o.Price, Rate: rate,
		Status: model.PromoterBillStatusValid, CreatedAt: common.GetTimestamp(),
		Remark: fmt.Sprintf("渠道客户充值返佣 %.2f 元", rebateYuan),
	}
	if err := model.DB.Create(bill).Error; err != nil {
		common.SysError(fmt.Sprintf("partner rebate bill failed partner=%d: %v", p.Id, err))
		return
	}
	model.AddPartnerRebate(p.Id, o.UserId, o.Price, rebateYuan, pc.FirstPaidAt == 0)
	Track(p.UserId, "partner_rebate", map[string]any{"partner_id": p.Id, "order_no": o.OrderNo, "rebate": rebateYuan})
}

// revokePartnerRebate 退款时冲回伙伴返佣。
func revokePartnerRebate(o *model.Order) {
	if o == nil {
		return
	}
	var bill model.PromoterBill
	if err := model.DB.Where("biz_type = ? AND source_no = ? AND status = ?",
		model.PromoterBillTypePartnerRebate, o.OrderNo, model.PromoterBillStatusValid).First(&bill).Error; err != nil {
		return
	}
	quota := int(decimal.NewFromFloat(bill.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	if err := model.DecreaseUserQuota(bill.UserId, quota); err != nil {
		common.SysError(fmt.Sprintf("partner rebate revoke quota failed bill=%d: %v", bill.Id, err))
	}
	model.DB.Model(&model.PromoterBill{}).Where("id = ?", bill.Id).Update("status", model.PromoterBillStatusRevoked)
	_ = model.DB.Create(&model.PromoterBill{
		UserId: bill.UserId, BizType: model.PromoterBillTypeRebateRevoke, Amount: -bill.Amount,
		FromUserId: bill.FromUserId, SourceNo: o.OrderNo, Status: model.PromoterBillStatusValid,
		CreatedAt: common.GetTimestamp(), Remark: "订单退款，渠道返佣冲回",
	}).Error
}

// PartnerDashboard GET /user/partner/me 响应。
type PartnerDashboard struct {
	Partner      *model.Partner          `json:"partner"`
	Link         string                  `json:"link"`
	Customers    int                     `json:"customers"`
	PaidUsers    int                     `json:"paid_users"`
	AmountTotal  float64                 `json:"amount_total"`
	RebateTotal  float64                 `json:"rebate_total"`
	Withdrawable float64                 `json:"withdrawable"`
	RebateRate   float64                 `json:"rebate_rate"`
	Recent       []PartnerRecentCustomer `json:"recent"`
}

type PartnerRecentCustomer struct {
	Nickname  string  `json:"nickname"`
	Amount    float64 `json:"amount"`
	Rebate    float64 `json:"rebate"`
	CreatedAt int64   `json:"created_at"`
}

// GetPartnerDashboard 未申请返回 nil。
func GetPartnerDashboard(userId int) *PartnerDashboard {
	p := model.GetPartnerByUserId(userId)
	if p == nil {
		return nil
	}
	cfg := setting.GetGrowthConfig()
	rate := p.RebateRate
	if rate <= 0 {
		rate = cfg.PartnerRebateRate
	}
	d := &PartnerDashboard{
		Partner: p, Customers: p.Customers, PaidUsers: p.PaidUsers,
		AmountTotal: p.AmountTotal, RebateTotal: p.RebateTotal, RebateRate: rate,
		Recent: []PartnerRecentCustomer{},
	}
	if p.Status == model.PartnerStatusApproved && p.Code != "" {
		d.Link = strings.TrimRight(cfg.InviteBaseUrl, "/") + "/partner/" + p.Code
	}
	if q, err := model.GetUserQuota(userId); err == nil {
		d.Withdrawable, _ = decimal.NewFromInt(int64(q)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Round(2).Float64()
	}
	list, _, err := model.GetPartnerCustomers(p.Id, &common.PageInfo{Page: 1, PageSize: 20})
	if err == nil {
		for _, c := range list {
			d.Recent = append(d.Recent, PartnerRecentCustomer{Nickname: maskNickname(c.Nickname), Amount: c.Amount, Rebate: c.Rebate, CreatedAt: c.CreatedAt})
		}
	}
	return d
}
