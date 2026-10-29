package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 站外发布返积分（T31）：用户提交平台链接，后台审核按时长档位发积分，破赞翻倍，月封顶。

func monthStart() int64 {
	now := time.Now().In(model.CheckinLocation)
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, model.CheckinLocation).Unix()
}

// SubmitUgc 提交投稿。
func SubmitUgc(userId int, platform, url string, workId int) (*model.UgcSubmission, error) {
	cfg := setting.GetGrowthConfig()
	platform = strings.ToLower(strings.TrimSpace(platform))
	if !model.UgcPlatforms[platform] {
		return nil, errors.New("平台不支持")
	}
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") || len(url) > 500 {
		return nil, errors.New("请填写完整的作品链接")
	}
	if model.UgcUrlExists(url) {
		return nil, errors.New("这个链接已经提交过了")
	}
	if workId > 0 {
		if _, err := model.GetUserWork(userId, workId); err != nil {
			return nil, errors.New("关联的作品不存在")
		}
	}
	_, entries := model.UgcMonthStats(userId, monthStart())
	if cfg.UgcMonthlyEntries > 0 && entries >= int64(cfg.UgcMonthlyEntries) {
		return nil, fmt.Errorf("本月最多提交 %d 条", cfg.UgcMonthlyEntries)
	}
	u := &model.UgcSubmission{UserId: userId, WorkId: workId, Platform: platform, Url: url, Status: model.UgcStatusPending}
	if err := u.Insert(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return nil, errors.New("这个链接已经提交过了")
		}
		return nil, err
	}
	Track(userId, "ugc_submit", map[string]any{"ugc_id": u.Id, "platform": platform, "work_id": workId})
	model.AttachUgcRefs([]*model.UgcSubmission{u})
	return u, nil
}

// UgcReview 审核参数。
type UgcReview struct {
	Action       string `json:"action"`
	DurationTier int    `json:"duration_tier"`
	Likes        int    `json:"likes"`
	Remark       string `json:"remark"`
	ToBanner     bool   `json:"to_banner"`
}

// ReviewUgc 通过 / 驳回；通过时按档位发积分（翻倍、月封顶），可选加入首页轮播，并发站内通知。
func ReviewUgc(id, adminId int, r UgcReview) (*model.UgcSubmission, error) {
	u, err := model.GetUgcById(id)
	if err != nil {
		return nil, err
	}
	if u.Status != model.UgcStatusPending {
		return nil, errors.New("该投稿已审核过")
	}
	now := common.GetTimestamp()
	fields := map[string]any{"reviewed_by": adminId, "reviewed_at": now, "remark": truncate(strings.TrimSpace(r.Remark), 255)}
	switch r.Action {
	case "reject":
		fields["status"] = model.UgcStatusRejected
		if err := model.DB.Model(&model.UgcSubmission{}).Where("id = ?", id).Updates(fields).Error; err != nil {
			return nil, err
		}
		model.PushNotification(u.UserId, model.NotificationTypeSystem, "站外投稿未通过", "你的投稿未通过审核："+truncate(r.Remark, 150), "/settings?tab=partner")
	case "approve":
		cfg := setting.GetGrowthConfig()
		switch r.DurationTier {
		case 30, 60, 120:
		default:
			return nil, errors.New("请选择时长档位")
		}
		points := cfg.UgcPointsForTier(r.DurationTier)
		viral := cfg.UgcViralLikes > 0 && r.Likes >= cfg.UgcViralLikes
		if viral {
			points *= 2
		}
		monthPoints, _ := model.UgcMonthStats(u.UserId, monthStart())
		if cfg.UgcMonthlyCap > 0 && monthPoints+points > cfg.UgcMonthlyCap {
			points = cfg.UgcMonthlyCap - monthPoints
			if points < 0 {
				points = 0
			}
		}
		fields["status"] = model.UgcStatusApproved
		fields["duration_tier"], fields["likes"], fields["viral"], fields["points"] = r.DurationTier, r.Likes, viral, points
		if err := model.DB.Model(&model.UgcSubmission{}).Where("id = ?", id).Updates(fields).Error; err != nil {
			return nil, err
		}
		if points > 0 {
			if err := GiftPointsWithBiz(u.UserId, points, cfg.UgcValidDays, setting.GrowthReasonUgc, fmt.Sprintf("站外发布返积分 +%d", points)); err != nil {
				common.SysError(fmt.Sprintf("ugc reward failed id=%d: %v", id, err))
			}
		}
		msg := fmt.Sprintf("你的站外投稿已通过，获得 %d 积分", points)
		if points == 0 {
			msg = "你的站外投稿已通过，本月返积分已达上限"
		}
		model.PushNotification(u.UserId, model.NotificationTypeSystem, "站外投稿已通过", msg, "/settings?tab=partner")
		if r.ToBanner && u.WorkId > 0 {
			if w, err := model.GetWorkById(u.WorkId); err == nil {
				b := &model.Banner{Title: w.Title, Subtitle: "来自创作伙伴计划", Tag: "创作伙伴", CoverUrl: w.CoverUrl, VideoUrl: w.VideoUrl,
					LinkUrl: fmt.Sprintf("/watch/%d", w.Id), Cta: "立即观看", Status: model.ContentStatusOn, SortOrder: 0}
				if err := b.Insert(); err != nil {
					common.SysError(fmt.Sprintf("ugc to banner failed id=%d: %v", id, err))
				}
			}
		}
		Track(u.UserId, "ugc_approved", map[string]any{"ugc_id": id, "points": points, "viral": viral})
	default:
		return nil, errors.New("无效的操作")
	}
	return model.GetUgcById(id)
}

// UgcMonthView GET /user/ugc 顶部的本月统计。
func UgcMonthView(userId int) map[string]any {
	cfg := setting.GetGrowthConfig()
	points, entries := model.UgcMonthStats(userId, monthStart())
	return map[string]any{
		"month_points": points, "month_cap": cfg.UgcMonthlyCap,
		"month_entries": entries, "month_entries_cap": cfg.UgcMonthlyEntries,
	}
}
