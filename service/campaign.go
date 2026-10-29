package service

import (
	"errors"
	"fmt"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 活动（T33）：投稿、审核、结算发奖。

// EnterCampaign 用已发布的作品投稿。
func EnterCampaign(userId, workId, campaignId int) (*model.CampaignEntry, error) {
	c, err := model.GetCampaignById(campaignId)
	if err != nil {
		return nil, errors.New("活动不存在")
	}
	now := common.GetTimestamp()
	if c.EffectiveStatus(now) != model.CampaignStatusActive || (c.StartsAt > 0 && now < c.StartsAt) {
		return nil, errors.New("活动不在投稿期")
	}
	w, err := model.GetUserWork(userId, workId)
	if err != nil {
		return nil, errors.New("作品不存在")
	}
	if w.Status != model.WorkStatusPublished {
		return nil, errors.New("只有已发布的作品可以投稿")
	}
	e := &model.CampaignEntry{CampaignId: campaignId, WorkId: workId, UserId: userId, Status: model.CampaignEntryPending, LikesSnapshot: w.LikeCount}
	if err := e.Insert(); err != nil {
		return nil, errors.New("这个作品已经投过这个活动")
	}
	Track(userId, "campaign_enter", map[string]any{"campaign_id": campaignId, "work_id": workId})
	return e, nil
}

// SettleCampaign 结束后按点赞快照排名发奖，幂等（status → 3）。
func SettleCampaign(id int) (int, error) {
	c, err := model.GetCampaignById(id)
	if err != nil {
		return 0, err
	}
	now := common.GetTimestamp()
	if c.Status == model.CampaignStatusSettled {
		return 0, errors.New("活动已发过奖")
	}
	if c.EffectiveStatus(now) != model.CampaignStatusEnded {
		return 0, errors.New("活动还没结束")
	}
	model.RefreshCampaignLikes(id)
	// 一次最多排 100 名（PageInfo 上限）；奖项都在前几名，名次之外的 rank 保持 0
	approved := model.CampaignEntryApproved
	entries, _, err := model.GetCampaignEntries(id, &approved, "likes", &common.PageInfo{Page: 1, PageSize: 100})
	if err != nil {
		return 0, err
	}
	prizes := c.ParsePrizes()
	prizeFor := func(rank int) (int, int) {
		for _, p := range prizes {
			if rank >= p.RankFrom && rank <= p.RankTo {
				return p.Points, p.MemberDays
			}
		}
		return 0, 0
	}
	awarded := 0
	for i, e := range entries {
		rank := i + 1
		points, days := prizeFor(rank)
		fields := map[string]any{"rank": rank, "prize_points": points, "prize_days": days}
		if err := model.UpdateCampaignEntryFields(e.Id, fields); err != nil {
			continue
		}
		if points > 0 {
			_ = GiftPointsWithBiz(e.UserId, points, 90, setting.GrowthReasonCampaign, fmt.Sprintf("活动「%s」第 %d 名奖励", truncate(c.Title, 30), rank))
		}
		if days > 0 {
			_ = model.DB.Transaction(func(tx *gorm.DB) error { return grantMemberDays(tx, e.UserId, days, "") })
		}
		if points > 0 || days > 0 {
			awarded++
			model.PushNotification(e.UserId, model.NotificationTypeSystem, "活动获奖", fmt.Sprintf("你在「%s」中获得第 %d 名，奖励已发放", c.Title, rank), "/watch/"+itoa(e.WorkId))
		}
	}
	_ = model.UpdateCampaignFields(id, map[string]any{"status": model.CampaignStatusSettled})
	model.ResetCampaignPromoCache()
	return awarded, nil
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
