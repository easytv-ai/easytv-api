package model

import (
	"math/rand"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Partner 渠道伙伴（M12）：MCN / 培训机构 / KOL / 开发者，带专属码与返佣。
type Partner struct {
	Id          int     `json:"id"`
	UserId      int     `json:"user_id" gorm:"uniqueIndex"`
	Name        string  `json:"name" gorm:"type:varchar(64)"`
	Kind        string  `json:"kind" gorm:"type:varchar(20);default:'other';comment:mcn/school/kol/dev/other"`
	Contact     string  `json:"contact" gorm:"type:varchar(120);default:''"`
	Intro       string  `json:"intro" gorm:"type:varchar(500);default:''"`
	Code        string  `json:"code" gorm:"type:varchar(20);uniqueIndex;default:'';comment:审核通过时生成"`
	RebateRate  float64 `json:"rebate_rate" gorm:"type:decimal(5,4);default:0;comment:0 取 GrowthPartnerRebateRate"`
	Status      int     `json:"status" gorm:"index;default:0;comment:0 待审 1 已通过 2 已驳回 3 已停用"`
	Remark      string  `json:"remark" gorm:"type:varchar(255);default:''"`
	Customers   int     `json:"customers" gorm:"default:0"`
	PaidUsers   int     `json:"paid_users" gorm:"default:0"`
	AmountTotal float64 `json:"amount_total" gorm:"type:decimal(12,2);default:0;comment:客户累计实付"`
	RebateTotal float64 `json:"rebate_total" gorm:"type:decimal(12,2);default:0;comment:累计返佣"`
	AppliedAt   int64   `json:"applied_at" gorm:"bigint"`
	ReviewedAt  int64   `json:"reviewed_at" gorm:"bigint;default:0"`
	ReviewedBy  int     `json:"reviewed_by" gorm:"default:0"`
	CreatedAt   int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt   int64   `json:"updated_at" gorm:"bigint"`

	Nickname string `json:"nickname,omitempty" gorm:"-"`
}

func (Partner) TableName() string { return "partner" }

// PartnerCustomer 归属关系（一个用户只归属一个伙伴）。
type PartnerCustomer struct {
	Id          int     `json:"id"`
	PartnerId   int     `json:"partner_id" gorm:"index"`
	UserId      int     `json:"user_id" gorm:"uniqueIndex"`
	Source      string  `json:"source" gorm:"type:varchar(10);default:'code';comment:code/link/manual"`
	FirstPaidAt int64   `json:"first_paid_at" gorm:"bigint;default:0"`
	Amount      float64 `json:"amount" gorm:"type:decimal(12,2);default:0"`
	Rebate      float64 `json:"rebate" gorm:"type:decimal(12,2);default:0"`
	CreatedAt   int64   `json:"created_at" gorm:"bigint;index"`

	Nickname string `json:"nickname,omitempty" gorm:"-"`
}

func (PartnerCustomer) TableName() string { return "partner_customer" }

const (
	PartnerStatusPending  = 0
	PartnerStatusApproved = 1
	PartnerStatusRejected = 2
	PartnerStatusDisabled = 3
)

var PartnerKinds = map[string]bool{"mcn": true, "school": true, "kol": true, "dev": true, "other": true}

func (p *Partner) Insert() error {
	now := common.GetTimestamp()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.AppliedAt == 0 {
		p.AppliedAt = now
	}
	return DB.Create(p).Error
}

func GetPartnerByUserId(userId int) *Partner {
	p := &Partner{}
	if err := DB.Where("user_id = ?", userId).First(p).Error; err != nil {
		return nil
	}
	return p
}

func GetPartnerById(id int) (*Partner, error) {
	p := &Partner{}
	if err := DB.Where("id = ?", id).First(p).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return p, nil
}

// GetPartnerByCode 已通过的伙伴按专属码查。
func GetPartnerByCode(code string) *Partner {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil
	}
	p := &Partner{}
	if err := DB.Where("code = ? AND status = ?", code, PartnerStatusApproved).First(p).Error; err != nil {
		return nil
	}
	return p
}

func UpdatePartnerFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&Partner{}).Where("id = ?", id), fields)
}

// NewPartnerCode 生成 6 位大写专属码（去易混字符）并保证唯一。
func NewPartnerCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for i := 0; i < 20; i++ {
		b := make([]byte, 6)
		for j := range b {
			b[j] = alphabet[rand.Intn(len(alphabet))]
		}
		code := "P" + string(b)
		var n int64
		DB.Model(&Partner{}).Where("code = ?", code).Count(&n)
		if n == 0 {
			return code
		}
	}
	return ""
}

type PartnerFilter struct {
	Status  *int
	Keyword string
}

func GetPartners(f PartnerFilter, pageInfo *common.PageInfo) (list []*Partner, total int64, err error) {
	q := DB.Model(&Partner{})
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(name LIKE ? OR contact LIKE ? OR code LIKE ?)", like, like, like)
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = q.Order("status asc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	AttachPartnerNicknames(list)
	return list, total, err
}

func AttachPartnerNicknames(list []*Partner) {
	ids := make([]int, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.UserId)
	}
	names := GetUserNicknames(ids)
	for _, p := range list {
		p.Nickname = names[p.UserId]
	}
}

// BindPartnerCustomer 绑定归属（已归属则不覆盖）；返回是否新绑定。
func BindPartnerCustomer(partnerId, userId int, source string) bool {
	if partnerId <= 0 || userId <= 0 {
		return false
	}
	var n int64
	DB.Model(&PartnerCustomer{}).Where("user_id = ?", userId).Count(&n)
	if n > 0 {
		return false
	}
	pc := &PartnerCustomer{PartnerId: partnerId, UserId: userId, Source: source, CreatedAt: common.GetTimestamp()}
	if err := DB.Create(pc).Error; err != nil {
		return false
	}
	DB.Model(&Partner{}).Where("id = ?", partnerId).Update("customers", gorm.Expr("customers + 1"))
	DB.Model(&User{}).Where("id = ?", userId).Update("partner_id", partnerId)
	return true
}

func GetPartnerCustomerByUser(userId int) *PartnerCustomer {
	pc := &PartnerCustomer{}
	if err := DB.Where("user_id = ?", userId).First(pc).Error; err != nil {
		return nil
	}
	return pc
}

func GetPartnerCustomers(partnerId int, pageInfo *common.PageInfo) (list []*PartnerCustomer, total int64, err error) {
	q := DB.Model(&PartnerCustomer{}).Where("partner_id = ?", partnerId)
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = q.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
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

// AddPartnerRebate 累加一笔返佣到伙伴与归属记录。
func AddPartnerRebate(partnerId, userId int, amount, rebate float64, firstPaid bool) {
	now := common.GetTimestamp()
	fields := map[string]any{
		"amount": gorm.Expr("amount + ?", amount),
		"rebate": gorm.Expr("rebate + ?", rebate),
	}
	if firstPaid {
		fields["first_paid_at"] = now
	}
	DB.Model(&PartnerCustomer{}).Where("partner_id = ? AND user_id = ?", partnerId, userId).Updates(fields)
	pf := map[string]any{
		"amount_total": gorm.Expr("amount_total + ?", amount),
		"rebate_total": gorm.Expr("rebate_total + ?", rebate),
		"updated_at":   now,
	}
	if firstPaid {
		pf["paid_users"] = gorm.Expr("paid_users + 1")
	}
	DB.Model(&Partner{}).Where("id = ?", partnerId).Updates(pf)
}
