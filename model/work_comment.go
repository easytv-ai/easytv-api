package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 作品评论（API.md 1.10）=====

const (
	CommentStatusNormal = 1 // 正常展示
	CommentStatusHidden = 2 // 管理端隐藏（举报成立 / 人工处理）

	CommentMaxLen = 500
)

// WorkComment 作品评论：一级评论 + 一层回复（回复的回复仍挂在同一个一级评论下，用 reply_to 标注对象）。
type WorkComment struct {
	Id         int            `json:"id" gorm:"comment:评论ID"`
	WorkId     int            `json:"work_id" gorm:"index:idx_comment_work,priority:1;comment:作品ID"`
	UserId     int            `json:"user_id" gorm:"index;comment:评论人"`
	ParentId   int            `json:"parent_id" gorm:"index:idx_comment_work,priority:2;default:0;comment:一级评论ID，0 表示一级"`
	ReplyToId  int            `json:"reply_to_id" gorm:"default:0;comment:被回复的用户ID（回复时）"`
	Content    string         `json:"content" gorm:"type:varchar(500);comment:内容"`
	Status     int            `json:"status" gorm:"index;default:1;comment:1 正常 2 已隐藏"`
	Remark     string         `json:"remark" gorm:"type:varchar(255);default:'';comment:处理备注"`
	ReplyCount int            `json:"reply_count" gorm:"default:0;comment:可见回复数（一级评论用）"`
	CreatedAt  int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt  int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt  gorm.DeletedAt `json:"-" gorm:"index"`

	// 非表字段
	Author    *WorkAuthor    `json:"author,omitempty" gorm:"-"`
	ReplyTo   *WorkAuthor    `json:"reply_to,omitempty" gorm:"-"`
	Replies   []*WorkComment `json:"replies" gorm:"-"`
	Mine      bool           `json:"mine" gorm:"-"`
	WorkTitle string         `json:"work_title,omitempty" gorm:"-"`
}

func (WorkComment) TableName() string { return "work_comment" }

var ErrCommentTooLong = errors.New("评论最多 500 字")

// CreateWorkComment 发表评论；parentId > 0 时为回复，回复对象可以是一级评论或它下面的回复。
func CreateWorkComment(userId, workId, parentId int, content string) (*WorkComment, error) {
	if len([]rune(content)) > CommentMaxLen {
		return nil, ErrCommentTooLong
	}
	var w Work
	if err := DB.Where("id = ? AND status = ?", workId, WorkStatusPublished).First(&w).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	now := common.GetTimestamp()
	c := &WorkComment{
		WorkId:    workId,
		UserId:    userId,
		Content:   content,
		Status:    CommentStatusNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if parentId > 0 {
		target := &WorkComment{}
		if err := DB.Where("id = ? AND work_id = ? AND status = ?", parentId, workId, CommentStatusNormal).First(target).Error; err != nil {
			return nil, wrapNotFound(err)
		}
		c.ReplyToId = target.UserId
		if target.ParentId > 0 {
			c.ParentId = target.ParentId
		} else {
			c.ParentId = target.Id
		}
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		if c.ParentId > 0 {
			if err := tx.Model(&WorkComment{}).Where("id = ?", c.ParentId).Update("reply_count", gorm.Expr("reply_count + 1")).Error; err != nil {
				return err
			}
		}
		return recountWorkComments(tx, workId)
	})
	if err != nil {
		return nil, err
	}
	attachCommentRefs([]*WorkComment{c}, userId)
	return c, nil
}

// recountWorkComments 重算作品的可见评论数（含回复）。先查数再更新，避免 MySQL 1093（同表子查询更新）。
func recountWorkComments(tx *gorm.DB, workId int) error {
	var n int64
	if err := tx.Model(&WorkComment{}).Where("work_id = ? AND status = ?", workId, CommentStatusNormal).Count(&n).Error; err != nil {
		return err
	}
	return tx.Model(&Work{}).Where("id = ?", workId).Update("comment_count", n).Error
}

// recountReplies 重算一级评论的可见回复数。
func recountReplies(tx *gorm.DB, parentId int) error {
	if parentId <= 0 {
		return nil
	}
	var n int64
	if err := tx.Model(&WorkComment{}).Where("parent_id = ? AND status = ?", parentId, CommentStatusNormal).Count(&n).Error; err != nil {
		return err
	}
	return tx.Model(&WorkComment{}).Where("id = ?", parentId).Update("reply_count", n).Error
}

// GetWorkComments 公开列表：一级评论分页（最新在前），每条附最多 repliesPerTop 条回复（最早在前）。
func GetWorkComments(workId, viewerId int, pageInfo *common.PageInfo, repliesPerTop int) (list []*WorkComment, total int64, err error) {
	query := DB.Model(&WorkComment{}).Where("work_id = ? AND parent_id = 0 AND status = ?", workId, CommentStatusNormal)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	if len(list) == 0 {
		return []*WorkComment{}, total, nil
	}
	parentIds := make([]int, 0, len(list))
	for _, c := range list {
		parentIds = append(parentIds, c.Id)
	}
	var replies []*WorkComment
	if err = DB.Where("parent_id IN ? AND status = ?", parentIds, CommentStatusNormal).Order("id asc").Find(&replies).Error; err != nil {
		return nil, 0, err
	}
	byParent := map[int][]*WorkComment{}
	for _, r := range replies {
		if repliesPerTop > 0 && len(byParent[r.ParentId]) >= repliesPerTop {
			continue
		}
		byParent[r.ParentId] = append(byParent[r.ParentId], r)
	}
	all := append([]*WorkComment{}, list...)
	for _, c := range list {
		c.Replies = byParent[c.Id]
		if c.Replies == nil {
			c.Replies = []*WorkComment{}
		}
		all = append(all, c.Replies...)
	}
	attachCommentRefs(all, viewerId)
	return list, total, nil
}

// GetCommentReplies 某条一级评论的全部可见回复（展开「查看更多回复」）。
func GetCommentReplies(parentId, viewerId int, pageInfo *common.PageInfo) (list []*WorkComment, total int64, err error) {
	query := DB.Model(&WorkComment{}).Where("parent_id = ? AND status = ?", parentId, CommentStatusNormal)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = query.Order("id asc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	attachCommentRefs(list, viewerId)
	return list, total, nil
}

func attachCommentRefs(list []*WorkComment, viewerId int) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, 0, len(list)*2)
	for _, c := range list {
		ids = append(ids, c.UserId)
		if c.ReplyToId > 0 {
			ids = append(ids, c.ReplyToId)
		}
	}
	names := GetUserNicknames(ids)
	for _, c := range list {
		c.Author = &WorkAuthor{Id: c.UserId, Nickname: names[c.UserId]}
		if c.ReplyToId > 0 {
			c.ReplyTo = &WorkAuthor{Id: c.ReplyToId, Nickname: names[c.ReplyToId]}
		}
		c.Mine = viewerId > 0 && c.UserId == viewerId
	}
}

// GetWorkCommentById 任意状态。
func GetWorkCommentById(id int) (*WorkComment, error) {
	c := &WorkComment{}
	if err := DB.Where("id = ?", id).First(c).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return c, nil
}

// DeleteWorkComment 评论人本人或作品作者删除（软删）；一级评论连带回复一起删。
func DeleteWorkComment(userId, id int) error {
	c, err := GetWorkCommentById(id)
	if err != nil {
		return err
	}
	if c.UserId != userId {
		w, err := GetWorkById(c.WorkId)
		if err != nil || w.UserId != userId {
			return ErrNotFound
		}
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if c.ParentId == 0 {
			if err := tx.Where("parent_id = ?", c.Id).Delete(&WorkComment{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(c).Error; err != nil {
			return err
		}
		if err := recountReplies(tx, c.ParentId); err != nil {
			return err
		}
		return recountWorkComments(tx, c.WorkId)
	})
}

// ----- 管理端 -----

// CommentAdminFilter 管理端评论列表过滤。
type CommentAdminFilter struct {
	Status  *int
	WorkId  int
	UserId  int
	Keyword string
}

// GetWorkCommentsAdmin 管理端列表（附作者昵称与作品标题），最新在前。
func GetWorkCommentsAdmin(filter CommentAdminFilter, pageInfo *common.PageInfo) (list []*WorkComment, total int64, err error) {
	query := DB.Model(&WorkComment{})
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.WorkId > 0 {
		query = query.Where("work_id = ?", filter.WorkId)
	}
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		query = query.Where("content LIKE ? OR work_id IN (?)", like,
			DB.Unscoped().Model(&Work{}).Select("id").Where("title LIKE ?", like))
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	attachCommentRefs(list, 0)
	attachCommentWorkTitles(list)
	return list, total, nil
}

func attachCommentWorkTitles(list []*WorkComment) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.WorkId)
	}
	var works []struct {
		Id    int
		Title string
	}
	DB.Unscoped().Model(&Work{}).Select("id, title").Where("id IN ?", ids).Scan(&works)
	titles := map[int]string{}
	for _, w := range works {
		titles[w.Id] = w.Title
	}
	for _, c := range list {
		c.WorkTitle = titles[c.WorkId]
	}
}

// SetWorkCommentStatus 管理端隐藏 / 恢复评论，并同步作品评论数与回复数。
func SetWorkCommentStatus(id, status int, remark string) (*WorkComment, error) {
	c, err := GetWorkCommentById(id)
	if err != nil {
		return nil, err
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(c).Updates(map[string]any{"status": status, "remark": remark, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		if err := recountReplies(tx, c.ParentId); err != nil {
			return err
		}
		return recountWorkComments(tx, c.WorkId)
	})
	if err != nil {
		return nil, err
	}
	c.Status = status
	c.Remark = remark
	return c, nil
}

// AdminDeleteWorkComment 管理端删除（软删，一级评论连带回复）。
func AdminDeleteWorkComment(id int) error {
	c, err := GetWorkCommentById(id)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if c.ParentId == 0 {
			if err := tx.Where("parent_id = ?", c.Id).Delete(&WorkComment{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(c).Error; err != nil {
			return err
		}
		if err := recountReplies(tx, c.ParentId); err != nil {
			return err
		}
		return recountWorkComments(tx, c.WorkId)
	})
}
