package model

import (
	"encoding/json"
	"strings"
	"time"

	"easytv-api/common"
)

// TrackEvent 增长事件（T1）：前端 POST /track 与服务端业务流程共同写入，漏斗与渠道归因从这张表出。
// 只增不改；props 为小 JSON（≤ 2KB），大对象不要往里塞。
type TrackEvent struct {
	Id        int64  `json:"id"`
	UserId    int    `json:"user_id" gorm:"index;default:0;comment:用户 ID，匿名为 0"`
	AnonId    string `json:"anon_id" gorm:"type:varchar(32);index;default:'';comment:前端匿名 ID，注册后用于关联注册前事件"`
	Name      string `json:"name" gorm:"type:varchar(40);index:idx_track_name_time,priority:1;comment:事件名"`
	Props     string `json:"props" gorm:"type:varchar(2048);default:'';comment:事件属性 JSON"`
	Source    string `json:"source" gorm:"type:varchar(40);index;default:'';comment:utm_source"`
	Medium    string `json:"medium" gorm:"type:varchar(40);default:'';comment:utm_medium"`
	Campaign  string `json:"campaign" gorm:"type:varchar(64);default:'';comment:utm_campaign"`
	AffCode   string `json:"aff_code" gorm:"type:varchar(32);index;default:'';comment:邀请码"`
	BonusCode string `json:"bonus_code" gorm:"type:varchar(32);index;default:'';comment:口令"`
	Ip        string `json:"ip" gorm:"type:varchar(45);default:''"`
	Device    string `json:"device" gorm:"type:varchar(64);default:'';comment:前端设备指纹（EasyTV-Device 头）"`
	CreatedAt int64  `json:"created_at" gorm:"index:idx_track_name_time,priority:2;bigint"`
}

func (TrackEvent) TableName() string { return "track_event" }

// 服务端事件名（与 API.md 7.4 对齐）。前端事件由前端自定义，长度限制在 40。
const (
	TrackEventRegister    = "register"
	TrackEventFirstCanvas = "first_canvas"
	TrackEventFirstTask   = "first_task"
	TrackEventFirstVideo  = "first_video"
	TrackEventFirstWork   = "first_work"
	TrackEventBindWeChat  = "bind_wechat"
	TrackEventPaySuccess  = "pay_success"
	TrackEventRefund      = "refund"
	TrackEventWorkClone   = "work_clone"
	TrackEventBonusClaim  = "bonus_claim"
	// TrackEventRefMissing 引用丢失：生成 / 合成时有参考素材、主体图或镜头视频找不到（API.md 17），
	// props.source = canvas_unlinked / input / subject / compose_clip，props.count = 丢失个数
	TrackEventRefMissing = "ref_missing"
)

// Attribution 归因信息：注册请求里的 attr，或前端上报事件时随事件携带。
type Attribution struct {
	Source    string `json:"source"`
	Medium    string `json:"medium"`
	Campaign  string `json:"campaign"`
	AffCode   string `json:"ref"`
	BonusCode string `json:"code"`
	// PartnerCode 渠道伙伴专属码（?pcode=，M12）
	PartnerCode string `json:"pcode"`
	AnonId      string `json:"anon_id"`
}

// Normalize 截断并清理，防止把整段 URL 塞进来。
func (a Attribution) Normalize() Attribution {
	return Attribution{
		Source:      trackClip(a.Source, 40),
		Medium:      trackClip(a.Medium, 40),
		Campaign:    trackClip(a.Campaign, 64),
		AffCode:     strings.ToUpper(trackClip(a.AffCode, 32)),
		BonusCode:   trackClip(a.BonusCode, 32),
		PartnerCode: strings.ToUpper(trackClip(a.PartnerCode, 20)),
		AnonId:      trackClip(a.AnonId, 32),
	}
}

func trackClip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// TrackEventNameValid 事件名只允许小写字母、数字与下划线，1~40 位。
func TrackEventNameValid(name string) bool {
	if name == "" || len(name) > 40 {
		return false
	}
	for _, r := range name {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// InsertTrackEvents 批量写入。
func InsertTrackEvents(list []*TrackEvent) error {
	if len(list) == 0 {
		return nil
	}
	return DB.CreateInBatches(list, 50).Error
}

// PropsJSON 把属性 map 编码为受限长度的 JSON。
func PropsJSON(props map[string]any) string {
	if len(props) == 0 {
		return ""
	}
	b, err := json.Marshal(props)
	if err != nil {
		return ""
	}
	if len(b) > 2048 {
		return ""
	}
	return string(b)
}

// AttachAnonEventsToUser 注册后把该匿名 ID 的历史事件归到用户名下（只补 user_id=0 的行）。
func AttachAnonEventsToUser(anonId string, userId int) {
	if anonId == "" || userId <= 0 {
		return
	}
	DB.Model(&TrackEvent{}).Where("anon_id = ? AND user_id = 0", anonId).Update("user_id", userId)
}

// HasUserEvent 用户是否已产生过某事件（用于 first_* 只记一次）。
func HasUserEvent(userId int, name string) bool {
	if userId <= 0 {
		return false
	}
	var n int64
	DB.Model(&TrackEvent{}).Where("user_id = ? AND name = ?", userId, name).Limit(1).Count(&n)
	return n > 0
}

// FunnelDay 漏斗按天统计行。
type FunnelDay struct {
	Day        string `json:"day"`
	Register   int64  `json:"register"`
	FirstTask  int64  `json:"first_task"`
	FirstVideo int64  `json:"first_video"`
	FirstWork  int64  `json:"first_work"`
	PaySuccess int64  `json:"pay_success"`
}

// FunnelSource 按来源分组行（来源 / 邀请码 / 口令三者任一非空即成一组）。
type FunnelSource struct {
	Source    string  `json:"source"`
	AffCode   string  `json:"aff_code"`
	BonusCode string  `json:"bonus_code"`
	Register  int64   `json:"register"`
	Paid      int64   `json:"paid"`
	Amount    float64 `json:"amount"`
}

// GetFunnel 漏斗统计：from/to 为 Unix 秒（闭区间按天），source 非空时只统计该来源的用户。
// 各步骤按「用户去重」计数，日期取事件发生日（Asia/Shanghai）。
func GetFunnel(from, to int64, source string) (days []FunnelDay, bySource []FunnelSource, err error) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.Local
	}
	names := []string{TrackEventRegister, TrackEventFirstTask, TrackEventFirstVideo, TrackEventFirstWork, TrackEventPaySuccess}

	type row struct {
		Name string
		Day  string
		N    int64
	}
	var rows []row
	q := DB.Model(&TrackEvent{}).
		Select("name, FROM_UNIXTIME(created_at, '%Y-%m-%d') AS day, COUNT(DISTINCT user_id) AS n").
		Where("name IN ? AND created_at BETWEEN ? AND ? AND user_id > 0", names, from, to)
	if source != "" {
		q = q.Where("user_id IN (?)", DB.Model(&User{}).Select("id").Where("source = ?", source))
	}
	if err = q.Group("name, day").Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	byDay := map[string]*FunnelDay{}
	for _, r := range rows {
		d := byDay[r.Day]
		if d == nil {
			d = &FunnelDay{Day: r.Day}
			byDay[r.Day] = d
		}
		switch r.Name {
		case TrackEventRegister:
			d.Register = r.N
		case TrackEventFirstTask:
			d.FirstTask = r.N
		case TrackEventFirstVideo:
			d.FirstVideo = r.N
		case TrackEventFirstWork:
			d.FirstWork = r.N
		case TrackEventPaySuccess:
			d.PaySuccess = r.N
		}
	}
	// 按日期补齐空行，便于前端画图
	for t := time.Unix(from, 0).In(loc); t.Unix() <= to; t = t.AddDate(0, 0, 1) {
		key := t.Format("2006-01-02")
		if _, ok := byDay[key]; !ok {
			byDay[key] = &FunnelDay{Day: key}
		}
	}
	for _, d := range byDay {
		days = append(days, *d)
	}
	sortFunnelDays(days)

	// 来源分组：注册数按 user 表来源列，付费按 pay_success 事件
	type srcRow struct {
		Source    string
		AffCode   string
		BonusCode string
		Register  int64
	}
	var regRows []srcRow
	regQ := DB.Model(&User{}).
		Select("user.source AS source, COALESCE(inviter.aff_code, '') AS aff_code, user.bonus_code AS bonus_code, COUNT(*) AS register").
		Joins("LEFT JOIN user AS inviter ON inviter.id = user.inviter_id").
		Where("user.created_at BETWEEN ? AND ?", from, to)
	if source != "" {
		regQ = regQ.Where("user.source = ?", source)
	}
	if err = regQ.Group("user.source, inviter.aff_code, user.bonus_code").Scan(&regRows).Error; err != nil {
		return days, nil, err
	}
	type payRow struct {
		Source    string
		AffCode   string
		BonusCode string
		Paid      int64
		Amount    float64
	}
	var payRows []payRow
	payQ := DB.Table("track_event").
		Select("track_event.source, track_event.aff_code, track_event.bonus_code, COUNT(DISTINCT track_event.user_id) AS paid, "+
			"COALESCE(SUM(JSON_EXTRACT(track_event.props, '$.amount')), 0) AS amount").
		Where("track_event.name = ? AND track_event.created_at BETWEEN ? AND ?", TrackEventPaySuccess, from, to)
	if source != "" {
		payQ = payQ.Where("track_event.source = ?", source)
	}
	if err = payQ.Group("track_event.source, track_event.aff_code, track_event.bonus_code").Scan(&payRows).Error; err != nil {
		return days, nil, err
	}
	// 无数据时返回空数组而不是 null，前端直接取 length
	bySource = []FunnelSource{}
	merged := map[string]*FunnelSource{}
	keyOf := func(s, a, b string) string { return s + "\x00" + a + "\x00" + b }
	for _, r := range regRows {
		k := keyOf(r.Source, r.AffCode, r.BonusCode)
		merged[k] = &FunnelSource{Source: r.Source, AffCode: r.AffCode, BonusCode: r.BonusCode, Register: r.Register}
	}
	for _, r := range payRows {
		k := keyOf(r.Source, r.AffCode, r.BonusCode)
		m := merged[k]
		if m == nil {
			m = &FunnelSource{Source: r.Source, AffCode: r.AffCode, BonusCode: r.BonusCode}
			merged[k] = m
		}
		m.Paid = r.Paid
		m.Amount = r.Amount
	}
	for _, m := range merged {
		bySource = append(bySource, *m)
	}
	sortFunnelSources(bySource)
	return days, bySource, nil
}

func sortFunnelDays(days []FunnelDay) {
	for i := 1; i < len(days); i++ {
		for j := i; j > 0 && days[j].Day < days[j-1].Day; j-- {
			days[j], days[j-1] = days[j-1], days[j]
		}
	}
}

func sortFunnelSources(list []FunnelSource) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].Register > list[j-1].Register; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

var _ = common.GetTimestamp
