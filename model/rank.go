package model

import (
	"time"

	"easytv-api/common"
)

// 榜单（M11）：作品榜按窗口内点赞数，作者榜按窗口内被赞数，技能榜按窗口内调用数。

// RankPeriodSince 周榜 / 月榜的起点。
func RankPeriodSince(period string) int64 {
	days := 7
	if period == "month" {
		days = 30
	}
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
}

// WorkRankRow 作品榜一行。
type WorkRankRow struct {
	Work          *Work `json:"work"`
	LikesInPeriod int64 `json:"likes_in_period"`
	Views         int   `json:"views"`
}

// GetWorkRank 窗口内点赞数前 limit 的已发布作品；不足时用累计点赞补齐。
func GetWorkRank(period string, limit int) []WorkRankRow {
	since := RankPeriodSince(period)
	type row struct {
		WorkId int
		Likes  int64
	}
	var rows []row
	DB.Table("work_like").Select("work_id, COUNT(*) AS likes").
		Where("created_at >= ?", since).Group("work_id").Order("likes desc").Limit(limit).Scan(&rows)
	ids := make([]int, 0, len(rows))
	likes := map[int]int64{}
	for _, r := range rows {
		ids = append(ids, r.WorkId)
		likes[r.WorkId] = r.Likes
	}
	out := []WorkRankRow{}
	if len(ids) > 0 {
		var works []*Work
		DB.Where("id IN ? AND status = ?", ids, WorkStatusPublished).Find(&works)
		attachWorkAuthors(works)
		byId := map[int]*Work{}
		for _, w := range works {
			byId[w.Id] = w
		}
		for _, r := range rows {
			if w := byId[r.WorkId]; w != nil {
				out = append(out, WorkRankRow{Work: w, LikesInPeriod: r.Likes, Views: w.ViewCount})
			}
		}
	}
	if len(out) >= limit {
		return out
	}
	// 窗口内点赞不足：用累计点赞高的已发布作品补齐
	seen := map[int]bool{}
	for _, r := range out {
		seen[r.Work.Id] = true
	}
	var extra []*Work
	DB.Where("status = ?", WorkStatusPublished).Order("like_count desc, view_count desc, id desc").Limit(limit * 2).Find(&extra)
	attachWorkAuthors(extra)
	for _, w := range extra {
		if len(out) >= limit {
			break
		}
		if seen[w.Id] {
			continue
		}
		out = append(out, WorkRankRow{Work: w, LikesInPeriod: likes[w.Id], Views: w.ViewCount})
	}
	return out
}

// AuthorRankRow 作者榜一行。
type AuthorRankRow struct {
	UserId    int    `json:"user_id"`
	Nickname  string `json:"nickname"`
	Works     int64  `json:"works"`
	Likes     int64  `json:"likes"`
	Followers int64  `json:"followers"`
}

// GetAuthorRank 窗口内作品被赞数前 limit 的作者。
func GetAuthorRank(period string, limit int) []AuthorRankRow {
	since := RankPeriodSince(period)
	type row struct {
		UserId int
		Likes  int64
	}
	var rows []row
	DB.Table("work_like").Select("work.user_id AS user_id, COUNT(*) AS likes").
		Joins("JOIN work ON work.id = work_like.work_id").
		Where("work_like.created_at >= ? AND work.status = ?", since, WorkStatusPublished).
		Group("work.user_id").Order("likes desc").Limit(limit).Scan(&rows)
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserId)
	}
	names := GetUserNicknames(ids)
	out := make([]AuthorRankRow, 0, len(rows))
	for _, r := range rows {
		var works int64
		DB.Model(&Work{}).Where("user_id = ? AND status = ?", r.UserId, WorkStatusPublished).Count(&works)
		out = append(out, AuthorRankRow{UserId: r.UserId, Nickname: names[r.UserId], Works: works, Likes: r.Likes, Followers: CountFollowers(r.UserId)})
	}
	return out
}

// SkillRankRow 技能榜一行。
type SkillRankRow struct {
	Skill *Skill `json:"skill"`
	Calls int64  `json:"calls"`
	Stars int    `json:"stars"`
}

// GetSkillRank 窗口内被调用（分成流水）次数前 limit 的已上架技能；不足用 uses 补齐。
func GetSkillRank(period string, limit int) []SkillRankRow {
	since := RankPeriodSince(period)
	type row struct {
		SkillId int
		Calls   int64
	}
	var rows []row
	DB.Table("promoter_bill").Select("generation_task.skill_id AS skill_id, COUNT(*) AS calls").
		Joins("JOIN generation_task ON generation_task.id = promoter_bill.ref_id").
		Where("promoter_bill.biz_type = ? AND promoter_bill.ref_type = ? AND promoter_bill.created_at >= ? AND generation_task.skill_id > 0",
			PromoterBillTypeSkillRevenue, PromoterBillRefTypeTask, since).
		Group("generation_task.skill_id").Order("calls desc").Limit(limit).Scan(&rows)
	calls := map[int]int64{}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		calls[r.SkillId] = r.Calls
		ids = append(ids, r.SkillId)
	}
	out := []SkillRankRow{}
	seen := map[int]bool{}
	if len(ids) > 0 {
		var skills []*Skill
		DB.Where("id IN ? AND status = ?", ids, SkillStatusOn).Find(&skills)
		byId := map[int]*Skill{}
		for _, s := range skills {
			byId[s.Id] = s
		}
		for _, r := range rows {
			if s := byId[r.SkillId]; s != nil && s.PublicVisible() {
				out = append(out, SkillRankRow{Skill: s, Calls: r.Calls, Stars: s.Stars})
				seen[s.Id] = true
			}
		}
	}
	if len(out) < limit {
		var extra []*Skill
		DB.Where("status = ? AND (user_id = 0 OR review_status = ?)", SkillStatusOn, SkillReviewPublished).
			Order("uses desc, stars desc, id desc").Limit(limit * 2).Find(&extra)
		for _, s := range extra {
			if len(out) >= limit {
				break
			}
			if seen[s.Id] {
				continue
			}
			out = append(out, SkillRankRow{Skill: s, Calls: calls[s.Id], Stars: s.Stars})
		}
	}
	return out
}

var _ = common.GetTimestamp
