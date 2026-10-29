package service

import (
	"fmt"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 分享回流奖励（T30）：播放页带 ref 打开时前端上报 share_open，作者按次得积分，日封顶。

// RewardShareOpen 在事件落表前调用（用已存事件做当日去重）。
func RewardShareOpen(workId int, ref, anonId string, viewerId int) {
	cfg := setting.GetGrowthConfig()
	if cfg.ShareOpenPoints <= 0 || workId <= 0 || ref == "" || anonId == "" {
		return
	}
	authorId := model.GetUserIdByAffCode(ref)
	if authorId <= 0 || authorId == viewerId {
		return
	}
	w, err := model.GetWorkById(workId)
	if err != nil || w.UserId != authorId || w.Status != model.WorkStatusPublished {
		return
	}
	dayStart := time.Now().In(model.CheckinLocation)
	dayStart = time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, model.CheckinLocation)
	since := dayStart.Unix()
	// 同一访客当日对同一作者只算一次
	var seen int64
	model.DB.Model(&model.TrackEvent{}).
		Where("name = ? AND anon_id = ? AND created_at >= ? AND props LIKE ?", "share_open", anonId, since, fmt.Sprintf(`%%"ref":"%s"%%`, ref)).
		Count(&seen)
	if seen > 0 {
		return
	}
	var today struct{ Sum int }
	model.DB.Model(&model.PointLog{}).Select("COALESCE(SUM(amount),0) AS sum").
		Where("user_id = ? AND biz_no = ? AND created_at >= ?", authorId, setting.GrowthReasonShareOpen, since).Scan(&today)
	if today.Sum >= cfg.ShareDailyCap {
		return
	}
	points := cfg.ShareOpenPoints
	if today.Sum+points > cfg.ShareDailyCap {
		points = cfg.ShareDailyCap - today.Sum
	}
	if err := GiftPointsWithBiz(authorId, points, cfg.ShareValidDays, setting.GrowthReasonShareOpen, fmt.Sprintf("作品《%s》被分享打开 +%d 积分", truncate(w.Title, 20), points)); err != nil {
		common.SysError(fmt.Sprintf("share open reward failed author=%d: %v", authorId, err))
	}
}
