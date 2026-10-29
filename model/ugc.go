package model

import (
	"strings"

	"easytv-api/common"
)

// UgcSubmission 站外发布返积分投稿（T31）。
type UgcSubmission struct {
	Id           int    `json:"id"`
	UserId       int    `json:"user_id" gorm:"index"`
	WorkId       int    `json:"work_id" gorm:"default:0"`
	Platform     string `json:"platform" gorm:"type:varchar(20);index"`
	Url          string `json:"url" gorm:"type:varchar(500);uniqueIndex"`
	DurationTier int    `json:"duration_tier" gorm:"default:0;comment:30/60/120"`
	Likes        int    `json:"likes" gorm:"default:0"`
	Status       int    `json:"status" gorm:"index;default:0;comment:0 待审 1 通过 2 驳回"`
	Points       int    `json:"points" gorm:"default:0"`
	Viral        bool   `json:"viral" gorm:"default:false"`
	ReviewedBy   int    `json:"reviewed_by" gorm:"default:0"`
	ReviewedAt   int64  `json:"reviewed_at" gorm:"bigint;default:0"`
	Remark       string `json:"remark" gorm:"type:varchar(255);default:''"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;index"`

	// 展示字段（非表字段）
	WorkTitle string      `json:"work_title" gorm:"-"`
	WorkCover string      `json:"work_cover" gorm:"-"`
	Nickname  string      `json:"nickname" gorm:"-"`
	User      *WorkAuthor `json:"user,omitempty" gorm:"-"`
	Work      *UgcWork    `json:"work,omitempty" gorm:"-"`
}

type UgcWork struct {
	Id       int    `json:"id"`
	Title    string `json:"title"`
	CoverUrl string `json:"cover_url"`
}

func (UgcSubmission) TableName() string { return "ugc_submission" }

const (
	UgcStatusPending  = 0
	UgcStatusApproved = 1
	UgcStatusRejected = 2
)

var UgcPlatforms = map[string]bool{"douyin": true, "xiaohongshu": true, "bilibili": true, "other": true}

func (u *UgcSubmission) Insert() error {
	u.CreatedAt = common.GetTimestamp()
	return DB.Create(u).Error
}

type UgcFilter struct {
	UserId   int
	Status   *int
	Platform string
	Keyword  string
}

func GetUgcSubmissions(f UgcFilter, pageInfo *common.PageInfo) (list []*UgcSubmission, total int64, err error) {
	q := DB.Model(&UgcSubmission{})
	if f.UserId > 0 {
		q = q.Where("user_id = ?", f.UserId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.Platform != "" {
		q = q.Where("platform = ?", f.Platform)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(url LIKE ? OR remark LIKE ?)", like, like)
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = q.Order("status asc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	AttachUgcRefs(list)
	return list, total, err
}

// AttachUgcRefs 填充作品标题 / 封面与用户昵称。
func AttachUgcRefs(list []*UgcSubmission) {
	userIds, workIds := []int{}, []int{}
	for _, u := range list {
		userIds = append(userIds, u.UserId)
		if u.WorkId > 0 {
			workIds = append(workIds, u.WorkId)
		}
	}
	names := GetUserNicknames(userIds)
	works := map[int]*Work{}
	if len(workIds) > 0 {
		var ws []*Work
		DB.Select("id, title, cover_url").Where("id IN ?", workIds).Find(&ws)
		for _, w := range ws {
			works[w.Id] = w
		}
	}
	for _, u := range list {
		u.Nickname = names[u.UserId]
		u.User = &WorkAuthor{Id: u.UserId, Nickname: u.Nickname}
		if w := works[u.WorkId]; w != nil {
			u.WorkTitle, u.WorkCover = w.Title, w.CoverUrl
			u.Work = &UgcWork{Id: w.Id, Title: w.Title, CoverUrl: w.CoverUrl}
		}
	}
}

func GetUgcById(id int) (*UgcSubmission, error) {
	u := &UgcSubmission{}
	if err := DB.Where("id = ?", id).First(u).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return u, nil
}

// UgcMonthStats 本月已获积分与已提交条数。
func UgcMonthStats(userId int, monthStart int64) (points int, entries int64) {
	var v struct{ Sum int }
	DB.Model(&UgcSubmission{}).Select("COALESCE(SUM(points),0) AS sum").
		Where("user_id = ? AND status = ? AND reviewed_at >= ?", userId, UgcStatusApproved, monthStart).Scan(&v)
	DB.Model(&UgcSubmission{}).Where("user_id = ? AND created_at >= ?", userId, monthStart).Count(&entries)
	return v.Sum, entries
}

func UgcUrlExists(url string) bool {
	var n int64
	DB.Model(&UgcSubmission{}).Where("url = ?", url).Count(&n)
	return n > 0
}
