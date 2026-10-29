package model

import (
	"encoding/json"

	"easytv-api/common"
)

// ComposeJob 成片合成任务（T24）：拼接镜头视频、烧字幕、混 BGM、免费用户加水印，输出进资产库。
type ComposeJob struct {
	Id       int `json:"id"`
	UserId   int `json:"user_id" gorm:"index"`
	CanvasId int `json:"canvas_id" gorm:"index"`
	TeamId   int `json:"team_id" gorm:"index;default:0;comment:创建时所在团队空间（API.md 20），worker 据此定水印与成片归属"`
	// WorkerTag 联调实例标签（环境变量 WORKER_TAG，见 common.WorkerTag）：worker 只领同标签的作业，生产为空串
	WorkerTag    string `json:"-" gorm:"type:varchar(32);index;default:'';comment:联调实例标签，worker 只领同标签作业"`
	ScreenplayId int    `json:"screenplay_id" gorm:"default:0"`
	Clips        string `json:"-" gorm:"type:text;comment:[{node_id,asset_id,url,duration,text}]"`
	Subtitles    bool   `json:"subtitles" gorm:"default:true"`
	BgmAssetId   int    `json:"bgm_asset_id" gorm:"default:0"`
	Aspect       string `json:"aspect" gorm:"type:varchar(10);default:'16:9'"`
	Lang         string `json:"lang" gorm:"type:varchar(10);default:'';comment:多语言成片语种（F5）"`
	SubtitleText string `json:"-" gorm:"type:text;comment:整份 SRT，优先于自动生成"`
	Watermark    bool   `json:"watermark" gorm:"default:false"`
	// 可控成片（API.md 12.3）：输出分辨率档位、BGM 音量、合成后质检结果
	Resolution string `json:"resolution" gorm:"type:varchar(10);default:'720p'"`
	// Preview 低清整段试听（API.md 25.4）：480p 快速编码，产物名带「预览」
	Preview bool `json:"preview" gorm:"default:false"`
	// Package 交付包（API.md 26.2）：成片之外再出无字幕母版、SRT、对白轨与素材清单
	Package     bool            `json:"package" gorm:"default:false"`
	PackageJSON string          `json:"-" gorm:"type:text;comment:交付包各文件的资产 id 与地址"`
	PackageView json.RawMessage `json:"package_files" gorm:"-"`
	// BgmVolume 不带 gorm default：带 default 时 GORM 建行会略过零值、被库里的 0.2 顶掉，用户明确设成 0（静音）就丢了（复审 D07）
	BgmVolume     float64         `json:"bgm_volume"`
	QC            string          `json:"-" gorm:"type:text;comment:合成后质检 JSON"`
	QCView        json.RawMessage `json:"qc" gorm:"-"`
	Status        int             `json:"status" gorm:"index;default:0;comment:0 排队 1 合成中 2 成功 -1 失败"`
	Progress      int             `json:"progress" gorm:"default:0"`
	OutputAssetId int             `json:"output_asset_id" gorm:"default:0"`
	OutputUrl     string          `json:"output_url" gorm:"type:varchar(1000);default:''"`
	Duration      float64         `json:"duration" gorm:"default:0"`
	Error         string          `json:"error" gorm:"type:varchar(500);default:''"`
	Warning       string          `json:"warning" gorm:"type:varchar(255);default:'';comment:降级说明（如字幕或水印未生效）"`
	CreatedAt     int64           `json:"created_at" gorm:"bigint"`
	StartedAt     int64           `json:"started_at" gorm:"bigint;default:0"`
	FinishedAt    int64           `json:"finished_at" gorm:"bigint;default:0"`

	ClipsView json.RawMessage `json:"clips" gorm:"-"`
}

func (ComposeJob) TableName() string { return "compose_job" }

const (
	ComposeStatusQueued  = 0
	ComposeStatusRunning = 1
	ComposeStatusDone    = 2
	ComposeStatusFailed  = -1

	ComposeMaxQueue = 20
	// ComposeMaxQueuePerUser 单用户在队列里的上限：全局上限是共享资源，
	// 不加这道限制时一个用户连点就能把 20 个位置占满，别人一个都排不进来。
	ComposeMaxQueuePerUser = 3

	AssetSourceCompose = "compose"
)

// ComposeClip 一个镜头片段。同一节点可以出现多次（时间线 A/B/A），ClipId 是时间线片段 id（复审 D04）。
type ComposeClip struct {
	ClipId   string  `json:"clip_id,omitempty"`
	NodeId   string  `json:"node_id"`
	AssetId  int     `json:"asset_id"`
	Url      string  `json:"url"`
	Duration float64 `json:"duration"`
	Text     string  `json:"text"`
	// 该镜头的独立配音（F05）：画布里「镜头视频 → 配音」连线上已生成的音频，合成时混进这一段
	VoiceAssetId int    `json:"voice_asset_id,omitempty"`
	VoiceUrl     string `json:"voice_url,omitempty"`
	// VoiceSkipped 有配音但语种与成片语言不一致，没混进去（合成结果里提示）
	VoiceSkipped bool `json:"voice_skipped,omitempty"`
	// 时间线（API.md 12.3）：入出点裁切（秒，Out=0 表示到结尾）与音量（nil 用缺省：原声 0.35 / 配音 1）
	In          float64  `json:"in,omitempty"`
	Out         float64  `json:"out,omitempty"`
	OrigVolume  *float64 `json:"orig_volume,omitempty"`
	VoiceVolume *float64 `json:"voice_volume,omitempty"`
	// Cues 逐条字幕（API.md 25.1）：来自分段配音的各段起止（相对配音开头的秒数），有就按条出字幕而不是整镜一句
	Cues []ComposeCue `json:"cues,omitempty"`
}

// ComposeCue 一条字幕：Start 相对该镜画面开头（已含配音延后）的秒数。
type ComposeCue struct {
	Start float64 `json:"start"`
	Dur   float64 `json:"dur"`
	Text  string  `json:"text"`
}

// VoiceCue 分段配音写在任务 result_text 里的一段：Start 相对音频开头。
type VoiceCue struct {
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	Dur     float64 `json:"dur"`
}

// VoiceCuesResult 音频任务 result_text 的结构。
type VoiceCuesResult struct {
	Cues []VoiceCue `json:"cues"`
}

func (j *ComposeJob) FillView() {
	j.QCView = nil
	if j.QC != "" {
		j.QCView = json.RawMessage(j.QC)
	}
	j.PackageView = nil
	if j.PackageJSON != "" {
		j.PackageView = json.RawMessage(j.PackageJSON)
	}
	if j.Clips == "" {
		j.ClipsView = json.RawMessage("[]")
		return
	}
	j.ClipsView = json.RawMessage(j.Clips)
}

func (j *ComposeJob) ParseClips() []ComposeClip {
	var list []ComposeClip
	_ = json.Unmarshal([]byte(j.Clips), &list)
	return list
}

func (j *ComposeJob) Insert() error {
	j.CreatedAt = common.GetTimestamp()
	return DB.Create(j).Error
}

func GetUserComposeJob(userId, id int) (*ComposeJob, error) {
	j := &ComposeJob{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(j).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	j.FillView()
	return j, nil
}

func GetComposeJobsByCanvas(userId, canvasId, limit int) ([]*ComposeJob, error) {
	var list []*ComposeJob
	err := DB.Where("user_id = ? AND canvas_id = ?", userId, canvasId).Order("id desc").Limit(limit).Find(&list).Error
	for _, j := range list {
		j.FillView()
	}
	return list, err
}

// CountQueuedComposeJobs 排队 + 合成中数量。
func CountQueuedComposeJobs() int64 {
	var n int64
	DB.Model(&ComposeJob{}).Where("status IN ?", []int{ComposeStatusQueued, ComposeStatusRunning}).Count(&n)
	return n
}

// CountQueuedComposeJobsByUser 该用户排队中 + 正在合成的任务数。
func CountQueuedComposeJobsByUser(userId int) int64 {
	var n int64
	DB.Model(&ComposeJob{}).
		Where("user_id = ? AND status IN ?", userId, []int{ComposeStatusQueued, ComposeStatusRunning}).
		Count(&n)
	return n
}

// ClaimComposeJob 原子领取一个排队任务（状态转换即锁）；没有任务返回 nil。
func ClaimComposeJob() *ComposeJob {
	var list []*ComposeJob
	if err := DB.Where("status = ? AND worker_tag = ?", ComposeStatusQueued, common.WorkerTag()).Order("id asc").Limit(1).Find(&list).Error; err != nil || len(list) == 0 {
		return nil
	}
	j := list[0]
	res := DB.Model(&ComposeJob{}).Where("id = ? AND status = ?", j.Id, ComposeStatusQueued).
		Updates(map[string]any{"status": ComposeStatusRunning, "started_at": common.GetTimestamp()})
	if res.Error != nil || res.RowsAffected == 0 {
		return nil
	}
	j.Status = ComposeStatusRunning
	return j
}

func UpdateComposeJob(id int, fields map[string]any) error {
	return DB.Model(&ComposeJob{}).Where("id = ?", id).Updates(fields).Error
}

// ResetStaleComposeJobs 把 started_at 早于 runningBefore 仍在合成中的任务标失败（执行进程已不在）。
func ResetStaleComposeJobs(runningBefore int64) int64 {
	res := DB.Model(&ComposeJob{}).Where("status = ? AND started_at < ? AND worker_tag = ?", ComposeStatusRunning, runningBefore, common.WorkerTag()).
		Updates(map[string]any{"status": ComposeStatusFailed, "error": "合成中断（服务重启或超时），请重新合成", "finished_at": common.GetTimestamp()})
	return res.RowsAffected
}

// ComposeCounts 画布的合成统计（制片报表）。
type ComposeCounts struct {
	Total    int `json:"total"`
	Done     int `json:"done"`
	Previews int `json:"previews"`
	Packages int `json:"packages"`
}

// CountComposeJobsByCanvas 合成次数 / 成功 / 预览 / 交付包。
func CountComposeJobsByCanvas(canvasId int) ComposeCounts {
	var rows []struct {
		Status  int
		Preview bool
		Package bool
		N       int
	}
	DB.Table("compose_job").Select("status, preview, package, COUNT(*) AS n").
		Where("canvas_id = ?", canvasId).Group("status, preview, package").Scan(&rows)
	c := ComposeCounts{}
	for _, r := range rows {
		c.Total += r.N
		if r.Status == ComposeStatusDone {
			c.Done += r.N
			if r.Preview {
				c.Previews += r.N
			}
			if r.Package {
				c.Packages += r.N
			}
		}
	}
	return c
}
