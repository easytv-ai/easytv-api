package service

import (
	"encoding/json"
	"fmt"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// RewardWorkClone 开源作品被他人做同款后给作者发积分（日封顶；同一访客同一作品当日只奖一次）。
// 在 work_clone 事件落表前调用，用已存事件去重。失败只记日志，不影响复制。
func RewardWorkClone(authorId, clonerId, workId int, title string) {
	if authorId <= 0 || clonerId <= 0 || workId <= 0 || authorId == clonerId {
		return
	}
	cfg := setting.GetGrowthConfig()
	if cfg.WorkClonePoints <= 0 {
		return
	}
	dayStart := time.Now().In(model.CheckinLocation)
	dayStart = time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, model.CheckinLocation)
	since := dayStart.Unix()
	var props []string
	model.DB.Model(&model.TrackEvent{}).
		Where("name = ? AND user_id = ? AND created_at >= ?", model.TrackEventWorkClone, clonerId, since).
		Limit(cloneDedupScan).Pluck("props", &props)
	if cloneSeenWork(props, workId) {
		return
	}
	var today struct{ Sum int }
	model.DB.Model(&model.PointLog{}).Select("COALESCE(SUM(amount),0) AS sum").
		Where("user_id = ? AND biz_no = ? AND created_at >= ?", authorId, setting.GrowthReasonWorkClone, since).Scan(&today)
	points := cloneRewardPoints(cfg.WorkClonePoints, today.Sum, cfg.WorkCloneDailyCap)
	if points <= 0 {
		return
	}
	label := truncate(title, 20)
	if label == "" {
		label = fmt.Sprintf("#%d", workId)
	}
	if err := GiftPointsWithBiz(authorId, points, cfg.WorkCloneValidDays, setting.GrowthReasonWorkClone,
		fmt.Sprintf("作品《%s》被做同款 +%d 积分", label, points)); err != nil {
		common.SysError(fmt.Sprintf("work clone reward failed author=%d work=%d: %v", authorId, workId, err))
		return
	}
	model.PushNotification(authorId, model.NotificationTypeWork,
		"作品被做同款",
		fmt.Sprintf("「%s」被做同款，你获得 %d 积分", label, points),
		fmt.Sprintf("/watch/%d", workId))
}

// cloneDedupScan 去重时最多看访客当天多少条做同款事件；超过这个量的访客不再给作者发奖。
const cloneDedupScan = 500

// cloneSeenWork 访客当天的做同款事件里有没有这件作品。
// 事件属性是 JSON 文本，键顺序不固定，解出来比对，不拿字符串去匹配；条数到顶按已做过算。
func cloneSeenWork(props []string, workId int) bool {
	if len(props) >= cloneDedupScan {
		return true
	}
	for _, raw := range props {
		var p struct {
			WorkId json.Number `json:"work_id"`
		}
		if json.Unmarshal([]byte(raw), &p) != nil {
			continue
		}
		if id, err := p.WorkId.Int64(); err == nil && int(id) == workId {
			return true
		}
	}
	return false
}

// cloneRewardPoints 本次实发积分：未达日上限发 per，超出则只补差额，已满或关闭为 0。
func cloneRewardPoints(per, already, cap int) int {
	if per <= 0 {
		return 0
	}
	if cap <= 0 {
		return per
	}
	if already >= cap {
		return 0
	}
	if already+per > cap {
		return cap - already
	}
	return per
}
