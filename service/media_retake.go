package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 真·片段重拍（API.md 23.8）：只重做选中的那一段画面，其余原样保留。
//
// 两个阶段：
//   1. 创建时（同步）：ffmpeg 取 start / end 两帧 → 建一条 fl2v 视频生成任务（计费走任务）→ 落一条 engine=pipeline 的作业壳；
//   2. 任务成功后（worker）：新片段裁到段长、缩放到源尺寸，与 [0,start)、[end,总长) 拼成整条；原声按 keep_audio 处理。
//
// 拼接是本地 ffmpeg，不计积分；任务失败时积分由任务体系退回，作业跟着标失败。

const (
	retakeSegmentMin = 0.5 // 秒，段太短模型出不了片
	retakeDurMin     = 3   // 网关视频模型普遍的最短档
	retakeDurMax     = 15  // 最长档
	retakeStitchTime = 10 * time.Minute
)

// retakeSegmentSpec 作业 params 里落库的内容（创建时算好，拼接时直接用，不再依赖请求）。
type retakeSegmentSpec struct {
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Prompt      string  `json:"prompt"`
	Model       string  `json:"model"`
	KeepAudio   bool    `json:"keep_audio"`
	Duration    int     `json:"duration"`     // 提交给模型的秒数档
	SrcDuration float64 `json:"src_duration"` // 源视频总长
	SrcWidth    int     `json:"src_width"`
	SrcHeight   int     `json:"src_height"`
	SrcTaskId   int     `json:"src_task_id"`
	FirstFrame  int     `json:"first_frame"` // 首帧资产
	LastFrame   int     `json:"last_frame"`  // 末帧资产（end 到结尾时为 0）
	Retried     bool    `json:"retried"`     // 已经去掉末帧重提过一次
}

// createRetakeSegmentJob 片段重拍：校验区间 → 取帧 → 建任务 → 落壳。
func createRetakeSegmentJob(job *model.MediaJob, action *MediaAction, src *model.Asset, req MediaJobRequest) (*model.MediaJob, error) {
	if src.Type != model.AssetTypeVideo {
		return nil, errors.New("只能对视频做片段重拍")
	}
	if !FFmpegAvailable() {
		return nil, errors.New("服务器未安装 ffmpeg，暂不能重拍")
	}
	if DefaultStorage == nil {
		return nil, errors.New("存储未配置，暂不能处理")
	}
	if err := CheckStorage(job.UserId, job.TeamId, 0); err != nil {
		return nil, err
	}
	maxQueue, maxPerUser := mediaQueueLimits()
	if n := model.CountQueuedMediaJobsByUser(job.UserId); int(n) >= maxPerUser {
		return nil, fmt.Errorf("你已有 %d 个处理任务在排队，等其中一个完成后再提交", n)
	}
	if n := model.CountQueuedMediaJobs(); int(n) >= maxQueue {
		return nil, fmt.Errorf("处理任务排队中，前面还有 %d 个任务，请稍后再试", n)
	}

	spec := retakeSegmentSpec{
		Start:     mediaParamFloat(req.Params, "start", -1),
		End:       mediaParamFloat(req.Params, "end", -1),
		Prompt:    mediaParamStr(req.Params, "prompt"),
		Model:     strings.TrimSpace(req.Model),
		KeepAudio: mediaParamBool(req.Params, "keep_audio", true),
	}
	if spec.Model == "" {
		spec.Model = mediaParamStr(req.Params, "model")
	}
	startRatio := mediaParamFloat(req.Params, "start_ratio", -1)
	endRatio := mediaParamFloat(req.Params, "end_ratio", -1)
	if (spec.Start < 0 || spec.End < 0) && !(startRatio >= 0 && endRatio > startRatio && endRatio <= 1) {
		return nil, errors.New("请先在视频上选择要重拍的片段")
	}

	// 源信息：资产上有就用，没有就探一下（ffprobe 能直接读 http）
	spec.SrcDuration, spec.SrcWidth, spec.SrcHeight = src.Duration, src.Width, src.Height
	if spec.SrcDuration <= 0 || spec.SrcWidth <= 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		dur, w, h, _ := ProbeMediaInfo(ctx, mediaProbeSource(src))
		cancel()
		if spec.SrcDuration <= 0 {
			spec.SrcDuration = dur
		}
		if spec.SrcWidth <= 0 {
			spec.SrcWidth, spec.SrcHeight = w, h
		}
	}
	if spec.SrcDuration <= 0 {
		return nil, errors.New("读不到源视频时长，请先把视频保存到资产库")
	}
	// 前端读不到时长（素材跨域）时只给比例，这里按服务端探到的时长换算
	if spec.Start < 0 || spec.End < 0 {
		spec.Start, spec.End = startRatio*spec.SrcDuration, endRatio*spec.SrcDuration
	}
	if err := checkRetakeRange(&spec); err != nil {
		return nil, err
	}

	// 源任务：沿用提示词 / 模型 / 画幅参数
	var srcTask *model.GenerationTask
	if src.TaskId > 0 {
		if t, err := model.GetTaskById(src.TaskId); err == nil && t.Type == model.TaskTypeVideo {
			srcTask = t
			spec.SrcTaskId = t.Id
		}
	}
	if spec.Prompt == "" && srcTask != nil {
		spec.Prompt = srcTask.Prompt
	}
	if spec.Prompt == "" {
		return nil, errors.New("请写一句这段要拍成什么")
	}
	spec.Model = pickRetakeModel(spec.Model, srcTask)
	if spec.Model == "" {
		return nil, errors.New("暂无可用的视频模型，请联系运营")
	}
	price, err := model.GetModelPrice(spec.Model)
	if err != nil || price.Type != model.TaskTypeVideo {
		return nil, fmt.Errorf("模型 %s 未定价或不是视频模型", spec.Model)
	}
	provider := FindProvider(model.TaskTypeVideo, spec.Model)
	if provider == nil {
		return nil, fmt.Errorf("模型 %s 暂未接入", spec.Model)
	}
	spec.Duration = pickRetakeDuration(price, spec.End-spec.Start)

	// 取帧：start 处一帧作首帧；end 没到结尾时再取 end 处一帧作末帧
	frames, err := extractRetakeFrames(job, src, &spec)
	if err != nil {
		return nil, err
	}

	params := retakeTaskParams(srcTask, &spec)
	paramsJSON, _ := json.Marshal(params)
	inputs := []map[string]any{{"asset_id": spec.FirstFrame, "role": "first_frame"}}
	if spec.LastFrame > 0 {
		inputs = append(inputs, map[string]any{"asset_id": spec.LastFrame, "role": "last_frame"})
	}
	inputsJSON, _ := json.Marshal(inputs)
	task := &model.GenerationTask{
		UserId: job.UserId, TeamId: job.TeamId, ProjectId: job.ProjectId, CanvasId: job.CanvasId, NodeId: job.NodeId,
		Type: model.TaskTypeVideo, Provider: provider.Name(), Model: spec.Model,
		Prompt: spec.Prompt, Params: string(paramsJSON), Inputs: string(inputsJSON),
	}
	if srcTask != nil {
		task.SkillId, task.Lang, task.RetakeOf = srcTask.SkillId, srcTask.Lang, srcTask.Id
	}
	task, err = StartTask(task, provider)
	if err != nil {
		discardMediaAssets(frames)
		return nil, err
	}
	specJSON, _ := json.Marshal(spec)
	job.Engine, job.TaskId, job.Params = model.MediaEnginePipeline, task.Id, string(specJSON)
	if err := job.Insert(); err != nil {
		_ = FailTask(task.Id, "创建重拍作业失败，请重试")
		return nil, err
	}
	job.FillView()
	applyPipelineTask(job, task)
	Track(job.UserId, "media_"+action.Action, map[string]any{
		"job_id": job.Id, "asset_id": src.Id, "engine": job.Engine, "model": spec.Model, "task_id": task.Id,
		"start": spec.Start, "end": spec.End,
	})
	return job, nil
}

// checkRetakeRange 区间合法性：在源视频范围内、段长够、end 允许贴到结尾。
func checkRetakeRange(spec *retakeSegmentSpec) error {
	spec.Start = math.Round(spec.Start*100) / 100
	spec.End = math.Round(spec.End*100) / 100
	if spec.End > spec.SrcDuration {
		// 前端按比例换算会有零点几秒的误差，贴着结尾的直接收到结尾
		if spec.End-spec.SrcDuration > 0.5 {
			return fmt.Errorf("片段超出了视频长度（视频共 %.1f 秒）", spec.SrcDuration)
		}
		spec.End = spec.SrcDuration
	}
	if spec.End-spec.Start < retakeSegmentMin {
		return fmt.Errorf("重拍片段至少 %.1f 秒", retakeSegmentMin)
	}
	return nil
}

// pickRetakeModel 模型优先级：面板指定 → 成片向导默认视频模型 → 源任务模型 → 清单第一个视频模型。
func pickRetakeModel(chosen string, srcTask *model.GenerationTask) string {
	candidates := []string{chosen, strings.TrimSpace(setting.GetSettingString(setting.ComposeVideoModel))}
	if srcTask != nil {
		candidates = append(candidates, srcTask.Model)
	}
	for _, m := range candidates {
		if m != "" && FindProvider(model.TaskTypeVideo, m) != nil {
			if _, err := model.GetModelPrice(m); err == nil {
				return m
			}
		}
	}
	return PickDefaultModel(model.TaskTypeVideo)
}

// pickRetakeDuration 提交给模型的秒数档：模型声明了档位就取 ≥ 段长的最小档（都不够取最大档，拼接时会冻结末帧补齐）；
// 没声明按 ceil(段长)，夹在 3~15 秒之间。
func pickRetakeDuration(price *model.ModelPrice, seg float64) int {
	need := int(math.Ceil(seg - 1e-9))
	if need < 1 {
		need = 1
	}
	if allowed := price.AllowedDurations(); len(allowed) > 0 {
		best, maxAllowed := 0, 0
		for _, d := range allowed {
			if d > maxAllowed {
				maxAllowed = d
			}
			if d >= need && (best == 0 || d < best) {
				best = d
			}
		}
		if best > 0 {
			return best
		}
		return maxAllowed
	}
	if need < retakeDurMin {
		return retakeDurMin
	}
	if need > retakeDurMax {
		return retakeDurMax
	}
	return need
}

// retakeTaskParams 任务参数：同一个模型时沿用源任务全部参数（画幅 / 分辨率 / 音频开关都照旧）；
// 换了模型只保留画幅——其余键多是模型专属（seedance 的 generate_audio、hailuo 的分辨率档位），
// 原样带过去会被网关 400。时长换成档位，模式定为首尾帧。
func retakeTaskParams(srcTask *model.GenerationTask, spec *retakeSegmentSpec) map[string]any {
	params := map[string]any{}
	if srcTask != nil && srcTask.Params != "" {
		src := map[string]any{}
		_ = json.Unmarshal([]byte(srcTask.Params), &src)
		if srcTask.Model == spec.Model {
			params = src
		} else {
			for _, k := range []string{"aspect_ratio", "ratio"} {
				if v, ok := src[k]; ok {
					params[k] = v
				}
			}
		}
	}
	// 音频开关一律不带：成片沿用原声（keep_audio）或按新片段自带的声音处理，
	// 而 audio / generate_audio 在不少模型上会直接被网关 400（实测 hailuo-02）。
	delete(params, "audio")
	delete(params, "generate_audio")
	if _, ok := params["aspect_ratio"]; !ok {
		if _, ok2 := params["ratio"]; !ok2 && spec.SrcWidth > 0 && spec.SrcHeight > 0 {
			params["aspect_ratio"] = aspectRatioOf(spec.SrcWidth, spec.SrcHeight)
		}
	}
	params["count"] = 1
	params["duration"] = spec.Duration
	params["mode"] = "fl2v"
	return params
}

// retakeRetryWithoutLastFrame 生成任务失败且这次带了末帧：去掉末帧重提一次（不少模型不收 last_frame）。
// 只重试一次；重试的任务同样按任务体系计费退款。成功返回 true（作业继续排队等新任务）。
func retakeRetryWithoutLastFrame(j *model.MediaJob, failed *model.GenerationTask) bool {
	var spec retakeSegmentSpec
	if err := json.Unmarshal([]byte(j.Params), &spec); err != nil || spec.LastFrame <= 0 || spec.Retried {
		return false
	}
	provider := FindProvider(model.TaskTypeVideo, spec.Model)
	if provider == nil {
		return false
	}
	inputs, _ := json.Marshal([]map[string]any{{"asset_id": spec.FirstFrame, "role": "first_frame"}})
	task := &model.GenerationTask{
		UserId: failed.UserId, TeamId: failed.TeamId, ProjectId: failed.ProjectId, CanvasId: failed.CanvasId, NodeId: failed.NodeId,
		Type: model.TaskTypeVideo, Provider: provider.Name(), Model: spec.Model,
		Prompt: failed.Prompt, Params: failed.Params, Inputs: string(inputs),
		SkillId: failed.SkillId, Lang: failed.Lang, RetakeOf: failed.RetakeOf,
	}
	task, err := StartTask(task, provider)
	if err != nil {
		common.SysLog(fmt.Sprintf("media job %d retake retry without last frame failed: %v", j.Id, err))
		return false
	}
	spec.LastFrame, spec.Retried = 0, true
	specJSON, _ := json.Marshal(spec)
	if err := model.UpdateMediaJob(j.Id, map[string]any{"task_id": task.Id, "params": string(specJSON)}); err != nil {
		_ = FailTask(task.Id, "更新重拍作业失败")
		return false
	}
	common.SysLog(fmt.Sprintf("media job %d: task %d failed (%s), resubmitted as task %d without last frame", j.Id, failed.Id, truncate(failed.ErrorMsg, 80), task.Id))
	return true
}

// aspectRatioOf 像素尺寸 → 网关常见画幅标签。
func aspectRatioOf(w, h int) string {
	if w <= 0 || h <= 0 {
		return "16:9"
	}
	r := float64(w) / float64(h)
	type cand struct {
		label string
		v     float64
	}
	list := []cand{{"16:9", 16.0 / 9}, {"9:16", 9.0 / 16}, {"1:1", 1}, {"4:3", 4.0 / 3}, {"3:4", 3.0 / 4}, {"21:9", 21.0 / 9}}
	best := list[0]
	for _, c := range list[1:] {
		if math.Abs(c.v-r) < math.Abs(best.v-r) {
			best = c
		}
	}
	return best.label
}

// extractRetakeFrames 取首帧（start 处）与末帧（end 处，贴着结尾时不取），存成图片资产。
func extractRetakeFrames(job *model.MediaJob, src *model.Asset, spec *retakeSegmentSpec) ([]*model.Asset, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	input := localVideoPath(src.StorageKey)
	var dir string
	if input == "" {
		if !isFetchableUrl(src.Url) {
			return nil, errors.New("视频地址不可用")
		}
		d, err := os.MkdirTemp("", "easytv-retake-")
		if err != nil {
			return nil, err
		}
		dir = d
		defer os.RemoveAll(dir)
		input = filepath.Join(dir, "src"+extOf(src.Url, ".mp4"))
		if err := DownloadTo(ctx, src.Url, input, MediaSourceMaxByte); err != nil {
			return nil, fmt.Errorf("下载视频失败：%v", err)
		}
	}
	created := []*model.Asset{}
	first, err := saveFrameAsset(ctx, job, src, input, spec.Start, "重拍首帧")
	if err != nil {
		return nil, err
	}
	created = append(created, first)
	spec.FirstFrame = first.Id
	if spec.SrcDuration-spec.End > 0.05 {
		last, err := saveFrameAsset(ctx, job, src, input, spec.End, "重拍末帧")
		if err != nil {
			discardMediaAssets(created)
			return nil, err
		}
		created = append(created, last)
		spec.LastFrame = last.Id
	}
	return created, nil
}

// saveFrameAsset 在 at 秒处取一帧 JPG 入资产库。
func saveFrameAsset(ctx context.Context, job *model.MediaJob, src *model.Asset, input string, at float64, label string) (*model.Asset, error) {
	data, err := frameAt(ctx, input, at)
	if err != nil {
		return nil, err
	}
	key := NewStorageKey(fmt.Sprintf("retake-%d-%s.jpg", src.Id, strconv.FormatFloat(at, 'f', 2, 64)))
	url, err := DefaultStorage.Save(key, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("保存帧失败：%v", err)
	}
	asset := &model.Asset{
		UserId: job.UserId, TeamId: job.TeamId, ProjectId: job.ProjectId, Type: model.AssetTypeImage,
		Name: fmt.Sprintf("%s · %s", label, mediaBaseName(src.Name)), Url: url, StorageKey: key,
		Mime: "image/jpeg", Size: int64(len(data)), Source: model.AssetSourceMedia,
	}
	if err := asset.Insert(); err != nil {
		_ = DefaultStorage.Remove(key)
		return nil, err
	}
	return asset, nil
}

// frameAt 取 at 秒处的一帧（JPEG 字节）；贴近结尾取不到时往前退一点。
func frameAt(ctx context.Context, input string, at float64) ([]byte, error) {
	bin := FFmpegBin()
	try := func(t float64) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error",
			"-ss", strconv.FormatFloat(math.Max(0, t), 'f', 3, 64), "-i", input,
			"-frames:v", "1", "-q:v", "3", "-f", "image2", "-vcodec", "mjpeg", "pipe:1")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			return nil, fmt.Errorf("取帧失败：%s", truncate(strings.TrimSpace(stderr.String()), 120))
		}
		return out, nil
	}
	out, err := try(at)
	if err != nil && at > 0.2 {
		out, err = try(at - 0.2)
	}
	return out, err
}

// mediaProbeSource ffprobe 能读的地址：本地文件优先，否则原始 URL。
func mediaProbeSource(src *model.Asset) string {
	if local := localVideoPath(src.StorageKey); local != "" {
		return local
	}
	return src.Url
}

// ===== 作业视图 =====

// applyPipelineTask 复合作业的展示状态：壳行（数据库里的 status）没到 2 / -1 时，状态由生成任务决定；
// 任务成功但还没拼接 → 对外是「处理中 90%」，且不把任务的片段当成片给出去。
func applyPipelineTask(j *model.MediaJob, t *model.GenerationTask) {
	dbStatus := j.Status
	if dbStatus == model.MediaStatusDone || dbStatus == model.MediaStatusFailed {
		return // 拼接阶段已有结论，以壳行为准
	}
	j.ApplyTask(t, model.AssetTypeVideo)
	if t.Status == model.TaskStatusSucceeded {
		j.Status, j.Progress = model.MediaStatusRunning, 90
		if dbStatus == model.MediaStatusRunning {
			j.Progress = 95 // 正在拼接
		}
		j.OutputAssetId, j.OutputUrl, j.FinishedAt = 0, "", 0
		j.SetOutputs([]model.MediaOutput{})
	}
}

// mergePipelineJob 读取时把生成任务的状态映射到复合作业上。
func mergePipelineJob(j *model.MediaJob) {
	if j.Engine != model.MediaEnginePipeline || j.TaskId <= 0 {
		return
	}
	if j.Status == model.MediaStatusDone || j.Status == model.MediaStatusFailed {
		return
	}
	if t, err := model.GetTaskById(j.TaskId); err == nil {
		applyPipelineTask(j, t)
	}
}

// ===== worker =====

// claimReadyPipelineJob 找一条「生成任务已经成功」的复合作业并领取；任务失败 / 取消的顺手标失败。
func claimReadyPipelineJob() *model.MediaJob {
	for _, j := range model.ListQueuedPipelineJobs(20) {
		t, err := model.GetTaskById(j.TaskId)
		if err != nil {
			finishMediaFail(j, "生成任务不存在")
			continue
		}
		if j.Action == "transcribe" {
			// 转写的计费任务没有提交网关，一直停在 running：只要没被取消 / 超时判失败就能领
			if claimTranscribeJob(j, t) {
				return j
			}
			continue
		}
		switch t.Status {
		case model.TaskStatusSucceeded:
			if model.ClaimMediaJobById(j.Id) {
				j.Status = model.MediaStatusRunning
				return j
			}
		case model.TaskStatusFailed:
			if retakeRetryWithoutLastFrame(j, t) {
				continue
			}
			msg := t.ErrorMsg
			if msg == "" {
				msg = "生成失败"
			}
			finishMediaFail(j, msg)
		case model.TaskStatusCancelled:
			finishMediaFail(j, "生成已取消")
		}
	}
	return nil
}

// runPipelineJob 复合作业的第二阶段（目前只有片段重拍）。
func runPipelineJob(j *model.MediaJob) {
	switch j.Action {
	case "retake_segment":
		runRetakeStitch(j)
	case "transcribe":
		runTranscribeJob(j)
	default:
		finishMediaFail(j, "不支持的处理动作")
	}
}

// runRetakeStitch 把生成好的片段拼回源视频。
func runRetakeStitch(j *model.MediaJob) {
	var spec retakeSegmentSpec
	if err := json.Unmarshal([]byte(j.Params), &spec); err != nil || spec.End <= spec.Start {
		finishMediaFail(j, "作业参数损坏")
		return
	}
	task, err := model.GetTaskById(j.TaskId)
	if err != nil || task.ResultAssetId <= 0 {
		finishMediaFail(j, "生成任务没有产物")
		return
	}
	clip, err := model.GetAssetByIdUnscoped(task.ResultAssetId)
	if err != nil {
		finishMediaFail(j, "重拍片段已不存在")
		return
	}
	src, err := model.GetAssetByIdUnscoped(j.SourceAssetId)
	if err != nil {
		finishMediaFail(j, "源素材已不存在")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), retakeStitchTime)
	defer cancel()
	dir, err := os.MkdirTemp("", fmt.Sprintf("easytv-retake-%d-", j.Id))
	if err != nil {
		finishMediaFail(j, "创建临时目录失败")
		return
	}
	defer os.RemoveAll(dir)
	setMediaProgress(j, 92)

	srcPath, err := mediaSourceFile(ctx, dir, src)
	if err != nil {
		finishMediaFail(j, "读取源素材失败："+err.Error())
		return
	}
	clipDir := filepath.Join(dir, "clip")
	_ = os.MkdirAll(clipDir, 0o755)
	clipPath, err := mediaSourceFile(ctx, clipDir, clip)
	if err != nil {
		finishMediaFail(j, "读取重拍片段失败："+err.Error())
		return
	}
	out, err := stitchRetake(ctx, dir, srcPath, clipPath, spec.Start, spec.End, spec.KeepAudio)
	if err != nil {
		// 片段本身已经在资产库里（生成任务的产物），只是没拼上；把它带在 outputs 里让用户能拿到
		partial := []model.MediaOutput{{AssetId: clip.Id, Url: clip.Url, Type: model.AssetTypeVideo, Name: clip.Name, Label: "重拍片段"}}
		b, _ := json.Marshal(partial)
		_ = model.UpdateMediaJob(j.Id, map[string]any{"outputs": string(b)})
		finishMediaFail(j, "拼接失败："+err.Error())
		return
	}
	setMediaProgress(j, 97)
	products := []mediaProduct{videoProduct(ctx, out, src, "重拍成片")}
	outputs, created, err := uploadMediaProducts(j, src, products)
	if err != nil {
		discardMediaAssets(created)
		finishMediaFail(j, err.Error())
		return
	}
	outputs = append(outputs, model.MediaOutput{AssetId: clip.Id, Url: clip.Url, Type: model.AssetTypeVideo, Name: clip.Name, Label: "重拍片段"})
	outputsJSON, _ := json.Marshal(outputs)
	fields := map[string]any{
		"status": model.MediaStatusDone, "progress": 100, "outputs": string(outputsJSON),
		"output_asset_id": outputs[0].AssetId, "output_url": outputs[0].Url,
		"finished_at": common.GetTimestamp(), "error": "",
	}
	if err := model.UpdateMediaJob(j.Id, fields); err != nil {
		discardMediaAssets(created)
		finishMediaFail(j, "写入结果失败")
		return
	}
	Track(j.UserId, "media_ok", map[string]any{"job_id": j.Id, "action": j.Action, "outputs": len(outputs)})
	recordMediaAudit(j, "media.process.complete", "片段重拍处理完成", true)
	model.PushNotification(j.UserId, model.NotificationTypeTask, "片段重拍已完成",
		fmt.Sprintf("%.1f–%.1f 秒已换成新画面，成片进入资产库", spec.Start, spec.End), fmt.Sprintf("/create?media=%d", j.Id))
}

// stitchRetake 纯 ffmpeg 拼接：[0,start) + 新片段(裁到段长、缩放到源尺寸、不够长冻结末帧) + [end,总长)。
// keepAudio 为真时整条沿用源视频原声；否则音轨也按三段拼（新片段没声音的那段静音）。
// 返回输出文件路径。
func stitchRetake(ctx context.Context, dir, srcPath, clipPath string, start, end float64, keepAudio bool) (string, error) {
	srcDur, w, h, srcAudio := ProbeMediaInfo(ctx, srcPath)
	if srcDur <= 0 || w <= 0 || h <= 0 {
		return "", errors.New("读不到源视频信息")
	}
	clipDur, _, _, clipAudio := ProbeMediaInfo(ctx, clipPath)
	if clipDur <= 0 {
		return "", errors.New("读不到重拍片段信息")
	}
	if end > srcDur {
		end = srcDur
	}
	seg := end - start
	if seg <= 0 {
		return "", errors.New("片段区间无效")
	}
	fps := probeFps(ctx, srcPath)
	hasHead := start > 0.02
	hasTail := srcDur-end > 0.02

	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	var parts []string
	var vlabels []string
	if hasHead {
		parts = append(parts, fmt.Sprintf("[0:v]trim=0:%s,setpts=PTS-STARTPTS,format=yuv420p[vh]", f(start)))
		vlabels = append(vlabels, "[vh]")
	}
	// 新片段：铺满后居中裁到源尺寸 → 帧率对齐 → 末帧冻结兜底 → 裁到段长
	parts = append(parts, fmt.Sprintf(
		"[1:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,fps=%s,format=yuv420p,"+
			"tpad=stop_mode=clone:stop_duration=%s,trim=0:%s,setpts=PTS-STARTPTS[vm]",
		w, h, w, h, fps, f(seg+1), f(seg)))
	vlabels = append(vlabels, "[vm]")
	if hasTail {
		parts = append(parts, fmt.Sprintf("[0:v]trim=start=%s,setpts=PTS-STARTPTS,format=yuv420p[vt]", f(end)))
		vlabels = append(vlabels, "[vt]")
	}
	n := len(vlabels)
	args := []string{"-y", "-i", srcPath, "-i", clipPath}

	withAudio := false
	if keepAudio {
		if srcAudio {
			withAudio = true
		}
		parts = append(parts, fmt.Sprintf("%sconcat=n=%d:v=1:a=0[v]", strings.Join(vlabels, ""), n))
	} else if srcAudio || clipAudio {
		withAudio = true
		const afmt = "aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo"
		var alabels []string
		if hasHead {
			parts = append(parts, audioPart("0", srcAudio, 0, start, afmt, "ah"))
			alabels = append(alabels, "[ah]")
		}
		parts = append(parts, audioPart("1", clipAudio, 0, seg, afmt, "am"))
		alabels = append(alabels, "[am]")
		if hasTail {
			parts = append(parts, audioPart("0", srcAudio, end, srcDur, afmt, "at"))
			alabels = append(alabels, "[at]")
		}
		pairs := make([]string, 0, n)
		for i := range vlabels {
			pairs = append(pairs, vlabels[i]+alabels[i])
		}
		parts = append(parts, fmt.Sprintf("%sconcat=n=%d:v=1:a=1[v][a]", strings.Join(pairs, ""), n))
	} else {
		parts = append(parts, fmt.Sprintf("%sconcat=n=%d:v=1:a=0[v]", strings.Join(vlabels, ""), n))
	}

	args = append(args, "-filter_complex", strings.Join(parts, ";"), "-map", "[v]")
	switch {
	case keepAudio && withAudio:
		args = append(args, "-map", "0:a:0", "-c:a", "aac", "-b:a", "192k")
	case withAudio:
		args = append(args, "-map", "[a]", "-c:a", "aac", "-b:a", "192k")
	default:
		args = append(args, "-an")
	}
	out := filepath.Join(dir, "retake.mp4")
	args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-movflags", "+faststart", out)
	if _, err := runMediaFFmpeg(ctx, out, args...); err != nil {
		return "", err
	}
	return out, nil
}

// audioPart 一段音轨：有源就 atrim，没有就等长静音；统一到 48k 立体声。
func audioPart(input string, has bool, from, to float64, afmt, label string) string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	if has {
		return fmt.Sprintf("[%s:a]atrim=%s:%s,asetpts=PTS-STARTPTS,%s[%s]", input, f(from), f(to), afmt, label)
	}
	return fmt.Sprintf("anullsrc=channel_layout=stereo:sample_rate=48000,atrim=0:%s,asetpts=PTS-STARTPTS,%s[%s]", f(to-from), afmt, label)
}

// probeFps 源视频帧率（r_frame_rate，如 30000/1001）；读不到按 25。
func probeFps(ctx context.Context, path string) string {
	cmd := exec.CommandContext(ctx, FFprobeBin(), "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	fps := strings.TrimSpace(string(out))
	if err != nil || fps == "" || fps == "0/0" {
		return "25"
	}
	// 只信合理范围内的值
	if num, den, ok := strings.Cut(fps, "/"); ok {
		a, _ := strconv.ParseFloat(num, 64)
		b, _ := strconv.ParseFloat(den, 64)
		if b <= 0 || a/b < 5 || a/b > 120 {
			return "25"
		}
	}
	return fps
}
