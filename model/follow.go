package model

import (
	"easytv-api/common"

	"gorm.io/gorm"
)

// UserFollow 关注关系（M11）：user_id 关注 author_id。
type UserFollow struct {
	Id        int   `json:"id"`
	UserId    int   `json:"user_id" gorm:"uniqueIndex:idx_follow,priority:1"`
	AuthorId  int   `json:"author_id" gorm:"uniqueIndex:idx_follow,priority:2;index"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
}

func (UserFollow) TableName() string { return "user_follow" }

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
