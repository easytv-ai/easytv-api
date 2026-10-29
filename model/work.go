package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Work 作品：从资产发布出来的成片，公开信息流的数据源。见 API.md 1.5。
type Work struct {
	Id          int  `json:"id" gorm:"comment:作品ID"`
	UserId      int  `json:"user_id" gorm:"index;comment:作者用户ID"`
	ProjectId   int  `json:"project_id" gorm:"default:0;comment:所属项目ID"`
	CanvasId    int  `json:"canvas_id" gorm:"default:0;comment:发布时所在画布ID（制作过程）"`
	ShowProcess bool `json:"show_process" gorm:"default:true;comment:播放页是否公开制作过程"`
	// 做同款（T5）：开源作品允许他人把来源画布复制到自己的项目
	SourceCanvasId int    `json:"source_canvas_id" gorm:"default:0;comment:做同款来源画布 ID（必须属于作者）"`
	SourceSkillId  int    `json:"source_skill_id" gorm:"default:0;comment:使用的 Skill ID，0 无"`
	OpenSource     int    `json:"open_source" gorm:"type:tinyint;index;default:0;comment:1 开源：允许他人做同款"`
	Lang           string `json:"lang" gorm:"type:varchar(10);index;default:'';comment:作品语种（F6 出海），空视为 zh"`
	// TemplateMeta 开源时来源画布的模型与能力快照（API.md 14.3），做同款预览据此提示模型下线 / 调价 / 能力变化
	TemplateMeta string         `json:"-" gorm:"type:text"`
	CloneCount   int            `json:"clone_count" gorm:"default:0;comment:被做同款次数"`
	AssetId      int            `json:"asset_id" gorm:"default:0;comment:成片资产ID"`
	Title        string         `json:"title" gorm:"type:varchar(200);comment:标题"`
	Description  string         `json:"description" gorm:"type:varchar(1000);default:'';comment:描述"`
	Category     string         `json:"category" gorm:"type:varchar(50);default:'';index;comment:分类（电影美学/商业广告/短剧/动画游戏/音乐 MV）"`
	CoverUrl     string         `json:"cover_url" gorm:"type:varchar(1000);default:'';comment:封面"`
	VideoUrl     string         `json:"video_url" gorm:"type:varchar(1000);comment:视频地址"`
	Duration     float64        `json:"duration" gorm:"default:0;comment:时长秒"`
	Status       int            `json:"status" gorm:"index;default:0;comment:0=草稿 1=已发布 2=已下架 -1=审核驳回"`
	Remark       string         `json:"remark" gorm:"type:varchar(255);default:'';comment:审核备注"`
	ReviewStatus int            `json:"review_status" gorm:"index;default:0;comment:发布审核 0 待审核 1 已通过 2 已驳回"`
	Featured     int            `json:"featured" gorm:"index;default:0;comment:1 精选（首页优先展示）"`
	SortOrder    int            `json:"sort_order" gorm:"default:0;comment:精选排序，越小越靠前"`
	ViewCount    int            `json:"view_count" gorm:"default:0;comment:浏览数"`
	LikeCount    int            `json:"like_count" gorm:"default:0;comment:点赞数"`
	CommentCount int            `json:"comment_count" gorm:"default:0;comment:可见评论数（含回复）"`
	PublishedAt  int64          `json:"published_at" gorm:"bigint;default:0;comment:发布时间"`
	CreatedAt    int64          `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	UpdatedAt    int64          `json:"updated_at" gorm:"bigint;comment:更新时间（Unix 秒）"`
	DeletedAt    gorm.DeletedAt `json:"deleted_at" gorm:"index;comment:删除时间"`

	// Author 列表附带作者信息（非表字段）
	Author *WorkAuthor `json:"author,omitempty" gorm:"-"`
	// Liked 当前登录用户是否已点赞（非表字段）
	Liked bool `json:"liked" gorm:"-"`
	// Source 开源作品的来源信息（非表字段，open_source=1 时由 AttachSource 填充）
	Source *WorkSource `json:"source,omitempty" gorm:"-"`
}

// WorkSource 公开详情里的做同款来源（API.md 7.2）。
type WorkSource struct {
	CanvasId   int `json:"canvas_id"`
	SkillId    int `json:"skill_id"`
	CloneCount int `json:"clone_count"`
}

// CanClone 是否允许他人做同款：开源且有来源画布。
func (w *Work) CanClone() bool {
	return w.OpenSource == 1 && w.SourceCanvasId > 0
}

// AttachSource 开源作品填充 source。
func (w *Work) AttachSource() {
	if w.CanClone() {
		w.Source = &WorkSource{CanvasId: w.SourceCanvasId, SkillId: w.SourceSkillId, CloneCount: w.CloneCount}
	}
}

// IncreaseWorkClone 做同款次数 +1。
func IncreaseWorkClone(id int) {
	DB.Model(&Work{}).Where("id = ?", id).Update("clone_count", gorm.Expr("clone_count + 1"))
}

// WorkLike 点赞关系（用户 × 作品唯一）。
type WorkLike struct {
	Id        int   `json:"id"`
	UserId    int   `json:"user_id" gorm:"uniqueIndex:idx_work_like,priority:1"`
	WorkId    int   `json:"work_id" gorm:"uniqueIndex:idx_work_like,priority:2;index"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
}

func (WorkLike) TableName() string { return "work_like" }

// ToggleWorkLike 点赞/取消点赞（已发布作品），返回最新状态与计数。
func ToggleWorkLike(userId, workId int) (liked bool, count int, err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		var w Work
		if err := tx.Where("id = ? AND status = ?", workId, WorkStatusPublished).First(&w).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		res := tx.Where("user_id = ? AND work_id = ?", userId, workId).Delete(&WorkLike{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			liked = false
			return tx.Model(&Work{}).Where("id = ? AND like_count > 0", workId).Update("like_count", gorm.Expr("like_count - 1")).Error
		}
		if err := tx.Create(&WorkLike{UserId: userId, WorkId: workId, CreatedAt: common.GetTimestamp()}).Error; err != nil {
			return err
		}
		liked = true
		return tx.Model(&Work{}).Where("id = ?", workId).Update("like_count", gorm.Expr("like_count + 1")).Error
	})
	if err != nil {
		return false, 0, err
	}
	var w Work
	DB.Select("like_count").Where("id = ?", workId).First(&w)
	return liked, w.LikeCount, nil
}

// AttachLiked 为列表填充当前用户点赞状态。
func AttachLiked(userId int, list []*Work) {
	if userId <= 0 || len(list) == 0 {
		return
	}
	ids := make([]int, 0, len(list))
	for _, w := range list {
		ids = append(ids, w.Id)
	}
	var likes []WorkLike
	DB.Where("user_id = ? AND work_id IN ?", userId, ids).Find(&likes)
	set := map[int]bool{}
	for _, l := range likes {
		set[l.WorkId] = true
	}
	for _, w := range list {
		w.Liked = set[w.Id]
	}
}

// WorkAuthor 作品作者摘要。
type WorkAuthor struct {
	Id       int    `json:"id"`
	Nickname string `json:"nickname"`
}

func (Work) TableName() string { return "work" }

const (
	WorkStatusDraft     = 0
	WorkStatusPublished = 1
	WorkStatusUnlisted  = 2
	WorkStatusRejected  = -1
)

// IsValidWorkStatus 校验状态值。
func IsValidWorkStatus(s int) bool {
	switch s {
	case WorkStatusDraft, WorkStatusPublished, WorkStatusUnlisted, WorkStatusRejected:
		return true
	}
	return false
}

// Insert 创建作品。
func (w *Work) Insert() error {
	now := common.GetTimestamp()
	w.CreatedAt = now
	w.UpdatedAt = now
	if w.Status == WorkStatusPublished && w.PublishedAt == 0 {
		w.PublishedAt = now
	}
	return DB.Create(w).Error
}

// GetUserWork 按用户 + ID 查询。
func GetUserWork(userId, id int) (*Work, error) {
	w := &Work{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(w).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return w, nil
}

// GetWorkById 任意状态（管理端 / 通知用）。
func GetWorkById(id int) (*Work, error) {
	w := &Work{}
	if err := DB.Where("id = ?", id).First(w).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return w, nil
}

// GetPublishedWork 公开详情（仅已发布）。
func GetPublishedWork(id int) (*Work, error) {
	w := &Work{}
	if err := DB.Where("id = ? AND status = ?", id, WorkStatusPublished).First(w).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	attachWorkAuthors([]*Work{w})
	return w, nil
}

// WorkFilter 作品列表过滤条件。
type WorkFilter struct {
	UserId       int
	Status       *int
	Keyword      string
	Category     string
	Featured     *int
	ReviewStatus *int
	// Lang 语种过滤（F6）：zh 含空值（历史作品）
	Lang string
}

// 作品发布审核状态
const (
	WorkReviewPending  = 0
	WorkReviewApproved = 1
	WorkReviewRejected = 2
)

// GetWorks 作品列表；Status 为 nil 查全部状态。
func GetWorks(filter WorkFilter, pageInfo *common.PageInfo) (list []*Work, total int64, err error) {
	query := DB.Model(&Work{})
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.Lang != "" {
		if filter.Lang == "zh" {
			query = query.Where("(lang = ? OR lang = '')", filter.Lang)
		} else {
			query = query.Where("lang = ?", filter.Lang)
		}
	}
	if filter.Keyword != "" {
		query = query.Where("title LIKE ?", "%"+filter.Keyword+"%")
	}
	if filter.Category != "" {
		query = query.Where("category = ?", filter.Category)
	}
	if filter.Featured != nil {
		query = query.Where("featured = ?", *filter.Featured)
	}
	if filter.ReviewStatus != nil {
		query = query.Where("review_status = ?", *filter.ReviewStatus)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "id desc"
	if filter.Featured != nil && *filter.Featured == 1 {
		// 精选列表按运营排序
		order = "sort_order asc, published_at desc, id desc"
	} else if filter.Status != nil && *filter.Status == WorkStatusPublished {
		// 公开信息流：精选优先，其余按发布时间
		order = "featured desc, sort_order asc, published_at desc, id desc"
	}
	err = query.Order(order).Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	attachWorkAuthors(list)
	return list, total, nil
}

// GetUserNicknames 批量取用户昵称（管理端列表附带 owner_name 用）。
func GetUserNicknames(ids []int) map[int]string {
	names := map[int]string{}
	if len(ids) == 0 {
		return names
	}
	var users []struct {
		Id       int
		Nickname string
		Username string
	}
	DB.Unscoped().Model(&User{}).Select("id, nickname, username").Where("id IN ?", ids).Scan(&users)
	for _, u := range users {
		if u.Nickname != "" {
			names[u.Id] = u.Nickname
		} else {
			names[u.Id] = u.Username
		}
	}
	return names
}

// attachWorkAuthors 填充作者昵称。
func attachWorkAuthors(list []*Work) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, 0, len(list))
	for _, w := range list {
		ids = append(ids, w.UserId)
	}
	names := GetUserNicknames(ids)
	for _, w := range list {
		w.Author = &WorkAuthor{Id: w.UserId, Nickname: names[w.UserId]}
	}
}

// AdminDeleteWork 管理端删除作品（软删）。
func AdminDeleteWork(id int) error {
	result := DB.Where("id = ?", id).Delete(&Work{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetWorksWithoutCover 没有封面的作品（回填用）。
func GetWorksWithoutCover(limit int) ([]*Work, error) {
	var list []*Work
	err := DB.Where("cover_url = ''").Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// SetWorkCover 写入作品封面。
func SetWorkCover(id int, coverUrl string) error {
	return DB.Model(&Work{}).Where("id = ?", id).Update("cover_url", coverUrl).Error
}

// UpdateWorkFields 用户侧更新字段。MySQL 在值未变化时 RowsAffected 为 0，因此 0 行时再确认作品是否存在。
func UpdateWorkFields(userId, id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	result := DB.Model(&Work{}).Where("id = ? AND user_id = ?", id, userId).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var n int64
		DB.Model(&Work{}).Where("id = ? AND user_id = ?", id, userId).Count(&n)
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// SetWorkStatus 管理端审核/上下架。
func SetWorkStatus(id, status int, remark string) error {
	fields := map[string]any{
		"status":     status,
		"remark":     remark,
		"updated_at": common.GetTimestamp(),
	}
	if status == WorkStatusPublished {
		fields["published_at"] = common.GetTimestamp()
	}
	result := DB.Model(&Work{}).Where("id = ?", id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AdminUpdateWork 管理端编辑作品（标题 / 分类 / 封面 / 精选 / 排序）。
func AdminUpdateWork(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&Work{}).Where("id = ?", id), fields)
}

// ReviewWork 发布审核：通过只改 review_status；驳回同时把作品置为 -1（信息流不可见）。
func ReviewWork(id, review int, remark string) error {
	fields := map[string]any{
		"review_status": review,
		"remark":        remark,
		"updated_at":    common.GetTimestamp(),
	}
	if review == WorkReviewRejected {
		fields["status"] = WorkStatusRejected
		fields["featured"] = 0
	} else if review == WorkReviewApproved {
		// 之前被驳回的作品通过复审后恢复发布
		if err := DB.Model(&Work{}).Where("id = ? AND status = ?", id, WorkStatusRejected).
			Updates(map[string]any{"status": WorkStatusPublished, "published_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
	}
	return updateOne(DB.Model(&Work{}).Where("id = ?", id), fields)
}

// DeleteUserWork 软删除作品。
func DeleteUserWork(userId, id int) error {
	result := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&Work{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// IncreaseWorkView 浏览数 +1。
func IncreaseWorkView(id int) {
	DB.Model(&Work{}).Where("id = ?", id).Update("view_count", gorm.Expr("view_count + 1"))
}

// AuthorStats 作者主页统计（仅已发布作品）。
type AuthorStats struct {
	WorkCount int64 `json:"work_count"`
	LikeTotal int64 `json:"like_total"`
	ViewTotal int64 `json:"view_total"`
}

// GetAuthorStats 已发布作品数、累计喜欢与播放。
func GetAuthorStats(userId int) AuthorStats {
	var s AuthorStats
	DB.Model(&Work{}).Select("COUNT(*) AS work_count, COALESCE(SUM(like_count), 0) AS like_total, COALESCE(SUM(view_count), 0) AS view_total").
		Where("user_id = ? AND status = ?", userId, WorkStatusPublished).Scan(&s)
	return s
}
