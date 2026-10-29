package controller

import (
	"fmt"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 关注与榜单（API.md 9.2，M11）=====

// ToggleFollowAuthor POST /user/author/:id/follow
func ToggleFollowAuthor(c *gin.Context) {
	userId := c.GetInt("id")
	authorId := paramInt(c, "id")
	if authorId == 0 {
		common.ApiErrorMsg(c, "无效的作者 ID")
		return
	}
	if authorId == userId {
		common.ApiErrorMsg(c, "不能关注自己")
		return
	}
	if _, err := model.GetUserById(authorId); err != nil {
		common.ApiErrorMsg(c, "作者不存在")
		return
	}
	following, followers, err := model.ToggleFollow(userId, authorId)
	if err != nil {
		respondModelError(c, err, "操作失败")
		return
	}
	if following {
		service.Track(userId, "follow_author", map[string]any{"author_id": authorId})
	}
	common.ApiSuccess(c, gin.H{"following": following, "followers": followers})
}

// GetMyFollowingFeed GET /user/following
func GetMyFollowingFeed(c *gin.Context) {
	list, total, err := model.GetFollowingFeed(c.GetInt("id"), parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// GetMyFollowedAuthors GET /user/follow/authors
func GetMyFollowedAuthors(c *gin.Context) {
	list, total, err := model.GetFollowedAuthors(c.GetInt("id"), parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	items := make([]gin.H, 0, len(list))
	for _, a := range list {
		items = append(items, gin.H{"id": a.Id, "nickname": a.Nickname, "works": a.Works, "followers": a.Followers, "is_following": true, "created_at": a.CreatedAt})
	}
	pagedResponse(c, items, total)
}

func rankPeriod(c *gin.Context) string {
	if c.Query("period") == "month" {
		return "month"
	}
	return "week"
}

// GetWorkRank GET /rank/works?period=
func GetWorkRank(c *gin.Context) {
	period := rankPeriod(c)
	rows := model.GetWorkRank(period, 20)
	model.AttachLiked(c.GetInt("id"), worksOfRank(rows))
	common.ApiSuccess(c, gin.H{"period": period, "items": rows})
}

func worksOfRank(rows []model.WorkRankRow) []*model.Work {
	out := make([]*model.Work, 0, len(rows))
	for _, r := range rows {
		if r.Work != nil {
			out = append(out, r.Work)
		}
	}
	return out
}

// GetAuthorRank GET /rank/authors?period=
func GetAuthorRank(c *gin.Context) {
	period := rankPeriod(c)
	rows := model.GetAuthorRank(period, 20)
	userId := c.GetInt("id")
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{
			"user_id": r.UserId, "nickname": r.Nickname, "works": r.Works, "likes": r.Likes,
			"followers": r.Followers, "is_following": model.IsFollowing(userId, r.UserId),
		})
	}
	common.ApiSuccess(c, gin.H{"period": period, "items": items})
}

// GetSkillRank GET /rank/skills?period=
func GetSkillRank(c *gin.Context) {
	period := rankPeriod(c)
	common.ApiSuccess(c, gin.H{"period": period, "items": model.GetSkillRank(period, 20)})
}

// NotifyFollowersNewWork 作者发布新作品后通知粉丝（异步，最多 500 人）。
func NotifyFollowersNewWork(w *model.Work, authorName string) {
	if w == nil || w.Status != model.WorkStatusPublished {
		return
	}
	ids := model.GetFollowerIds(w.UserId, 500)
	if len(ids) == 0 {
		return
	}
	link := fmt.Sprintf("/watch/%d", w.Id)
	model.PushNotificationToUsers(ids, model.NotificationTypeWork, authorName+" 发布了新作品", w.Title, link)
}
