package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 内容运营：轮播 Banner / 公告 =====

// Banner 创作台首页轮播（运营维护）。
type Banner struct {
	Id        int            `json:"id" gorm:"comment:BannerID"`
	Title     string         `json:"title" gorm:"type:varchar(100);comment:标题"`
	Subtitle  string         `json:"subtitle" gorm:"type:varchar(200);default:'';comment:副标题（作者 · 说明）"`
	Tag       string         `json:"tag" gorm:"type:varchar(50);default:'';comment:左上角标签"`
	CoverUrl  string         `json:"cover_url" gorm:"type:varchar(1000);default:'';comment:封面"`
	VideoUrl  string         `json:"video_url" gorm:"type:varchar(1000);default:'';comment:背景视频（可空）"`
	LinkUrl   string         `json:"link_url" gorm:"type:varchar(1000);default:'';comment:点击跳转（站内路径或外链）"`
	Cta       string         `json:"cta" gorm:"type:varchar(50);default:'';comment:按钮文案，默认「立即观看」"`
	SortOrder int            `json:"sort_order" gorm:"default:0;index"`
	Status    int            `json:"status" gorm:"index;default:1;comment:1 上架 2 下架"`
	StartsAt  int64          `json:"starts_at" gorm:"bigint;default:0;comment:生效开始（0 不限）"`
	EndsAt    int64          `json:"ends_at" gorm:"bigint;default:0;comment:生效结束（0 不限）"`
	CreatedAt int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Banner) TableName() string { return "banner" }

// Notice 公告（消息中心）。
type Notice struct {
	Id          int            `json:"id" gorm:"comment:公告ID"`
	Title       string         `json:"title" gorm:"type:varchar(100);comment:标题"`
	Content     string         `json:"content" gorm:"type:text;comment:内容（纯文本，换行保留）"`
	Type        string         `json:"type" gorm:"type:varchar(20);default:'system';index;comment:system 系统 / activity 活动"`
	Pinned      bool           `json:"pinned" gorm:"default:false;comment:置顶"`
	Status      int            `json:"status" gorm:"index;default:1;comment:1 已发布 2 已下线"`
	PublishedAt int64          `json:"published_at" gorm:"bigint;default:0"`
	CreatedAt   int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt   int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Notice) TableName() string { return "notice" }

const (
	ContentStatusOn  = 1
	ContentStatusOff = 2

	NoticeTypeSystem   = "system"
	NoticeTypeActivity = "activity"
)

// ----- Banner -----

func (b *Banner) Insert() error {
	now := common.GetTimestamp()
	b.CreatedAt, b.UpdatedAt = now, now
	if b.Status == 0 {
		b.Status = ContentStatusOn
	}
	return DB.Create(b).Error
}

// GetActiveBanners 上架且在生效时间窗内，按 sort_order 倒序。
func GetActiveBanners() (list []*Banner, err error) {
	now := common.GetTimestamp()
	err = DB.Where("status = ? AND (starts_at = 0 OR starts_at <= ?) AND (ends_at = 0 OR ends_at > ?)", ContentStatusOn, now, now).
		Order("sort_order desc, id desc").Limit(10).Find(&list).Error
	return list, err
}

// GetBanners 管理端列表。
func GetBanners(keyword string, status *int, pageInfo *common.PageInfo) (list []*Banner, total int64, err error) {
	query := DB.Model(&Banner{})
	if keyword != "" {
		query = query.Where("title LIKE ?", "%"+keyword+"%")
	}
	if status != nil {
		query = query.Where("status = ?", *status)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("sort_order desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

func UpdateBannerFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&Banner{}).Where("id = ?", id), fields)
}

func DeleteBanner(id int) error {
	return deleteOne(DB.Where("id = ?", id), &Banner{})
}

// ----- Notice -----

func (n *Notice) Insert() error {
	now := common.GetTimestamp()
	n.CreatedAt, n.UpdatedAt = now, now
	if n.Status == 0 {
		n.Status = ContentStatusOn
	}
	if n.Type == "" {
		n.Type = NoticeTypeSystem
	}
	if n.Status == ContentStatusOn && n.PublishedAt == 0 {
		n.PublishedAt = now
	}
	return DB.Create(n).Error
}

func GetNoticeById(id int) (*Notice, error) {
	n := &Notice{}
	if err := DB.Where("id = ?", id).First(n).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return n, nil
}

// GetNotices 列表；publishedOnly=true 仅已发布，置顶优先。
func GetNotices(keyword string, status *int, pageInfo *common.PageInfo) (list []*Notice, total int64, err error) {
	query := DB.Model(&Notice{})
	if keyword != "" {
		query = query.Where("title LIKE ?", "%"+keyword+"%")
	}
	if status != nil {
		query = query.Where("status = ?", *status)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("pinned desc, published_at desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

func UpdateNoticeFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	if s, ok := fields["status"].(int); ok && s == ContentStatusOn {
		var n Notice
		if DB.Select("published_at").Where("id = ?", id).First(&n).Error == nil && n.PublishedAt == 0 {
			fields["published_at"] = common.GetTimestamp()
		}
	}
	return updateOne(DB.Model(&Notice{}).Where("id = ?", id), fields)
}

func DeleteNotice(id int) error {
	return deleteOne(DB.Where("id = ?", id), &Notice{})
}
