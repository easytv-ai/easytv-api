package model

import (
	"encoding/json"

	"easytv-api/common"
)

// MediaJob 媒体工具作业（API.md 23）：素材后处理的统一作业壳。
//
// 两个引擎：
//   - local：服务端 ffmpeg 处理（裁切 / 截取 / 分离 / 抽帧），不计积分，产物可以有多份；
//   - model：走生成任务（generation_task）调网关模型，计费与退款由任务体系负责，
//     本表只留一条壳记录（task_id 指向任务），详情读取时把任务状态映射回来。
//
// 拆成独立表而不是塞进 generation_task：本地处理没有模型可定价（成片合成同样不计积分），
// 且一次处理可能出多个产物（音轨 + 无声视频、N 张抽帧），任务表的单产物字段放不下。
type MediaJob struct {
	Id     int    `json:"id" gorm:"comment:作业ID"`
	UserId int    `json:"user_id" gorm:"index;comment:发起用户ID"`
	TeamId int    `json:"team_id" gorm:"index;default:0;comment:创建时所在团队空间（API.md 20），产物归属按它"`
	Engine string `json:"engine" gorm:"type:varchar(10);index;default:'local';comment:local 本地 ffmpeg / model 走生成任务"`
	// WorkerTag 联调实例标签（环境变量 WORKER_TAG，见 common.WorkerTag）：worker 只领同标签的作业，生产为空串
	WorkerTag     string `json:"-" gorm:"type:varchar(32);index;default:'';comment:联调实例标签，worker 只领同标签作业"`
	Action        string `json:"action" gorm:"type:varchar(32);index;comment:动作标识，见 service/media_actions.go"`
	SourceAssetId int    `json:"source_asset_id" gorm:"index;default:0;comment:源素材资产ID"`
	TaskId        int    `json:"task_id" gorm:"index;default:0;comment:model 引擎对应的生成任务ID"`
	NodeId        string `json:"node_id" gorm:"type:varchar(64);default:'';comment:画布节点ID"`
	CanvasId      int    `json:"canvas_id" gorm:"index;default:0;comment:所属画布ID"`
	ProjectId     int    `json:"project_id" gorm:"index;default:0;comment:所属项目ID"`
	Params        string `json:"-" gorm:"type:text;comment:动作参数 JSON"`
	Outputs       string `json:"-" gorm:"type:text;comment:产物 JSON 数组"`
	Status        int    `json:"status" gorm:"index;default:0;comment:0 排队 1 处理中 2 成功 -1 失败"`
	Progress      int    `json:"progress" gorm:"default:0;comment:进度 0-100"`
	OutputAssetId int    `json:"output_asset_id" gorm:"default:0;comment:首个产物资产ID"`
	OutputUrl     string `json:"output_url" gorm:"type:varchar(1000);default:'';comment:首个产物地址"`
	Error         string `json:"error" gorm:"type:varchar(500);default:'';comment:失败原因"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint"`
	StartedAt     int64  `json:"started_at" gorm:"bigint;default:0"`
	FinishedAt    int64  `json:"finished_at" gorm:"bigint;default:0"`

	ParamsView  json.RawMessage `json:"params" gorm:"-"`
	OutputsView json.RawMessage `json:"outputs" gorm:"-"`
	// ResultText model 引擎的文本产物（如拉片解读），本地引擎为空
	ResultText string `json:"result_text,omitempty" gorm:"-"`
}

func (MediaJob) TableName() string { return "media_job" }

const (
	MediaEngineLocal = "local"
	MediaEngineModel = "model"
	// MediaEnginePipeline 复合作业（API.md 23.8 片段重拍）：先走一条生成任务，任务成功后再由本地 worker 拼接。
	// 壳行在任务阶段停在 0（排队），worker 只认领「任务已成功」的那些。
	MediaEnginePipeline = "pipeline"

	MediaStatusQueued  = 0
	MediaStatusRunning = 1
	MediaStatusDone    = 2
	MediaStatusFailed  = -1

	// MediaMaxQueue 全局队列上限；MediaMaxQueuePerUser 单用户上限。
	// 与成片合成同一个道理：全局名额是共享资源，不卡单用户的话一个人连点就能占满。
	MediaMaxQueue        = 20
	MediaMaxQueuePerUser = 3
)

// MediaOutput 一个产物。前端按 label / type 决定挂成什么节点。
type MediaOutput struct {
	AssetId int    `json:"asset_id"`
	Url     string `json:"url"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Label   string `json:"label,omitempty"`
	// TaskId 复合动作附带的任务（逐帧拉片的解读任务），前端拿它去轮询 /user/task/:id
	TaskId int `json:"task_id,omitempty"`
	// Time 抽帧产物在源视频里的时间点（秒）；只有抽帧类动作会填
	Time float64 `json:"time,omitempty"`
	// Width / Height 产物像素尺寸（图片 / 视频有值），前端据此定节点比例
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Text 文本产物的正文（语音转字幕的整份 SRT，≤ 20000 字），前端不用再拉文件
	Text string `json:"text,omitempty"`
	// Cues 语音转字幕的逐句时间轴（秒）
	Cues []MediaCue `json:"cues,omitempty"`
	// Warning 处理完成但有保留的提示（给人看的中文；前端优先读 WarningCode）
	Warning string `json:"warning,omitempty"`
	// WarningCode 机器可读原因，见 MediaWarning*。旧作业没有这个字段时前端回退认 Warning 原文。
	WarningCode string `json:"warning_code,omitempty"`
	// WarningParams 给前端拼文案的数字（如 transcribe_truncated 的 minutes）
	WarningParams map[string]any `json:"warning_params,omitempty"`
}

const (
	MediaWarningTranscribeTruncated    = "transcribe_truncated"
	MediaWarningTranscribeNoTimestamps = "transcribe_no_timestamps"
	MediaWarningNoSilence              = "no_silence"
)

// MediaCue 一句字幕：起止秒数与文本。
type MediaCue struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func (j *MediaJob) FillView() {
	if j.Params == "" {
		j.ParamsView = json.RawMessage("{}")
	} else {
		j.ParamsView = json.RawMessage(j.Params)
	}
	if j.Outputs == "" {
		j.OutputsView = json.RawMessage("[]")
	} else {
		j.OutputsView = json.RawMessage(j.Outputs)
	}
}

func (j *MediaJob) ParseOutputs() []MediaOutput {
	var list []MediaOutput
	_ = json.Unmarshal([]byte(j.Outputs), &list)
	return list
}

func (j *MediaJob) SetOutputs(list []MediaOutput) {
	b, err := json.Marshal(list)
	if err != nil {
		return
	}
	j.Outputs = string(b)
	j.OutputsView = json.RawMessage(j.Outputs)
}

// ApplyTask 把 model 引擎对应任务的状态映射到作业视图上。
// outputType 是该动作产物的资产类型（服务端知道，任务本身不记）。
func (j *MediaJob) ApplyTask(t *GenerationTask, outputType string) {
	if t == nil {
		return
	}
	j.TaskId = t.Id
	j.Progress = t.Progress
	j.Error = t.ErrorMsg
	switch t.Status {
	case TaskStatusPending:
		j.Status = MediaStatusQueued
	case TaskStatusRunning:
		j.Status = MediaStatusRunning
	case TaskStatusSucceeded:
		j.Status = MediaStatusDone
	default: // failed / cancelled
		j.Status = MediaStatusFailed
	}
	if t.StartedAt > 0 {
		j.StartedAt = t.StartedAt
	}
	if t.FinishedAt > 0 {
		j.FinishedAt = t.FinishedAt
	}
	j.ResultText = t.ResultText
	if t.ResultAssetId > 0 {
		j.OutputAssetId, j.OutputUrl = t.ResultAssetId, t.ResultUrl
		j.SetOutputs([]MediaOutput{{
			AssetId: t.ResultAssetId, Url: t.ResultUrl, Type: outputType, Name: t.Model,
		}})
	}
}

func (j *MediaJob) Insert() error {
	j.CreatedAt = common.GetTimestamp()
	return DB.Create(j).Error
}

func GetUserMediaJob(userId, id int) (*MediaJob, error) {
	j := &MediaJob{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(j).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	j.FillView()
	return j, nil
}

// MediaJobFilter 列表过滤条件。
type MediaJobFilter struct {
	UserId     int
	CanvasId   int
	AssetId    int
	ProjectId  int
	Action     string
	OnlyActive bool
}

// GetMediaJobs 作业列表（用户侧）。
func GetMediaJobs(filter MediaJobFilter, pageInfo *common.PageInfo) (list []*MediaJob, total int64, err error) {
	query := DB.Model(&MediaJob{}).Where("user_id = ?", filter.UserId)
	if filter.CanvasId > 0 {
		query = query.Where("canvas_id = ?", filter.CanvasId)
	}
	if filter.ProjectId > 0 {
		query = query.Where("project_id = ?", filter.ProjectId)
	}
	if filter.AssetId > 0 {
		query = query.Where("source_asset_id = ?", filter.AssetId)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.OnlyActive {
		// 只看本地 / 复合作业：模型作业的壳行永远停在 0，按状态筛会把它们一直算成「进行中」
		query = query.Where("engine IN ? AND status IN ?", []string{MediaEngineLocal, MediaEnginePipeline}, []int{MediaStatusQueued, MediaStatusRunning})
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	for _, j := range list {
		j.FillView()
	}
	return list, total, err
}

// CountQueuedMediaJobs 排队 + 处理中的本地 / 复合作业数。
// 不数模型作业：它的状态由生成任务承载（并发与额度那边已经在管），
// 这里再数一次会让用户的队列额度被早已跑完的模型作业长期占住。
// 复合作业（片段重拍）要数：它在任务阶段就占着队列名额，任务失败 / 拼完后才释放。
func CountQueuedMediaJobs() int64 {
	var n int64
	DB.Model(&MediaJob{}).
		Where("engine IN ? AND status IN ?", []string{MediaEngineLocal, MediaEnginePipeline}, []int{MediaStatusQueued, MediaStatusRunning}).
		Count(&n)
	return n
}

// CountQueuedMediaJobsByUser 该用户排队中 + 处理中的本地 / 复合作业数。
func CountQueuedMediaJobsByUser(userId int) int64 {
	var n int64
	DB.Model(&MediaJob{}).
		Where("user_id = ? AND engine IN ? AND status IN ?", userId, []string{MediaEngineLocal, MediaEnginePipeline}, []int{MediaStatusQueued, MediaStatusRunning}).
		Count(&n)
	return n
}

// ListQueuedPipelineJobs 排队中的复合作业（最早的在前），worker 拿去看各自的生成任务到哪一步了。
func ListQueuedPipelineJobs(limit int) []*MediaJob {
	var list []*MediaJob
	DB.Where("engine = ? AND status = ? AND task_id > 0 AND worker_tag = ?", MediaEnginePipeline, MediaStatusQueued, common.WorkerTag()).
		Order("id asc").Limit(limit).Find(&list)
	return list
}

// ClaimMediaJobById 原子把一条排队作业转成处理中；被别的 worker 抢先时返回 false。
func ClaimMediaJobById(id int) bool {
	res := DB.Model(&MediaJob{}).Where("id = ? AND status = ?", id, MediaStatusQueued).
		Updates(map[string]any{"status": MediaStatusRunning, "started_at": common.GetTimestamp()})
	return res.Error == nil && res.RowsAffected > 0
}

// ClaimMediaJob 原子领取一个排队的本地作业（状态转换即锁）；没有则返回 nil。
// 必须限定 engine=local：模型作业的壳行也停在 0，被本地 worker 领走只会白跑一趟。
func ClaimMediaJob() *MediaJob {
	var list []*MediaJob
	err := DB.Where("engine = ? AND status = ? AND worker_tag = ?", MediaEngineLocal, MediaStatusQueued, common.WorkerTag()).Order("id asc").Limit(1).Find(&list).Error
	if err != nil || len(list) == 0 {
		return nil
	}
	j := list[0]
	res := DB.Model(&MediaJob{}).Where("id = ? AND status = ?", j.Id, MediaStatusQueued).
		Updates(map[string]any{"status": MediaStatusRunning, "started_at": common.GetTimestamp()})
	if res.Error != nil || res.RowsAffected == 0 {
		return nil
	}
	j.Status = MediaStatusRunning
	return j
}

func UpdateMediaJob(id int, fields map[string]any) error {
	return DB.Model(&MediaJob{}).Where("id = ?", id).Updates(fields).Error
}

// ResetStaleMediaJobs 把 started_at 早于 runningBefore 仍在处理中的作业标失败（执行进程已不在）。
func ResetStaleMediaJobs(runningBefore int64) int64 {
	res := DB.Model(&MediaJob{}).Where("status = ? AND started_at > 0 AND started_at < ? AND worker_tag = ?", MediaStatusRunning, runningBefore, common.WorkerTag()).
		Updates(map[string]any{"status": MediaStatusFailed, "error": "处理中断（服务重启或超时），请重新提交", "finished_at": common.GetTimestamp()})
	return res.RowsAffected
}
