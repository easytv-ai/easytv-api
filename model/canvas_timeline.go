package model

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// CanvasTimeline 画布的成片时间线（API.md 12.2）：镜头顺序、启用、候选、入出点、音量、逐镜字幕与导出设置。
// 单独成表而不是塞进画布 graph：画布页和向导都会整份保存 graph，时间线放进去会被互相覆盖。
type CanvasTimeline struct {
	Id        int    `json:"id"`
	CanvasId  int    `json:"canvas_id" gorm:"uniqueIndex"`
	UserId    int    `json:"user_id" gorm:"index"`
	Data      string `json:"-" gorm:"type:text"`
	Version   int    `json:"version" gorm:"default:1;comment:乐观锁"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
	// 定版锁（API.md 25.6）：锁住后 PUT 被拒，合成照常；解锁需显式调用
	Locked   bool  `json:"locked" gorm:"default:false"`
	LockedAt int64 `json:"locked_at" gorm:"bigint;default:0"`
	LockedBy int   `json:"locked_by" gorm:"default:0"`
}

// ErrTimelineLocked 时间线已定版。
var ErrTimelineLocked = errors.New("时间线已定版，先解锁再修改")

// SetTimelineLock 上锁 / 解锁；没保存过时间线时返回 ErrNotFound（先保存才能定版）。
func SetTimelineLock(canvasId, userId int, locked bool) (*CanvasTimeline, error) {
	row := &CanvasTimeline{}
	if err := DB.Where("canvas_id = ?", canvasId).First(row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	fields := map[string]any{"locked": locked, "locked_at": 0, "locked_by": 0}
	if locked {
		fields["locked_at"], fields["locked_by"] = common.GetTimestamp(), userId
	}
	if err := DB.Model(&CanvasTimeline{}).Where("id = ?", row.Id).Updates(fields).Error; err != nil {
		return nil, err
	}
	row.Locked = locked
	row.LockedAt, _ = fields["locked_at"].(int64)
	row.LockedBy, _ = fields["locked_by"].(int)
	return row, nil
}

func (CanvasTimeline) TableName() string { return "canvas_timeline" }

// Timeline 时间线内容。
type Timeline struct {
	Aspect     string `json:"aspect"`
	Resolution string `json:"resolution"`
	Subtitles  bool   `json:"subtitles"`
	Voice      bool   `json:"voice"`
	Lang       string `json:"lang"`
	BgmAssetId int    `json:"bgm_asset_id"`
	// BgmVolume BGM 音量：JSON 里缺失解码成 TimelineVolumeUnset(-1)，Normalize 回落 0.2；明确传 0 就是静音（复审 D07）
	BgmVolume float64        `json:"bgm_volume"`
	Clips     []TimelineClip `json:"clips"`
	// ReviewGate 审批门禁（API.md 26.1）："" 关闭 / rejected 退回即拦 / approved 分镜图通过才放行视频
	ReviewGate string `json:"review_gate"`
	// SubtitleNodeId 字幕来源（API.md 35.4）：画布上一个装着 SRT 的文本节点（语音转字幕的产物）；空 = 用策划案台词
	SubtitleNodeId string `json:"subtitle_node_id"`
}

// TimelineVolumeUnset 音量「未设置」哨兵：只在 JSON 解码时出现，Normalize 后不会残留。
const TimelineVolumeUnset = -1.0

// UnmarshalJSON 缺失的 bgm_volume 解码为 TimelineVolumeUnset，区分「没传」与「明确为 0」。
func (t *Timeline) UnmarshalJSON(b []byte) error {
	type alias Timeline
	a := alias{BgmVolume: TimelineVolumeUnset}
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*t = Timeline(a)
	return nil
}

// TimelineClip 时间线上的一个剪辑片段。同一个节点（素材）可以按不同入出点出现多次（正反打 / 反应镜头），
// 每条片段有独立 Id（复审 D04），服务端为空时生成。
type TimelineClip struct {
	Id      string `json:"id"`
	NodeId  string `json:"node_id"`
	Enabled bool   `json:"enabled"`
	// AssetId 选用的候选视频；0 表示用画布节点当前的视频
	AssetId int     `json:"asset_id"`
	In      float64 `json:"in"`
	Out     float64 `json:"out"`
	// VoiceMode mix 原声 + 配音；replace 只留配音；off 不要配音
	VoiceMode   string  `json:"voice_mode"`
	OrigVolume  float64 `json:"orig_volume"`
	VoiceVolume float64 `json:"voice_volume"`
	// Subtitle 逐镜字幕；nil 用台词，空串表示这一镜不要字幕
	Subtitle *string `json:"subtitle"`
}

const (
	TimelineMaxClips    = 60
	TimelineVoiceMix    = "mix"
	TimelineVoiceOnly   = "replace"
	TimelineVoiceOff    = "off"
	TimelineRes720      = "720p"
	TimelineRes1080     = "1080p"
	TimelineMaxVolume   = 2.0
	TimelineSubtitleMax = 200
)

var ErrTimelineVersionConflict = errors.New("时间线已在其他地方修改，请刷新后再保存")

// ValidAspect 成片支持的画幅。
func ValidAspect(v string) bool {
	return v == "16:9" || v == "9:16" || v == "1:1"
}

// Normalize 清洗时间线：非法值回落缺省、音量钳到 [0,2]、入出点非负、片段按 id 去重（同一节点可多次出现）、条数上限。
// BgmVolume 未设置（负数）回落 0.2，明确为 0 保留为 0。
func (t *Timeline) Normalize() {
	if !ValidAspect(t.Aspect) {
		t.Aspect = "16:9"
	}
	if t.Resolution != TimelineRes1080 {
		t.Resolution = TimelineRes720
	}
	t.BgmVolume = clampVolume(t.BgmVolume, 0.2)
	if t.BgmAssetId < 0 {
		t.BgmAssetId = 0
	}
	t.SubtitleNodeId = strings.TrimSpace(t.SubtitleNodeId)
	if len(t.SubtitleNodeId) > 64 {
		t.SubtitleNodeId = ""
	}
	switch strings.ToLower(strings.TrimSpace(t.ReviewGate)) {
	case "rejected", "approved":
		t.ReviewGate = strings.ToLower(strings.TrimSpace(t.ReviewGate))
	default:
		t.ReviewGate = ""
	}
	seen := map[string]bool{}
	clips := make([]TimelineClip, 0, len(t.Clips))
	for _, c := range t.Clips {
		c.NodeId = strings.TrimSpace(c.NodeId)
		if c.NodeId == "" || len(c.NodeId) > 64 {
			continue
		}
		c.Id = strings.TrimSpace(c.Id)
		if c.Id == "" || len(c.Id) > 32 || seen[c.Id] {
			c.Id = NewTimelineClipId()
		}
		seen[c.Id] = true
		if c.AssetId < 0 {
			c.AssetId = 0
		}
		c.In = roundTenth(math.Max(0, c.In))
		c.Out = roundTenth(math.Max(0, c.Out))
		if c.Out > 0 && c.Out <= c.In {
			c.Out = 0
		}
		switch c.VoiceMode {
		case TimelineVoiceMix, TimelineVoiceOnly, TimelineVoiceOff:
		default:
			c.VoiceMode = TimelineVoiceMix
		}
		c.OrigVolume = clampVolume(c.OrigVolume, 0.35)
		c.VoiceVolume = clampVolume(c.VoiceVolume, 1)
		if c.Subtitle != nil {
			v := strings.TrimSpace(*c.Subtitle)
			if r := []rune(v); len(r) > TimelineSubtitleMax {
				v = string(r[:TimelineSubtitleMax])
			}
			c.Subtitle = &v
		}
		clips = append(clips, c)
		if len(clips) >= TimelineMaxClips {
			break
		}
	}
	t.Clips = clips
}

// NewTimelineClipId 片段 id：c_ + 8 位随机串。
func NewTimelineClipId() string {
	return "c_" + common.GetRandomString(8)
}

// clampVolume 负数 / NaN 视为未设置回落 def；0 是合法值（静音）。
func clampVolume(v, def float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return def
	}
	if v > TimelineMaxVolume {
		return TimelineMaxVolume
	}
	return math.Round(v*100) / 100
}

func roundTenth(v float64) float64 {
	return math.Round(v*10) / 10
}

// GetCanvasTimeline 取画布时间线；没有返回 ErrNotFound。
func GetCanvasTimeline(canvasId int) (*CanvasTimeline, *Timeline, error) {
	row := &CanvasTimeline{}
	if err := DB.Where("canvas_id = ?", canvasId).First(row).Error; err != nil {
		return nil, nil, wrapNotFound(err)
	}
	tl := &Timeline{}
	if err := json.Unmarshal([]byte(row.Data), tl); err != nil {
		return row, nil, err
	}
	return row, tl, nil
}

// SaveCanvasTimeline 乐观锁保存：version 与库里不一致返回 ErrTimelineVersionConflict；首次保存 version 传 0。返回新版本号。
func SaveCanvasTimeline(userId, canvasId, version int, tl *Timeline) (int, error) {
	data, err := json.Marshal(tl)
	if err != nil {
		return 0, err
	}
	now := common.GetTimestamp()
	var next int
	err = DB.Transaction(func(tx *gorm.DB) error {
		row := &CanvasTimeline{}
		err := tx.Where("canvas_id = ?", canvasId).First(row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if version != 0 {
				return ErrTimelineVersionConflict
			}
			row = &CanvasTimeline{CanvasId: canvasId, UserId: userId, Data: string(data), Version: 1, UpdatedAt: now}
			next = 1
			return tx.Create(row).Error
		}
		if err != nil {
			return err
		}
		if row.Locked {
			return ErrTimelineLocked
		}
		res := tx.Model(&CanvasTimeline{}).Where("id = ? AND version = ?", row.Id, version).
			Updates(map[string]any{"data": string(data), "version": gorm.Expr("version + 1"), "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrTimelineVersionConflict
		}
		next = version + 1
		return nil
	})
	return next, err
}
