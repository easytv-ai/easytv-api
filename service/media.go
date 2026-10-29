package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 媒体工具作业（API.md 23）：素材后处理的统一入口。
//
// 本地动作由本文件的 worker 用 ffmpeg 直接跑，免费（同成片合成，只受队列与存储额度约束）；
// 模型动作转成生成任务提交网关，计费 / 退款 / 团队额度全走生成任务那一套。

const (
	mediaJobWorkers = 4
	mediaJobTimeout = 10 * time.Minute

	// mediaFrameWidth 抽帧产物最长边：帧是拿来看和喂模型的，不必留原尺寸。
	mediaFrameWidth = 1280
)

var mediaSem = make(chan struct{}, mediaJobWorkers)

const defaultStoryboardPrompt = `你是短视频拉片助手。下面按时间顺序给了这段视频的 %d 张关键帧。请逐帧输出：
1. 镜号与内容（主体、动作、景别）
2. 镜头运动与构图
3. 画面里的字幕或台词（若有）
最后用一段话总结这支片子的风格与节奏。用中文条目化输出，不要客套话。`

// MediaJobRequest POST /user/media/jobs 请求体。
type MediaJobRequest struct {
	Action    string         `json:"action"`
	AssetId   int            `json:"asset_id"`
	TaskId    int            `json:"task_id"`
	Params    map[string]any `json:"params"`
	NodeId    string         `json:"node_id"`
	CanvasId  int            `json:"canvas_id"`
	ProjectId int            `json:"project_id"`
	Model     string         `json:"model"`
}

// CreateMediaJob 校验并创建作业。
// 本地动作入队等 worker 处理；模型动作当场提交生成任务（同步报错时任务已标失败退分，作业状态同步为失败）。
// teamId 为请求空间；挂了项目且项目属于团队时以项目为准（API.md 20）。
func CreateMediaJob(userId, teamId int, req MediaJobRequest) (*model.MediaJob, error) {
	action := FindMediaAction(req.Action)
	if action == nil {
		return nil, errors.New("不支持的处理动作")
	}
	if setting.IsMediaActionDisabled(action.Action) {
		return nil, fmt.Errorf("%s 已被关闭", action.Label)
	}
	src, err := resolveMediaSource(userId, teamId, req.AssetId, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !action.SourceTypeAllowed(src.Type) {
		return nil, fmt.Errorf("%s 不能处理%s素材", action.Label, mediaTypeLabel(src.Type))
	}
	projectId := src.ProjectId
	if req.ProjectId > 0 {
		projectId = req.ProjectId
	}
	resolvedTeam, err := ResolveTaskTeam(userId, teamId, projectId)
	if err != nil {
		return nil, err
	}
	params := req.Params
	if params == nil {
		params = map[string]any{}
	}
	// 分镜组拼接：多图归属只能在这里查（worker 不认用户），素材列表形状也一并校验
	if action.Action == "montage" {
		spec, err := parseMontageParams(params)
		if err != nil {
			return nil, err
		}
		if _, err := validateMontageAssets(userId, teamId, src, spec); err != nil {
			return nil, err
		}
	}
	if action.Action == "mix_bgm" {
		if err := validateMixBgm(userId, teamId, src, params); err != nil {
			return nil, err
		}
	}
	paramsJSON, _ := json.Marshal(params)
	job := &model.MediaJob{
		UserId: userId, TeamId: resolvedTeam, Engine: action.Engine, Action: action.Action,
		SourceAssetId: src.Id, NodeId: truncate(strings.TrimSpace(req.NodeId), 64),
		CanvasId: req.CanvasId, ProjectId: projectId, Params: string(paramsJSON), Status: model.MediaStatusQueued,
		WorkerTag: common.WorkerTag(),
	}

	if action.Action == "transcribe" {
		// 转写不是生成任务：预扣后由本地 worker 抽音轨、调网关（API.md 35）
		return createTranscribeJob(job, action, src, req)
	}
	if action.Engine == model.MediaEngineModel {
		return createMediaModelJob(job, action, src, req)
	}
	if action.Engine == model.MediaEnginePipeline {
		return createRetakeSegmentJob(job, action, src, req)
	}
	if !FFmpegAvailable() {
		return nil, errors.New("服务器未安装 ffmpeg，暂不能处理")
	}
	if DefaultStorage == nil {
		return nil, errors.New("存储未配置，暂不能处理")
	}
	if err := CheckStorage(userId, resolvedTeam, 0); err != nil {
		return nil, err
	}
	maxQueue, maxPerUser := mediaQueueLimits()
	if n := model.CountQueuedMediaJobsByUser(userId); int(n) >= maxPerUser {
		return nil, fmt.Errorf("你已有 %d 个处理任务在排队，等其中一个完成后再提交", n)
	}
	if n := model.CountQueuedMediaJobs(); int(n) >= maxQueue {
		return nil, fmt.Errorf("处理任务排队中，前面还有 %d 个任务，请稍后再试", n)
	}
	if err := job.Insert(); err != nil {
		return nil, err
	}
	job.FillView()
	Track(userId, "media_"+action.Action, map[string]any{"job_id": job.Id, "asset_id": src.Id, "engine": action.Engine})
	return job, nil
}

// createMediaModelJob 模型动作：按能力挑模型 → 建生成任务 → 落一条作业壳。
func createMediaModelJob(job *model.MediaJob, action *MediaAction, src *model.Asset, req MediaJobRequest) (*model.MediaJob, error) {
	modelName := strings.TrimSpace(req.Model)
	available := PickModelsByAbility(action.Ability)
	if modelName == "" {
		modelName = PickModelByAbility(action.Ability, mediaPreferType(action, src))
	}
	if modelName == "" {
		return nil, fmt.Errorf("暂未接入%s模型，请联系运营", action.Label)
	}
	if !containsString(available, modelName) {
		return nil, fmt.Errorf("模型 %s 不支持%s", modelName, action.Label)
	}
	price, err := model.GetModelPrice(modelName)
	if err != nil {
		return nil, fmt.Errorf("模型 %s 未定价，请联系运营", modelName)
	}
	provider := FindProvider(price.Type, modelName)
	if provider == nil {
		return nil, fmt.Errorf("模型 %s 暂未接入", modelName)
	}
	prompt := mediaParamStr(req.Params, "prompt")
	if prompt == "" {
		prompt = action.Prompt
	}
	if prompt == "" {
		prompt = action.Label
	}
	// 动作参数原样交给网关：只认得自己那几个键，多余的会被忽略，
	// 这样模型侧新加的参数不用改这里。
	params := map[string]any{"count": 1}
	for k, v := range req.Params {
		if k == "model" {
			continue
		}
		params[k] = v
	}
	fillMediaJobDuration(params, src)
	paramsJSON, _ := json.Marshal(params)
	// 免积分标记只能由服务端写：媒体动作的 params 也是客户端透传的，同样不许自带 params.agent
	// （复核发现：媒体入口未覆盖）。正常目录下媒体任务都是 image/video/audio、命中不了免积分分支，
	// 这里按「所有接受客户端 params 的入口」统一口径堵住，避免模型目录变化后变成旁路。
	if err := CheckNoClientAgentParams(string(paramsJSON)); err != nil {
		return nil, fmt.Errorf("%s：%w", action.Label, err)
	}
	inputs, _ := json.Marshal([]map[string]any{{"asset_id": src.Id, "role": mediaInputRole(action, src)}})
	task := &model.GenerationTask{
		UserId: job.UserId, TeamId: job.TeamId, ProjectId: job.ProjectId, CanvasId: job.CanvasId, NodeId: job.NodeId,
		Type: price.Type, Provider: provider.Name(), Model: modelName,
		Prompt: prompt, Params: string(paramsJSON), Inputs: string(inputs),
	}
	task, err = StartTask(task, provider)
	if err != nil {
		return nil, err
	}
	job.Engine, job.TaskId = model.MediaEngineModel, task.Id
	if err := job.Insert(); err != nil {
		// 壳行没落下来，用户拿不到这个作业；任务已经预扣了积分，标失败退回去
		_ = FailTask(task.Id, "创建媒体作业失败，请重试")
		return nil, err
	}
	job.FillView()
	job.ApplyTask(task, action.OutputType(src.Type))
	Track(job.UserId, "media_"+action.Action, map[string]any{
		"job_id": job.Id, "asset_id": src.Id, "engine": action.Engine, "model": modelName, "task_id": task.Id,
	})
	return job, nil
}

// fillMediaJobDuration 请求没带 duration 时用源素材时长。按秒计价的模型没时长会按 5 秒预扣，
// 长视频会少扣、短视频会多扣；素材库里已经有 duration 就先写进去。
func fillMediaJobDuration(params map[string]any, src *model.Asset) {
	if params == nil || src == nil {
		return
	}
	if _, ok := params["duration"]; ok {
		return
	}
	seconds := int(src.Duration)
	if seconds < 1 {
		return
	}
	params["duration"] = seconds
}

// resolveMediaSource 找到要处理的素材：直接给资产 ID，或给一个已成功任务（取其产物）。
func resolveMediaSource(userId, teamId, assetId, taskId int) (*model.Asset, error) {
	if assetId <= 0 && taskId > 0 {
		t, err := model.GetUserTask(userId, taskId)
		if err != nil {
			return nil, errors.New("源任务不存在")
		}
		if t.ResultAssetId <= 0 {
			return nil, errors.New("源任务还没有产物")
		}
		assetId = t.ResultAssetId
	}
	if assetId <= 0 {
		return nil, errors.New("请指定要处理的素材")
	}
	return ResolveOwnedAsset(userId, teamId, assetId)
}

// ResolveOwnedAsset 按用户取资产，团队空间里队友的素材也算数（与生成任务取素材的口径一致）。
func ResolveOwnedAsset(userId, teamId, assetId int) (*model.Asset, error) {
	asset, err := model.GetAssetByIdUnscoped(assetId)
	if err != nil || asset.DeletedAt.Valid {
		return nil, errors.New("素材不存在")
	}
	if asset.UserId != userId && !(teamId > 0 && asset.TeamId == teamId) {
		return nil, errors.New("素材不存在")
	}
	return asset, nil
}

// MediaJobDetail 读作业详情；模型引擎联查生成任务，把状态与产物映射回来。
func MediaJobDetail(userId, id int) (*model.MediaJob, error) {
	job, err := model.GetUserMediaJob(userId, id)
	if err != nil {
		return nil, err
	}
	MergeMediaJobTasks([]*model.MediaJob{job})
	return job, nil
}

// MergeMediaJobTasks 列表里的模型作业补上生成任务的实时状态：
// 壳行自己只停在 0（排队），不映射的话前端刷新后会看到已完成的任务还在排队。
func MergeMediaJobTasks(list []*model.MediaJob) {
	for _, j := range list {
		if j.Engine == model.MediaEnginePipeline {
			mergePipelineJob(j)
			continue
		}
		if j.Engine != model.MediaEngineModel || j.TaskId <= 0 {
			continue
		}
		if t, err := model.GetTaskById(j.TaskId); err == nil {
			j.ApplyTask(t, "")
		}
	}
}

// RunMediaWorker 周期领取作业；由 main 的后台 goroutine 调用。
func RunMediaWorker() {
	for {
		select {
		case mediaSem <- struct{}{}:
		default:
			return // 并发已满
		}
		job := model.ClaimMediaJob()
		if job == nil {
			// 本地队列空了再看复合作业：生成任务已经成功、等着拼接的
			job = claimReadyPipelineJob()
		}
		if job == nil {
			<-mediaSem
			return
		}
		go func(j *model.MediaJob) {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("media job %d panic: %v", j.Id, r))
					finishMediaFail(j, "处理异常")
				}
				<-mediaSem
			}()
			if j.Engine == model.MediaEnginePipeline {
				runPipelineJob(j)
				return
			}
			runMediaJob(j)
		}(job)
	}
}

// ResetStaleMediaJobs 把超时仍停在处理中的作业标失败：活着的 worker 到 mediaJobTimeout 会自己结束，
// 再多等 2 分钟还是 running 的，说明执行它的进程已经没了（重启 / 崩溃）。
func ResetStaleMediaJobs() {
	if n := model.ResetStaleMediaJobs(common.GetTimestamp() - int64((mediaJobTimeout + 2*time.Minute).Seconds())); n > 0 {
		common.SysLog(fmt.Sprintf("media: reset %d stale jobs", n))
	}
}

func runMediaJob(j *model.MediaJob) {
	action := FindMediaAction(j.Action)
	if action == nil || action.Engine != model.MediaEngineLocal {
		finishMediaFail(j, "不支持的处理动作")
		return
	}
	src, err := model.GetAssetByIdUnscoped(j.SourceAssetId)
	if err != nil {
		finishMediaFail(j, "源素材已不存在")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), mediaJobTimeout)
	defer cancel()
	dir, err := os.MkdirTemp("", fmt.Sprintf("easytv-media-%d-", j.Id))
	if err != nil {
		finishMediaFail(j, "创建临时目录失败")
		return
	}
	defer os.RemoveAll(dir)
	setMediaProgress(j, 5)

	srcPath, err := mediaSourceFile(ctx, dir, src)
	if err != nil {
		finishMediaFail(j, "读取源素材失败："+err.Error())
		return
	}
	// 源视频还没有时长 / 尺寸（老资产或 OSS 直传）时顺手补齐：前端做爆款复刻要按原片时长与比例出片，
	// 浏览器读 OSS 视频又常被 CORS 拦住，作业跑完后它会重新拉一次资产拿这些字段
	fillAssetMediaInfoFromFile(ctx, src, srcPath)
	setMediaProgress(j, 35)

	var params map[string]any
	if j.Params != "" {
		_ = json.Unmarshal([]byte(j.Params), &params)
	}
	products, err := runMediaAction(ctx, dir, action, src, srcPath, params)
	if err != nil {
		finishMediaFail(j, err.Error())
		return
	}
	if len(products) == 0 {
		finishMediaFail(j, "没有产出任何文件")
		return
	}
	setMediaProgress(j, 70)

	outputs, created, err := uploadMediaProducts(j, src, products)
	if err != nil {
		discardMediaAssets(created)
		finishMediaFail(j, err.Error())
		return
	}
	// 逐帧拉片：抽完帧再让文本视觉模型读一遍画面
	if action.Action == "storyboard" && mediaParamBool(params, "analyze", true) {
		if taskId, err := createStoryboardTask(j, src, created); err != nil {
			common.SysError(fmt.Sprintf("media job %d storyboard analyze failed: %v", j.Id, err))
		} else {
			outputs = append(outputs, model.MediaOutput{Type: model.AssetTypeText, Label: "解读", TaskId: taskId})
		}
	}
	outputsJSON, _ := json.Marshal(outputs)
	fields := map[string]any{
		"status": model.MediaStatusDone, "progress": 100, "outputs": string(outputsJSON),
		"finished_at": common.GetTimestamp(), "error": "",
	}
	if len(outputs) > 0 && outputs[0].AssetId > 0 {
		fields["output_asset_id"], fields["output_url"] = outputs[0].AssetId, outputs[0].Url
	}
	if err := model.UpdateMediaJob(j.Id, fields); err != nil {
		discardMediaAssets(created)
		finishMediaFail(j, "写入结果失败")
		return
	}
	Track(j.UserId, "media_ok", map[string]any{"job_id": j.Id, "action": j.Action, "outputs": len(outputs)})
	recordMediaAudit(j, "media.process.complete", fmt.Sprintf("%s 处理完成", action.Label), true)
	model.PushNotification(j.UserId, model.NotificationTypeTask, action.Label+"已完成",
		fmt.Sprintf("产物已进入资产库（%d 个文件）", len(outputs)), fmt.Sprintf("/create?media=%d", j.Id))
}

// mediaSourceFile 源素材落地成 ffmpeg 能读的本地文件。本地存储直接读盘，省一次拷贝。
func mediaSourceFile(ctx context.Context, dir string, src *model.Asset) (string, error) {
	if local := localVideoPath(src.StorageKey); local != "" {
		return local, nil
	}
	if !isFetchableUrl(src.Url) {
		return "", errors.New("素材地址不可用")
	}
	dst := filepath.Join(dir, "source"+extOf(src.Url, ".mp4"))
	if err := DownloadTo(ctx, src.Url, dst, MediaSourceMaxByte); err != nil {
		return "", err
	}
	return dst, nil
}

// uploadMediaProducts 把产物上传成资产，返回产物清单与已创建的资产（失败时回滚用）。
func uploadMediaProducts(j *model.MediaJob, src *model.Asset, products []mediaProduct) ([]model.MediaOutput, []*model.Asset, error) {
	outputs := make([]model.MediaOutput, 0, len(products))
	created := make([]*model.Asset, 0, len(products))
	for _, p := range products {
		asset, err := SaveAssetFromFile(p.Path, AssetDraft{
			UserId: j.UserId, TeamId: j.TeamId, ProjectId: j.ProjectId, TaskId: j.TaskId,
			Type: p.Type, Name: p.Name, Mime: p.Mime, Source: model.AssetSourceMedia,
			Duration: p.Duration, Width: p.Width, Height: p.Height,
		})
		if err != nil {
			return nil, created, fmt.Errorf("保存产物失败：%v", err)
		}
		created = append(created, asset)
		outputs = append(outputs, model.MediaOutput{
			AssetId: asset.Id, Url: asset.Url, Type: asset.Type, Name: asset.Name, Label: p.Label, Time: p.Time,
			Width: asset.Width, Height: asset.Height,
			Warning: p.Warning, WarningCode: p.WarningCode, WarningParams: p.WarningParams,
		})
	}
	return outputs, created, nil
}

// createStoryboardTask 用抽出来的帧建一个文本任务，让视觉模型给一段拉片解读。
func createStoryboardTask(j *model.MediaJob, src *model.Asset, frames []*model.Asset) (int, error) {
	if len(frames) == 0 {
		return 0, errors.New("没有可解读的帧")
	}
	modelName := setting.GetMediaStoryboardModel()
	if modelName == "" || FindProvider(model.TaskTypeText, modelName) == nil {
		modelName = PickDefaultModel(model.TaskTypeText)
	}
	if modelName == "" {
		return 0, errors.New("暂无可用的文本模型")
	}
	provider := FindProvider(model.TaskTypeText, modelName)
	if provider == nil {
		return 0, errors.New("暂无可用的文本模型")
	}
	inputs := make([]map[string]any, 0, len(frames))
	for _, f := range frames {
		inputs = append(inputs, map[string]any{"asset_id": f.Id, "role": "image"})
	}
	b, _ := json.Marshal(inputs)
	task := &model.GenerationTask{
		UserId: j.UserId, TeamId: j.TeamId, ProjectId: j.ProjectId, CanvasId: j.CanvasId, NodeId: j.NodeId,
		Type: model.TaskTypeText, Provider: provider.Name(), Model: modelName,
		Prompt: fmt.Sprintf(defaultStoryboardPrompt, len(frames)),
		Params: `{"count":1}`, Inputs: string(b),
	}
	task, err := StartTask(task, provider)
	if err != nil {
		return 0, err
	}
	return task.Id, nil
}

func finishMediaFail(j *model.MediaJob, msg string) {
	_ = model.UpdateMediaJob(j.Id, map[string]any{
		"status": model.MediaStatusFailed, "error": truncate(msg, 500), "finished_at": common.GetTimestamp(),
	})
	Track(j.UserId, "media_fail", map[string]any{"job_id": j.Id, "action": j.Action, "error": truncate(msg, 120)})
	recordMediaAudit(j, "media.process.complete", fmt.Sprintf("%s 处理失败：%s", j.Action, truncate(msg, 120)), false)
	label := j.Action
	if a := FindMediaAction(j.Action); a != nil {
		label = a.Label
	}
	model.PushNotification(j.UserId, model.NotificationTypeTask, label+"失败", truncate(msg, 200), fmt.Sprintf("/create?media=%d", j.Id))
}

func setMediaProgress(j *model.MediaJob, p int) {
	_ = model.UpdateMediaJob(j.Id, map[string]any{"progress": p})
}

// discardMediaAssets 回滚已经落库的产物（含存储里的文件）。
func discardMediaAssets(list []*model.Asset) {
	for _, a := range list {
		if a.StorageKey != "" && DefaultStorage != nil {
			_ = DefaultStorage.Remove(a.StorageKey)
		}
		_ = model.DB.Unscoped().Delete(&model.Asset{}, a.Id).Error
	}
}

// recordMediaAudit 媒体作业的使用日志（service 层无 gin context）。
func recordMediaAudit(j *model.MediaJob, action, content string, success bool) {
	status := 200
	if !success {
		status = 500
	}
	user, _ := model.GetUserById(j.UserId)
	username := ""
	role := common.RoleCommonUser
	if user != nil {
		username, role = user.Username, user.Role
	}
	model.RecordAuditLog(nil, model.AuditLog{
		UserId: j.UserId, Username: username, ActorRole: role,
		CreatedAt: common.GetTimestamp(),
		Category:  model.AuditCategoryUsage, Action: action, Content: content,
		Status: status, Success: success,
		Other: model.AuditOther{Op: &model.AuditOperation{Action: action, Params: model.AuditFields{
			"job_id": j.Id, "media_action": j.Action, "engine": j.Engine, "asset_id": j.SourceAssetId,
		}}},
	})
}

// ===== 本地动作 =====

// mediaProduct 一个待上传的产物。
type mediaProduct struct {
	Path     string
	Name     string
	Mime     string
	Type     string
	Label    string
	Duration float64
	Width    int
	Height   int
	// Time 抽帧产物在源视频里的时间点（秒），透传到 MediaOutput.Time
	Time float64
	// Warning / WarningCode / WarningParams 处理成功但有保留，透传到 MediaOutput
	Warning       string
	WarningCode   string
	WarningParams map[string]any
}

func runMediaAction(ctx context.Context, dir string, a *MediaAction, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	switch a.Action {
	case "crop":
		return mediaCrop(ctx, dir, src, srcPath, params)
	case "montage":
		return mediaMontage(ctx, dir, src, srcPath, params)
	case "grid_split":
		return mediaGridSplit(ctx, dir, src, srcPath, params)
	case "trim":
		return mediaTrim(ctx, dir, src, srcPath, params)
	case "extract_audio":
		p, err := mediaExtractAudio(ctx, dir, src, srcPath, params)
		if err != nil {
			return nil, err
		}
		return []mediaProduct{p}, nil
	case "mute_video":
		p, err := mediaMuteVideo(ctx, dir, src, srcPath)
		if err != nil {
			return nil, err
		}
		return []mediaProduct{p}, nil
	case "mix_bgm":
		p, err := mediaMixBgm(ctx, dir, src, srcPath, params)
		if err != nil {
			return nil, err
		}
		return []mediaProduct{p}, nil
	case "split_av":
		audio, err := mediaExtractAudio(ctx, dir, src, srcPath, params)
		if err != nil {
			return nil, err
		}
		video, err := mediaMuteVideo(ctx, dir, src, srcPath)
		if err != nil {
			return nil, err
		}
		return []mediaProduct{audio, video}, nil
	case "split":
		return mediaSplit(ctx, dir, src, srcPath, params)
	case "extract_frame":
		return mediaExtractFrame(ctx, dir, src, srcPath, params)
	case "extract_frames", "storyboard":
		return mediaExtractFrames(ctx, dir, src, srcPath, params)
	case "compress":
		return mediaCompress(ctx, dir, src, srcPath, params)
	case "remove_silence":
		return mediaRemoveSilence(ctx, dir, src, srcPath, params)
	}
	return nil, errors.New("不支持的处理动作")
}

// mediaCrop 画面裁切：框选区域按归一化坐标裁，可再缩放到指定尺寸。
func mediaCrop(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	rect, ok := parseMediaRect(params)
	if !ok {
		return nil, errors.New("请先框选要保留的区域")
	}
	// trunc 到偶数：h264 的 420 采样不接受奇数宽高
	vf := fmt.Sprintf("crop='trunc(iw*%.6f/2)*2':'trunc(ih*%.6f/2)*2':'trunc(iw*%.6f/2)*2':'trunc(ih*%.6f/2)*2'",
		rect.W, rect.H, rect.X, rect.Y)
	scale, err := mediaScaleFilter(mediaParamStr(params, "size"))
	if err != nil {
		return nil, err
	}
	if scale != "" {
		vf += "," + scale
	}
	if src.Type == model.AssetTypeImage {
		out := filepath.Join(dir, "crop.jpg")
		if _, err := runMediaFFmpeg(ctx, out, "-y", "-i", srcPath, "-vf", vf, "-frames:v", "1", "-q:v", "2", out); err != nil {
			return nil, fmt.Errorf("裁切失败：%v", err)
		}
		return []mediaProduct{imageProduct(out, src, "裁切")}, nil
	}
	out := filepath.Join(dir, "crop.mp4")
	if _, err := runMediaFFmpeg(ctx, out, "-y", "-i", srcPath, "-vf", vf,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-c:a", "aac", "-b:a", "192k",
		"-movflags", "+faststart", out); err != nil {
		return nil, fmt.Errorf("裁切失败：%v", err)
	}
	return []mediaProduct{videoProduct(ctx, out, src, "裁切")}, nil
}

// mediaTrim 片段截取：默认重编码保证起止点精确，关掉 accurate 才走 -c copy。
func mediaTrim(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	start := mediaParamFloat(params, "start", 0)
	if start < 0 {
		start = 0
	}
	dur := mediaParamFloat(params, "duration", 0)
	if dur <= 0 {
		if end := mediaParamFloat(params, "end", 0); end > start {
			dur = end - start
		}
	}
	if dur > 0 && dur < 0.3 {
		return nil, errors.New("截取时长太短，至少 0.3 秒")
	}
	accurate := mediaParamBool(params, "accurate", true)
	args := []string{"-y"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", srcPath)
	if dur > 0 {
		args = append(args, "-t", strconv.FormatFloat(dur, 'f', 3, 64))
	}
	isAudio := src.Type == model.AssetTypeAudio
	if accurate {
		if isAudio {
			args = append(args, "-vn", "-c:a", "aac", "-b:a", "192k")
		} else {
			args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-c:a", "aac", "-b:a", "192k")
		}
	} else {
		args = append(args, "-c", "copy")
	}
	out := filepath.Join(dir, "trim"+mediaExt(src.Type))
	args = append(args, "-movflags", "+faststart", out)
	if _, err := runMediaFFmpeg(ctx, out, args...); err != nil {
		return nil, fmt.Errorf("截取失败：%v", err)
	}
	p := videoProduct(ctx, out, src, "截取")
	if isAudio {
		p = audioProduct(ctx, out, src, "截取")
	}
	return []mediaProduct{p}, nil
}

// mediaSplit 视频分割：在 at 秒切成前后两段（都重编码保证切点精确），一次出两份产物，顺序固定为前段、后段。
func mediaSplit(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	at := mediaParamFloat(params, "at", 0)
	if at <= 0 {
		return nil, errors.New("请先选择分割点")
	}
	total := src.Duration
	if total <= 0 {
		total, _, _, _ = ProbeMediaInfo(ctx, srcPath)
	}
	const minPart = 0.3
	if total > 0 && (at < minPart || at > total-minPart) {
		return nil, errors.New("分割点太靠近首尾，两段都要至少 0.3 秒")
	}
	isAudio := src.Type == model.AssetTypeAudio
	codec := []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-c:a", "aac", "-b:a", "192k"}
	if isAudio {
		codec = []string{"-vn", "-c:a", "aac", "-b:a", "192k"}
	}
	ext := mediaExt(src.Type)
	first := filepath.Join(dir, "split-1"+ext)
	second := filepath.Join(dir, "split-2"+ext)
	argsFirst := append([]string{"-y", "-i", srcPath, "-t", strconv.FormatFloat(at, 'f', 3, 64)}, codec...)
	argsFirst = append(argsFirst, "-movflags", "+faststart", first)
	if _, err := runMediaFFmpeg(ctx, first, argsFirst...); err != nil {
		return nil, fmt.Errorf("分割失败：%v", err)
	}
	argsSecond := append([]string{"-y", "-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", srcPath}, codec...)
	argsSecond = append(argsSecond, "-movflags", "+faststart", second)
	if _, err := runMediaFFmpeg(ctx, second, argsSecond...); err != nil {
		return nil, fmt.Errorf("分割失败：%v", err)
	}
	products := make([]mediaProduct, 0, 2)
	for i, out := range []string{first, second} {
		label := fmt.Sprintf("分割 %d", i+1)
		p := videoProduct(ctx, out, src, label)
		if isAudio {
			p = audioProduct(ctx, out, src, label)
		}
		products = append(products, p)
	}
	return products, nil
}

// mediaExtractAudio 音轨提取。
func mediaExtractAudio(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) (mediaProduct, error) {
	codec, ext, mime := mediaAudioFormat(mediaParamStr(params, "format"))
	ctxProbe, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, hasAudio := ProbeMedia(ctxProbe, srcPath)
	cancel()
	if !hasAudio {
		return mediaProduct{}, errors.New("这段素材没有音轨")
	}
	out := filepath.Join(dir, "audio"+ext)
	if _, err := runMediaFFmpeg(ctx, out, "-y", "-i", srcPath, "-vn", "-c:a", codec, "-b:a", "192k", out); err != nil {
		return mediaProduct{}, fmt.Errorf("提取音轨失败：%v", err)
	}
	p := mediaProduct{Path: out, Name: fmt.Sprintf("音轨 · %s%s", mediaBaseName(src.Name), ext), Mime: mime, Type: model.AssetTypeAudio, Label: "音轨"}
	p.Duration, _, _, _ = ProbeMediaInfo(ctx, out)
	return p, nil
}

// mediaMuteVideo 无声视频：优先直接复制视频流，容器不兼容时回退重编码。
func mediaMuteVideo(ctx context.Context, dir string, src *model.Asset, srcPath string) (mediaProduct, error) {
	out := filepath.Join(dir, "mute.mp4")
	if _, err := runMediaFFmpeg(ctx, out, "-y", "-i", srcPath, "-c:v", "copy", "-an", "-movflags", "+faststart", out); err != nil {
		if _, err2 := runMediaFFmpeg(ctx, out, "-y", "-i", srcPath, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-an", "-movflags", "+faststart", out); err2 != nil {
			return mediaProduct{}, fmt.Errorf("生成无声视频失败：%v", err2)
		}
	}
	return videoProduct(ctx, out, src, "无声"), nil
}

// mediaExtractFrame 取一帧：首帧 / 末帧 / 指定秒。
func mediaExtractFrame(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	at := strings.ToLower(mediaParamStr(params, "at"))
	if at == "" {
		at = "first"
	}
	out := filepath.Join(dir, "frame.jpg")
	args := []string{"-y"}
	switch at {
	case "last":
		// 容器不支持从结尾回退时改用最后一帧的 select 兜底
		args = append(args, "-sseof", "-0.5", "-i", srcPath, "-update", "1", "-frames:v", "1",
			"-q:v", "3", "-f", "image2", "-vcodec", "mjpeg", out)
	default:
		seek := "0"
		if at != "first" {
			sec := mediaParamFloat(params, "at", -1)
			if sec < 0 {
				return nil, errors.New("at 只能是 first / last / 秒数")
			}
			seek = strconv.FormatFloat(sec, 'f', 3, 64)
		}
		args = append(args, "-ss", seek, "-i", srcPath, "-vf", fmt.Sprintf("scale='min(%d,iw)':-2", mediaFrameWidth),
			"-frames:v", "1", "-q:v", "3", "-f", "image2", "-vcodec", "mjpeg", out)
	}
	_, err := runMediaFFmpeg(ctx, out, args...)
	if err != nil && at == "last" {
		// 容器不支持从结尾回退取帧（-sseof）时，退回取首帧
		args = []string{"-y", "-i", srcPath, "-vf", "select=eq(n\\,0)", "-frames:v", "1", "-q:v", "3", "-f", "image2", "-vcodec", "mjpeg", out}
		_, err = runMediaFFmpeg(ctx, out, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("取帧失败：%v", err)
	}
	p := imageProduct(out, src, "取帧")
	return []mediaProduct{p}, nil
}

// mediaExtractFrames 抽帧：按固定间隔或按画面切换，抽出来的帧是逐帧拉片的原料。
// fillAssetMediaInfoFromFile 源素材缺时长 / 尺寸时用已下载的本地文件探测并写回（同步，探测很快）。
func fillAssetMediaInfoFromFile(ctx context.Context, src *model.Asset, srcPath string) {
	if src == nil || src.Duration > 0 || (src.Type != model.AssetTypeVideo && src.Type != model.AssetTypeAudio) {
		return
	}
	dur, w, h, _ := ProbeMediaInfo(ctx, srcPath)
	if dur <= 0 {
		return
	}
	dur = math.Round(dur*100) / 100
	if err := model.SetAssetMediaInfo(src.Id, dur, w, h); err == nil {
		src.Duration, src.Width, src.Height = dur, w, h
	}
}

// spreadFrameInset 末帧内收的秒数：容器时长常比最后一帧的时间戳大（音轨更长、B 帧延迟），贴着 duration seek 会取不到帧。
const spreadFrameInset = 0.3

// spreadFrameTimes 把 count 个取帧点均匀铺在整段视频上（末尾内收 spreadFrameInset）。
// 与前端 samplePromptReverseTimes 同一口径：反推分镜结构时浏览器抽不到帧就改走这里。
func spreadFrameTimes(duration float64, count int) []float64 {
	if count < 1 {
		count = 1
	}
	end := math.Max(0, duration-spreadFrameInset)
	if end < 0.2 || count == 1 {
		return []float64{0}
	}
	times := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		t := end * float64(i) / float64(count-1)
		times = append(times, math.Round(t*1000)/1000)
	}
	return times
}

// mediaExtractFrames 抽帧：interval 按固定间隔从头抽、scene 按画面切换、spread 按 count 均匀铺满整段。
// 产物带 Time（源视频里的秒数），spread / interval 模式可算，scene 模式不填。
func mediaExtractFrames(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	if mediaParamStr(params, "mode") == "spread" {
		return mediaExtractFramesSpread(ctx, dir, src, srcPath, params)
	}
	count := paramInt(params, "count", 6)
	if count < 1 {
		count = 1
	}
	if count > mediaFramesMax {
		count = mediaFramesMax
	}
	vf := fmt.Sprintf("fps=1/%.3f", math.Max(0.2, mediaParamFloat(params, "interval", 2)))
	if mediaParamStr(params, "mode") == "scene" {
		vf = "select='gt(scene,0.3)'"
	}
	vf += fmt.Sprintf(",scale='min(%d,iw)':-2", mediaFrameWidth)
	pattern := filepath.Join(dir, "frame%03d.jpg")
	args := []string{"-y", "-i", srcPath, "-vf", vf}
	if mediaParamStr(params, "mode") == "scene" {
		args = append(args, "-vsync", "vfr")
	}
	args = append(args, "-frames:v", strconv.Itoa(count), "-q:v", "3", pattern)
	if _, err := runMediaFFmpeg(ctx, pattern, args...); err != nil {
		return nil, fmt.Errorf("抽帧失败：%v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "frame*.jpg"))
	if err != nil || len(files) == 0 {
		return nil, errors.New("这段视频抽不出帧，请确认它包含画面")
	}
	sort.Strings(files)
	interval := math.Max(0.2, mediaParamFloat(params, "interval", 2))
	products := make([]mediaProduct, 0, len(files))
	for i, f := range files {
		p := imageProduct(f, src, "抽帧")
		p.Name = fmt.Sprintf("抽帧 %02d · %s", i+1, truncate(src.Name, 30))
		if mediaParamStr(params, "mode") != "scene" {
			p.Time = math.Round(float64(i)*interval*1000) / 1000
		}
		products = append(products, p)
	}
	return products, nil
}

// mediaExtractFramesSpread 均匀铺满整段：先探时长，再逐个时间点取一帧（count ≤ mediaFramesMax，逐帧 seek 很快）。
// 时长探不到时退回按间隔抽。
func mediaExtractFramesSpread(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	count := paramInt(params, "count", 6)
	if count < 1 {
		count = 1
	}
	if count > mediaFramesMax {
		count = mediaFramesMax
	}
	duration := src.Duration
	if duration <= 0 {
		duration, _, _, _ = ProbeMediaInfo(ctx, srcPath)
	}
	if duration <= 0 {
		next := map[string]any{}
		for k, v := range params {
			next[k] = v
		}
		next["mode"] = "interval"
		return mediaExtractFrames(ctx, dir, src, srcPath, next)
	}
	scale := fmt.Sprintf("scale='min(%d,iw)':-2", mediaFrameWidth)
	products := make([]mediaProduct, 0, count)
	var lastErr error
	for i, at := range spreadFrameTimes(duration, count) {
		out := filepath.Join(dir, fmt.Sprintf("spread%03d.jpg", i+1))
		// 取帧点落在最后一帧之后时 ffmpeg 一张也不写；往前退半秒最多重试两次，还不行就跳过这一帧
		got := false
		for try := 0; try < 3 && at >= 0; try++ {
			args := []string{"-y"}
			if at > 0 {
				args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
			}
			args = append(args, "-i", srcPath, "-vf", scale, "-frames:v", "1", "-q:v", "3", out)
			if _, err := runMediaFFmpeg(ctx, out, args...); err != nil {
				lastErr = err
				at = math.Round(math.Max(0, at-0.5)*1000) / 1000
				continue
			}
			got = true
			break
		}
		if !got {
			continue
		}
		p := imageProduct(out, src, "抽帧")
		p.Name = fmt.Sprintf("抽帧 %02d · %s", len(products)+1, truncate(src.Name, 30))
		p.Time = at
		products = append(products, p)
	}
	if len(products) == 0 {
		return nil, fmt.Errorf("抽帧失败：%v", lastErr)
	}
	return products, nil
}

// mediaCompress 压缩：降分辨率 + 提高 CRF，产物与源同类型。
func mediaCompress(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	target := paramInt(params, "target_height", 0)
	crf := paramInt(params, "crf", 23)
	if crf < 0 {
		crf = 23
	}
	if crf > 51 {
		crf = 51
	}
	scale := ""
	if target > 0 {
		scale = fmt.Sprintf("scale=-2:%d", target)
	}
	if src.Type == model.AssetTypeImage {
		out := filepath.Join(dir, "compress.jpg")
		args := []string{"-y", "-i", srcPath}
		if scale != "" {
			args = append(args, "-vf", scale)
		}
		args = append(args, "-frames:v", "1", "-q:v", "4", out)
		if _, err := runMediaFFmpeg(ctx, out, args...); err != nil {
			return nil, fmt.Errorf("压缩失败：%v", err)
		}
		return []mediaProduct{imageProduct(out, src, "压缩")}, nil
	}
	out := filepath.Join(dir, "compress.mp4")
	args := []string{"-y", "-i", srcPath}
	if scale != "" {
		args = append(args, "-vf", scale)
	}
	args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", strconv.Itoa(crf),
		"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", out)
	if _, err := runMediaFFmpeg(ctx, out, args...); err != nil {
		return nil, fmt.Errorf("压缩失败：%v", err)
	}
	return []mediaProduct{videoProduct(ctx, out, src, "压缩")}, nil
}

// ===== 小工具 =====

// runMediaFFmpeg 跑一次 ffmpeg；expectPath 非空时校验产物确实生成了（失败不外抛时也要能发现）。
func runMediaFFmpeg(ctx context.Context, expectPath string, args ...string) (string, error) {
	out, err := RunFFmpeg(ctx, args...)
	if err != nil {
		return out, FFmpegError(out, err)
	}
	if expectPath != "" && !strings.Contains(expectPath, "%") {
		if st, statErr := os.Stat(expectPath); statErr != nil || st.Size() == 0 {
			return out, errors.New("没有生成输出文件")
		}
	}
	return out, nil
}

func imageProduct(path string, src *model.Asset, label string) mediaProduct {
	return mediaProduct{
		Path: path, Name: fmt.Sprintf("%s · %s.jpg", label, mediaBaseName(src.Name)),
		Mime: "image/jpeg", Type: model.AssetTypeImage, Label: label,
	}
}

func videoProduct(ctx context.Context, path string, src *model.Asset, label string) mediaProduct {
	dur, w, h, _ := ProbeMediaInfo(ctx, path)
	return mediaProduct{
		Path: path, Name: fmt.Sprintf("%s · %s.mp4", label, mediaBaseName(src.Name)),
		Mime: "video/mp4", Type: model.AssetTypeVideo, Label: label,
		Duration: dur, Width: w, Height: h,
	}
}

func audioProduct(ctx context.Context, path string, src *model.Asset, label string) mediaProduct {
	dur, _, _, _ := ProbeMediaInfo(ctx, path)
	ext := filepath.Ext(path)
	mime := "audio/mp4"
	if ext == ".mp3" {
		mime = "audio/mpeg"
	}
	return mediaProduct{
		Path: path, Name: fmt.Sprintf("%s · %s%s", label, mediaBaseName(src.Name), ext),
		Mime: mime, Type: model.AssetTypeAudio, Label: label, Duration: dur,
	}
}

// mediaBaseName 产物命名用的源素材名：去掉扩展名再截断，免得出现「重拍成片 · xxx.mp4.mp4」。
func mediaBaseName(name string) string {
	name = strings.TrimSpace(name)
	if ext := filepath.Ext(name); ext != "" && len(ext) <= 5 {
		name = strings.TrimSuffix(name, ext)
	}
	return truncate(name, 40)
}

// mediaAudioFormat 音频输出格式 → 编码器 / 扩展名 / MIME。
func mediaAudioFormat(format string) (codec, ext, mime string) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp3":
		return "libmp3lame", ".mp3", "audio/mpeg"
	default:
		return "aac", ".m4a", "audio/mp4"
	}
}

// mediaScaleFilter 输出尺寸 → ffmpeg scale 滤镜；空串表示不改尺寸。
func mediaScaleFilter(size string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(size))
	if s == "" {
		return "", nil
	}
	if h, err := strconv.Atoi(strings.TrimSuffix(s, "p")); err == nil && strings.HasSuffix(s, "p") && h > 0 {
		return fmt.Sprintf("scale=-2:%d", h), nil
	}
	parts := strings.Split(s, "x")
	if len(parts) == 2 {
		w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
		h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
		if errW == nil && errH == nil && w > 0 && h > 0 {
			return fmt.Sprintf("scale=%d:%d", w, h), nil
		}
	}
	return "", errors.New("输出尺寸格式不对，例：1280x720 或 720p")
}

func mediaExt(assetType string) string {
	if assetType == model.AssetTypeAudio {
		return ".m4a"
	}
	return ".mp4"
}

func mediaTypeLabel(assetType string) string {
	switch assetType {
	case model.AssetTypeVideo:
		return "视频"
	case model.AssetTypeImage:
		return "图片"
	case model.AssetTypeAudio:
		return "音频"
	}
	return "该类型"
}

func mediaQueueLimits() (global, perUser int) {
	global, perUser = setting.MediaQueueLimits()
	if global <= 0 {
		global = model.MediaMaxQueue
	}
	if perUser <= 0 {
		perUser = model.MediaMaxQueuePerUser
	}
	return global, perUser
}

// mediaInputRole 模型动作交给网关的素材角色。
// 深度提取图和视频都能做：图走 image，视频走 reference_video；其它动作沿用注册表。
func mediaInputRole(action *MediaAction, src *model.Asset) string {
	if action != nil && action.Action == "depth_extract" && src != nil && src.Type == model.AssetTypeImage {
		return "image"
	}
	if action != nil && action.InputRole != "" {
		return action.InputRole
	}
	return "image"
}

// mediaPreferType 自动挑模型时优先匹配的任务类型。
// 深度提取按源素材类型挑（图优先 image 模型，视频优先 video 模型）。
func mediaPreferType(action *MediaAction, src *model.Asset) string {
	if action != nil && action.Action == "depth_extract" && src != nil && src.Type != "" {
		return src.Type
	}
	if action != nil {
		return action.TaskType
	}
	return ""
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
