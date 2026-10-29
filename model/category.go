package model

import (
	"easytv-api/common"
)

// ===== 内容分类：成片分类 / Skill 分类（运营维护，见 API.md 1.7） =====

const (
	CategoryScopeWork  = "work"
	CategoryScopeSkill = "skill"
)

// Category 前台筛选标签。名称即作品 / 技能的 category 字段值（按名称关联，不存 ID）。
type Category struct {
	Id        int    `json:"id" gorm:"comment:分类ID"`
	Name      string `json:"name" gorm:"type:varchar(50);uniqueIndex:idx_category_scope_name,priority:2;comment:名称"`
	Scope     string `json:"scope" gorm:"type:varchar(20);uniqueIndex:idx_category_scope_name,priority:1;index;comment:work 成片 / skill 技能"`
	SortOrder int    `json:"sort_order" gorm:"default:0;index;comment:越小越靠前"`
	Status    int    `json:"status" gorm:"index;default:1;comment:1 启用 2 停用"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}

func (Category) TableName() string { return "category" }

// IsValidCategoryScope 校验范围。
func IsValidCategoryScope(s string) bool {
	return s == CategoryScopeWork || s == CategoryScopeSkill
}

func (cat *Category) Insert() error {
	now := common.GetTimestamp()
	cat.CreatedAt, cat.UpdatedAt = now, now
	if cat.Status == 0 {
		cat.Status = ContentStatusOn
	}
	return DB.Create(cat).Error
}

// GetActiveCategories 前台：某范围下启用的分类，按 sort_order 升序。
func GetActiveCategories(scope string) (list []*Category, err error) {
	err = DB.Where("scope = ? AND status = ?", scope, ContentStatusOn).
		Order("sort_order asc, id asc").Find(&list).Error
	return list, err
}

// GetCategories 管理端：scope 为空查全部范围，status 为 nil 查全部状态。
func GetCategories(scope, keyword string, status *int) (list []*Category, err error) {
	query := DB.Model(&Category{})
	if scope != "" {
		query = query.Where("scope = ?", scope)
	}
	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}
	if status != nil {
		query = query.Where("status = ?", *status)
	}
	err = query.Order("scope asc, sort_order asc, id asc").Find(&list).Error
	return list, err
}

func GetCategoryById(id int) (*Category, error) {
	cat := &Category{}
	if err := DB.Where("id = ?", id).First(cat).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return cat, nil
}

// IsCategoryNameTaken 同范围内名称唯一；excludeId 用于编辑时排除自身。
func IsCategoryNameTaken(scope, name string, excludeId int) bool {
	var count int64
	DB.Model(&Category{}).Where("scope = ? AND name = ? AND id <> ?", scope, name, excludeId).Count(&count)
	return count > 0
}

func UpdateCategoryFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&Category{}).Where("id = ?", id), fields)
}

func DeleteCategory(id int) error {
	return deleteOne(DB.Where("id = ?", id), &Category{})
}
