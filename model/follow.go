package model

import (
	"easytv-api/common"

	"gorm.io/gorm"
)

// UserFollow 关注关系：user_id 关注 author_id。
type UserFollow struct {
	Id        int   `json:"id"`
	UserId    int   `json:"user_id" gorm:"uniqueIndex:idx_follow,priority:1"`
	AuthorId  int   `json:"author_id" gorm:"uniqueIndex:idx_follow,priority:2;index"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
}

func (UserFollow) TableName() string { return "user_follow" }

// WorkFollow 从某部已发布作品的播放页点下的关注。只追加，取关不删。
// 同一人在 30 天内对同一部作品反复关注，列表里只算一个人。
type WorkFollow struct {
	Id        int   `json:"id"`
	UserId    int   `json:"user_id" gorm:"index;comment:关注的人"`
	AuthorId  int   `json:"author_id" gorm:"comment:被关注的作者"`
	WorkId    int   `json:"work_id" gorm:"index:idx_work_follow_work,priority:1;comment:从哪部已发布作品点的关注"`
	CreatedAt int64 `json:"created_at" gorm:"bigint;index:idx_work_follow_work,priority:2;comment:关注时间"`
}

func (WorkFollow) TableName() string { return "work_follow" }

// RecordWorkFollow 播放页关注成功后记一笔。作品不是这位作者的已发布作品时不记，也不影响关注本身。
func RecordWorkFollow(userId, authorId, workId int) error {
	if userId <= 0 || authorId <= 0 || workId <= 0 || userId == authorId {
		return nil
	}
	var n int64
	if err := DB.Model(&Work{}).Where("id = ? AND user_id = ? AND status = ?", workId, authorId, WorkStatusPublished).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	return DB.Create(&WorkFollow{
		UserId:    userId,
		AuthorId:  authorId,
		WorkId:    workId,
		CreatedAt: common.GetTimestamp(),
	}).Error
}

// CountWorkFollowsSince 每部作品在 since 之后有多少不同的人从播放页关注过。
func CountWorkFollowsSince(workIds []int, since int64) (map[int]int, error) {
	out := map[int]int{}
	if len(workIds) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkId int
		N      int
	}
	err := DB.Model(&WorkFollow{}).
		Select("work_id, COUNT(DISTINCT user_id) AS n").
		Where("work_id IN ? AND created_at >= ?", workIds, since).
		Group("work_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.WorkId] = row.N
	}
	return out, nil
}

// ToggleFollow 关注 / 取关，返回关注后状态与作者粉丝数。
func ToggleFollow(userId, authorId int) (following bool, followers int64, err error) {
	if userId <= 0 || authorId <= 0 || userId == authorId {
		return false, 0, ErrNotFound
	}
	var row UserFollow
	tx := DB.Where("user_id = ? AND author_id = ?", userId, authorId).First(&row)
	if tx.Error == nil {
		if err = DB.Where("id = ?", row.Id).Delete(&UserFollow{}).Error; err != nil {
			return false, 0, err
		}
		following = false
	} else {
		if err = DB.Create(&UserFollow{UserId: userId, AuthorId: authorId, CreatedAt: common.GetTimestamp()}).Error; err != nil {
			return false, 0, err
		}
		following = true
	}
	followers = CountFollowers(authorId)
	return following, followers, nil
}

func CountFollowers(authorId int) int64 {
	var n int64
	DB.Model(&UserFollow{}).Where("author_id = ?", authorId).Count(&n)
	return n
}

func CountFollowing(userId int) int64 {
	var n int64
	DB.Model(&UserFollow{}).Where("user_id = ?", userId).Count(&n)
	return n
}

func IsFollowing(userId, authorId int) bool {
	if userId <= 0 || authorId <= 0 {
		return false
	}
	var n int64
	DB.Model(&UserFollow{}).Where("user_id = ? AND author_id = ?", userId, authorId).Count(&n)
	return n > 0
}

// GetFollowerIds 作者的粉丝 ID（发新作品通知用，限量）。
func GetFollowerIds(authorId, limit int) []int {
	var ids []int
	DB.Model(&UserFollow{}).Select("user_id").Where("author_id = ?", authorId).Order("id desc").Limit(limit).Scan(&ids)
	return ids
}

// FollowedAuthor 我关注的作者列表一行。
type FollowedAuthor struct {
	Id        int    `json:"id"`
	Nickname  string `json:"nickname"`
	Works     int64  `json:"works"`
	Followers int64  `json:"followers"`
	CreatedAt int64  `json:"created_at"`
}

func GetFollowedAuthors(userId int, pageInfo *common.PageInfo) ([]FollowedAuthor, int64, error) {
	var total int64
	DB.Model(&UserFollow{}).Where("user_id = ?", userId).Count(&total)
	var rows []UserFollow
	if err := DB.Where("user_id = ?", userId).Order("id desc").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.AuthorId)
	}
	names := GetUserNicknames(ids)
	out := make([]FollowedAuthor, 0, len(rows))
	for _, r := range rows {
		var works int64
		DB.Model(&Work{}).Where("user_id = ? AND status = ?", r.AuthorId, WorkStatusPublished).Count(&works)
		out = append(out, FollowedAuthor{Id: r.AuthorId, Nickname: names[r.AuthorId], Works: works, Followers: CountFollowers(r.AuthorId), CreatedAt: r.CreatedAt})
	}
	return out, total, nil
}

// GetFollowingFeed 我关注的作者的已发布作品流。
func GetFollowingFeed(userId int, pageInfo *common.PageInfo) ([]*Work, int64, error) {
	sub := DB.Model(&UserFollow{}).Select("author_id").Where("user_id = ?", userId)
	q := DB.Model(&Work{}).Where("status = ? AND user_id IN (?)", WorkStatusPublished, sub)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*Work
	if err := q.Order("published_at desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	attachWorkAuthors(list)
	AttachLiked(userId, list)
	return list, total, nil
}

var _ = gorm.ErrRecordNotFound
