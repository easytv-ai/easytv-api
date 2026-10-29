package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// SkillFavorite Skill 收藏关系（用户 × 技能唯一）。
type SkillFavorite struct {
	Id        int   `json:"id"`
	UserId    int   `json:"user_id" gorm:"uniqueIndex:idx_skill_fav,priority:1"`
	SkillId   int   `json:"skill_id" gorm:"uniqueIndex:idx_skill_fav,priority:2;index"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
}

func (SkillFavorite) TableName() string { return "skill_favorite" }

// ToggleSkillFavorite 收藏 / 取消收藏（仅已上架的官方技能或自己的技能），返回最新状态与收藏数。
func ToggleSkillFavorite(userId, skillId int) (favorited bool, stars int, err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		var s Skill
		if err := tx.Where("id = ?", skillId).First(&s).Error; err != nil {
			return wrapNotFound(err)
		}
		if s.Status != SkillStatusOn && s.UserId != userId {
			return ErrNotFound
		}
		res := tx.Where("user_id = ? AND skill_id = ?", userId, skillId).Delete(&SkillFavorite{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			favorited = false
			if err := tx.Model(&Skill{}).Where("id = ? AND stars > 0", skillId).Update("stars", gorm.Expr("stars - 1")).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Create(&SkillFavorite{UserId: userId, SkillId: skillId, CreatedAt: common.GetTimestamp()}).Error; err != nil {
				return err
			}
			favorited = true
			if err := tx.Model(&Skill{}).Where("id = ?", skillId).Update("stars", gorm.Expr("stars + 1")).Error; err != nil {
				return err
			}
		}
		var fresh Skill
		if err := tx.Select("stars").Where("id = ?", skillId).First(&fresh).Error; err != nil {
			return err
		}
		stars = fresh.Stars
		return nil
	})
	return favorited, stars, err
}

// AttachSkillFavorited 标记列表中当前用户已收藏的技能。
func AttachSkillFavorited(userId int, list []*Skill) {
	if userId <= 0 || len(list) == 0 {
		return
	}
	ids := make([]int, 0, len(list))
	for _, s := range list {
		ids = append(ids, s.Id)
	}
	var favIds []int
	DB.Model(&SkillFavorite{}).Where("user_id = ? AND skill_id IN ?", userId, ids).Pluck("skill_id", &favIds)
	set := map[int]bool{}
	for _, id := range favIds {
		set[id] = true
	}
	for _, s := range list {
		s.Favorited = set[s.Id]
	}
}

// GetFavoriteSkills 我的收藏（按收藏时间倒序），已下架的官方技能不再出现；支持分类与关键字（标题/描述/作者）过滤。
func GetFavoriteSkills(userId int, category, keyword string, pageInfo *common.PageInfo) (list []*Skill, total int64, err error) {
	base := DB.Table("skill").
		Joins("JOIN skill_favorite ON skill_favorite.skill_id = skill.id AND skill_favorite.user_id = ?", userId).
		Where("skill.deleted_at IS NULL AND (skill.status = ? OR skill.user_id = ?)", SkillStatusOn, userId)
	if category != "" {
		base = base.Where("skill.category = ?", category)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		base = base.Where("(skill.title LIKE ? OR skill.description LIKE ? OR skill.author_name LIKE ?)", like, like, like)
	}
	if err = base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = base.Select("skill.*").Order("skill_favorite.id desc").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Scan(&list).Error
	if err != nil {
		return nil, 0, err
	}
	for _, s := range list {
		s.Favorited = true
	}
	return list, total, nil
}

// IsSkillFavorited 是否已收藏。
func IsSkillFavorited(userId, skillId int) bool {
	var n int64
	DB.Model(&SkillFavorite{}).Where("user_id = ? AND skill_id = ?", userId, skillId).Count(&n)
	return n > 0
}

var _ = errors.New
