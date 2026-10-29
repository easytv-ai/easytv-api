package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 语音转字幕（API.md 35，L8 落地部分）。
//
// 动作 transcribe 是 model 引擎（按 transcribe 能力位挑模型、按模型定价计费），
// 但不走生成任务的提交路径：网关 /v1/audio/transcriptions 收的是音频文件而不是提示词，
// 所以作业壳用 pipeline 引擎交给本地 worker——抽成 16k 单声道 mp3 后直接调网关，再把 SRT 存成文本资产。
// 计费：创建时按源时长建一条 running 的 audio 类型任务预扣（Unit=second 的定价按秒算），
// 转写成功后按实际送出的秒数退差价再结算（RefundTaskPointsPartial + CompleteTask），失败全退（FailTask）。

const (
	// transcribeMaxSeconds 单次转写上限：网关单文件 25 MB，48 kbps 单声道 mp3 20 分钟约 7 MB，留足余量。
	transcribeMaxSeconds = 20 * 60
	// transcribeDefaultSeconds 探不到源时长时的预扣基准。
	transcribeDefaultSeconds = 60
	// transcribeTextMax MediaOutput.Text 的字数上限。
	transcribeTextMax = 20000
	// srtLineMax 一行字幕最多多少个字符（中文按字数、英文按字母数），超过按标点拆句。
	srtLineMax = 42
	// transcribeAudioBitrate 送网关的 mp3 码率：语音识别 48k 单声道足够。
	transcribeAudioBitrate = "48k"
)

var transcribeLanguages = []string{"auto", "zh", "en", "ja", "ko", "es", "fr"}

// TranscribeSegment 转写出的一段（秒）。
type TranscribeSegment struct {
	Start   float64
	End     float64
	Text    string
	Speaker string
}

// TranscribeResult 转写结果；Degraded 表示网关不给时间戳，只拿到了整段文本。
type TranscribeResult struct {
	Text     string
	Language string
	Duration float64
	Segments []TranscribeSegment
	Degraded bool
}

// Transcriber 能做语音转写的厂商（EasyAPI 实现）。
type Transcriber interface {
	Transcribe(modelName string, file io.Reader, filename, language string, diarize bool) (TranscribeResult, error)
}

// transcribeSpec 作业参数（存在 media_job.params）。
type transcribeSpec struct {
	Model    string  `json:"model"`
	Language string  `json:"language"`
	Diarize  bool    `json:"diarize"`
	Duration float64 `json:"duration"` // 预扣时按的源时长（秒）
	Frozen   int     `json:"frozen"`   // 预扣积分
}

// createTranscribeJob 语音转字幕：挑模型 → 校验 → 建 running 任务预扣 → 落 pipeline 壳等 worker。
func createTranscribeJob(job *model.MediaJob, action *MediaAction, src *model.Asset, req MediaJobRequest) (*model.MediaJob, error) {
	if !FFmpegAvailable() {
		return nil, errors.New("服务器未安装 ffmpeg，暂不能转写")
	}
	if DefaultStorage == nil {
		return nil, errors.New("存储未配置，暂不能处理")
	}
	available := PickModelsByAbility(model.AbilityTranscribe)
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		modelName = mediaParamStr(req.Params, "model")
	}
	if modelName == "" {
		if v := setting.GetTranscribeModel(); v != "" && containsString(available, v) {
			modelName = v
		}
	}
	if modelName == "" {
		modelName = PickModelByAbility(model.AbilityTranscribe, model.TaskTypeAudio)
	}
	if modelName == "" {
		return nil, errors.New("暂未接入语音转写模型，请联系运营")
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
	if _, ok := provider.(Transcriber); !ok {
		return nil, fmt.Errorf("模型 %s 所在厂商不支持语音转写", modelName)
	}
	language := strings.ToLower(mediaParamStr(req.Params, "language"))
	if language == "" || !containsString(transcribeLanguages, language) {
		language = "auto"
	}
	spec := transcribeSpec{
		Model: modelName, Language: language,
		Diarize: mediaParamBool(req.Params, "diarize", false) && strings.Contains(strings.ToLower(modelName), "diarize"),
	}

	// 与 StartTask 同一套前置：团队池 / 付款人 / 并发 / 存储 / 队列
	if err := CanUseTeamPool(job.UserId, job.TeamId); err != nil {
		return nil, err
	}
	if _, err := PayerForTeam(job.UserId, job.TeamId); err != nil {
		return nil, err
	}
	if err := CheckConcurrency(job.UserId, job.TeamId); err != nil {
		return nil, err
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

	// 预扣按源时长（封顶 20 分钟，转写也只送前 20 分钟）
	spec.Duration = transcribeBillableSeconds(src)
	taskParams := map[string]any{"transcribe": true, "count": 1, "duration": int(math.Ceil(spec.Duration)), "language": language}
	paramsJSON, _ := json.Marshal(taskParams)
	isMember := IsMemberForPricing(job.UserId, job.TeamId)
	points, _, err := EstimateTaskPoints(price.Type, modelName, string(paramsJSON), isMember)
	if err != nil {
		return nil, err
	}
	inputs, _ := json.Marshal([]map[string]any{{"asset_id": src.Id, "role": action.InputRole}})
	task := &model.GenerationTask{
		UserId: job.UserId, TeamId: job.TeamId, ProjectId: job.ProjectId, CanvasId: job.CanvasId, NodeId: job.NodeId,
		Type: model.TaskTypeAudio, Provider: provider.Name(), Model: modelName,
		Prompt: action.Prompt, Params: string(paramsJSON), Inputs: string(inputs),
	}
	if err := task.Insert(); err != nil {
		return nil, &ErrTaskModel{Err: err, Msg: "创建任务失败"}
	}
	TrackOnce(job.UserId, model.TrackEventFirstTask, map[string]any{"type": task.Type, "model": task.Model})
	if err := FreezeTaskPoints(task, points); err != nil {
		_ = model.SetTaskFailed(task.Id, err.Error())
		var insufficient model.ErrInsufficientPoints
		if errors.As(err, &insufficient) {
			return nil, err
		}
		return nil, &ErrTaskModel{Err: err, Msg: "积分扣减失败"}
	}
	task.PointsFrozen = points
	spec.Frozen = points
	// 没有厂商回调推进状态，任务直接置 running；worker 完成时结算
	if err := model.SetTaskRunning(task.Id); err != nil {
		_ = FailTask(task.Id, "创建转写作业失败，请重试")
		return nil, &ErrTaskModel{Err: err, Msg: "创建任务失败"}
	}
	specJSON, _ := json.Marshal(spec)
	job.Engine, job.TaskId, job.Params = model.MediaEnginePipeline, task.Id, string(specJSON)
	if err := job.Insert(); err != nil {
		_ = FailTask(task.Id, "创建转写作业失败，请重试")
		return nil, err
	}
	job.FillView()
	task.Status = model.TaskStatusRunning
	applyPipelineTask(job, task)
	Track(job.UserId, "media_"+action.Action, map[string]any{
		"job_id": job.Id, "asset_id": src.Id, "engine": job.Engine, "model": modelName, "task_id": task.Id,
		"duration": spec.Duration, "points": points,
	})
	return job, nil
}

// transcribeBillableSeconds 预扣按的秒数：资产时长 → ffprobe → 60 秒兜底，封顶 20 分钟。
func transcribeBillableSeconds(src *model.Asset) float64 {
	dur := src.Duration
	if dur <= 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		dur, _, _, _ = ProbeMediaInfo(ctx, mediaProbeSource(src))
		cancel()
	}
	if dur <= 0 {
		dur = transcribeDefaultSeconds
	}
	return math.Min(dur, transcribeMaxSeconds)
}

// claimTranscribeJob worker 领取转写作业：计费任务还在进行中才领；被取消 / 超时判失败的作业直接标失败（积分已由任务侧退回）。
func claimTranscribeJob(j *model.MediaJob, t *model.GenerationTask) bool {
	switch t.Status {
	case model.TaskStatusPending, model.TaskStatusRunning:
		if model.ClaimMediaJobById(j.Id) {
			j.Status = model.MediaStatusRunning
			return true
		}
	case model.TaskStatusCancelled:
		finishMediaFail(j, "转写已取消")
	default:
		msg := t.ErrorMsg
		if msg == "" {
			msg = "转写失败"
		}
		finishMediaFail(j, msg)
	}
	return false
}

// runTranscribeJob worker 第二阶段：抽音轨 → 调网关 → 存 SRT → 结算。
func runTranscribeJob(j *model.MediaJob) {
	var spec transcribeSpec
	if err := json.Unmarshal([]byte(j.Params), &spec); err != nil || spec.Model == "" {
		failTranscribe(j, "作业参数损坏")
		return
	}
	src, err := model.GetAssetByIdUnscoped(j.SourceAssetId)
	if err != nil {
		failTranscribe(j, "源素材已不存在")
		return
	}
	price, err := model.GetModelPrice(spec.Model)
	if err != nil {
		failTranscribe(j, "转写模型已下架，请联系运营")
		return
	}
	transcriber, _ := FindProvider(price.Type, spec.Model).(Transcriber)
	if transcriber == nil {
		failTranscribe(j, "转写模型暂未接入")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), mediaJobTimeout)
	defer cancel()
	dir, err := os.MkdirTemp("", fmt.Sprintf("easytv-transcribe-%d-", j.Id))
	if err != nil {
		failTranscribe(j, "创建临时目录失败")
		return
	}
	defer os.RemoveAll(dir)
	setMediaProgress(j, 10)

	srcPath, err := mediaSourceFile(ctx, dir, src)
	if err != nil {
		failTranscribe(j, "读取源素材失败："+err.Error())
		return
	}
	fillAssetMediaInfoFromFile(ctx, src, srcPath)
	total, _, _, hasAudio := ProbeMediaInfo(ctx, srcPath)
	if !hasAudio {
		failTranscribe(j, "这段素材没有音轨")
		return
	}
	audioPath, sent, err := transcribeAudioTrack(ctx, dir, srcPath, total)
	if err != nil {
		failTranscribe(j, "抽取音轨失败："+err.Error())
		return
	}
	setMediaProgress(j, 40)

	f, err := os.Open(audioPath)
	if err != nil {
		failTranscribe(j, "读取音轨失败")
		return
	}
	result, err := transcriber.Transcribe(spec.Model, f, filepath.Base(audioPath), spec.Language, spec.Diarize)
	f.Close()
	if err != nil {
		failTranscribe(j, err.Error())
		return
	}
	setMediaProgress(j, 80)

	segments := result.Segments
	noTimestamps := len(segments) == 0
	if noTimestamps {
		// 网关只给了整段文本：按句拆开，时间在整段时长上均分
		dur := result.Duration
		if dur <= 0 {
			dur = sent
		}
		segments = evenTranscriptSegments(result.Text, dur)
	}
	warning, warningCode, warningParams := transcribeOutputWarning(total, noTimestamps)
	srt := BuildTranscriptSRT(segments)
	if strings.TrimSpace(srt) == "" {
		failTranscribe(j, "没有识别出任何语音")
		return
	}
	cues := transcriptCues(segments)
	plain := transcriptPlainText(segments)
	srtPath := filepath.Join(dir, "subtitle.srt")
	if err := os.WriteFile(srtPath, []byte(srt), 0o600); err != nil {
		failTranscribe(j, "写入字幕失败")
		return
	}
	asset, err := SaveAssetFromFile(srtPath, AssetDraft{
		UserId: j.UserId, TeamId: j.TeamId, ProjectId: j.ProjectId, TaskId: j.TaskId,
		Type: model.AssetTypeText, Name: fmt.Sprintf("字幕 · %s.srt", mediaBaseName(src.Name)), Mime: "text/plain",
		Source: model.AssetSourceMedia,
	})
	if err != nil {
		failTranscribe(j, "保存字幕失败："+err.Error())
		return
	}

	// 结算：按实际送出的秒数退差价，再把剩余预扣结掉
	settleTranscribePoints(j, &spec, price, sent)
	if err := CompleteTask(j.TaskId, asset.Id, asset.Url, truncateRunes(plain, transcribeTextMax)); err != nil {
		// 任务已不在进行中（用户取消 / 超时判失败，积分已退）：不能让退了积分的结果照样进资产库
		discardMediaAssets([]*model.Asset{asset})
		finishMediaFail(j, "转写已取消")
		return
	}
	outputs := []model.MediaOutput{{
		AssetId: asset.Id, Url: asset.Url, Type: model.AssetTypeText, Name: asset.Name, Label: "字幕",
		Text: truncateRunes(srt, transcribeTextMax), Cues: cues,
		Warning: warning, WarningCode: warningCode, WarningParams: warningParams,
	}}
	outputsJSON, _ := json.Marshal(outputs)
	fields := map[string]any{
		"status": model.MediaStatusDone, "progress": 100, "outputs": string(outputsJSON),
		"output_asset_id": asset.Id, "output_url": asset.Url,
		"finished_at": common.GetTimestamp(), "error": "",
	}
	if err := model.UpdateMediaJob(j.Id, fields); err != nil {
		finishMediaFail(j, "写入结果失败")
		return
	}
	Track(j.UserId, "media_ok", map[string]any{"job_id": j.Id, "action": j.Action, "outputs": 1, "cues": len(cues)})
	recordMediaAudit(j, "media.process.complete", "语音转字幕处理完成", true)
	model.PushNotification(j.UserId, model.NotificationTypeTask, "语音转字幕已完成",
		fmt.Sprintf("识别出 %d 句字幕，已进入资产库", len(cues)), fmt.Sprintf("/create?media=%d", j.Id))
}

// transcribeOutputWarning 转写成功但有保留：截断 20 分钟、网关没给时间戳。
// 两条可以同时出现；warning_code 优先截断（丢内容比时间均分更严重），中文 warning 仍拼两句。
func transcribeOutputWarning(srcSeconds float64, noTimestamps bool) (text, code string, params map[string]any) {
	var parts []string
	if srcSeconds > transcribeMaxSeconds+1 {
		minutes := transcribeMaxSeconds / 60
		parts = append(parts, fmt.Sprintf("素材长 %.0f 分钟，只转写了前 20 分钟", srcSeconds/60))
		code = model.MediaWarningTranscribeTruncated
		params = map[string]any{"minutes": minutes}
	}
	if noTimestamps {
		parts = append(parts, "网关未返回时间戳，字幕时间按句均分，请校对")
		if code == "" {
			code = model.MediaWarningTranscribeNoTimestamps
		}
	}
	return strings.Join(parts, "；"), code, params
}

// failTranscribe 转写失败：计费任务标失败全额退回，作业标失败。
func failTranscribe(j *model.MediaJob, msg string) {
	if j.TaskId > 0 {
		_ = FailTask(j.TaskId, truncate(msg, 500))
	}
	finishMediaFail(j, msg)
}

// settleTranscribePoints 实际送出的秒数比预扣时按的短就退差价；退款失败只记日志，不影响结果交付。
func settleTranscribePoints(j *model.MediaJob, spec *transcribeSpec, price *model.ModelPrice, sent float64) {
	if spec.Frozen <= 0 || price.Unit != model.PriceUnitSecond || sent <= 0 {
		return
	}
	actualSec := int(math.Ceil(sent))
	if float64(actualSec) >= spec.Duration {
		return
	}
	isMember := IsMemberForPricing(j.UserId, j.TeamId)
	actual, _, err := EstimateTaskPoints(price.Type, spec.Model, fmt.Sprintf(`{"count":1,"duration":%d}`, actualSec), isMember)
	if err != nil || actual >= spec.Frozen {
		return
	}
	reason := fmt.Sprintf("按实际 %d 秒结算，退回 %d 积分", actualSec, spec.Frozen-actual)
	if _, err := RefundTaskPointsPartial(j.TaskId, spec.Frozen-actual, reason); err != nil {
		common.SysError(fmt.Sprintf("media job %d transcribe partial refund failed: %v", j.Id, err))
	}
}

// transcribeAudioTrack 抽成 16 kHz 单声道 mp3（网关单文件 25 MB 上限），超过 20 分钟只取前 20 分钟。
// 返回音频路径与实际送出的秒数。
//
//	ffmpeg -i in -vn -ac 1 -ar 16000 -c:a libmp3lame -b:a 48k [-t 1200] out.mp3
func transcribeAudioTrack(ctx context.Context, dir, srcPath string, total float64) (string, float64, error) {
	out := filepath.Join(dir, "speech.mp3")
	args := []string{"-y", "-i", srcPath, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", transcribeAudioBitrate}
	if total > transcribeMaxSeconds {
		args = append(args, "-t", fmt.Sprintf("%d", transcribeMaxSeconds))
	}
	args = append(args, out)
	if _, err := runMediaFFmpeg(ctx, out, args...); err != nil {
		return "", 0, err
	}
	sent, _, _, _ := ProbeMediaInfo(ctx, out)
	if sent <= 0 {
		sent = math.Min(total, transcribeMaxSeconds)
	}
	return out, sent, nil
}

// ===== SRT 构造（纯函数，可测）=====

// BuildTranscriptSRT 把转写段落拼成 SRT：每段一条 cue，超过 srtLineMax 字的段按标点拆成多条、时间按字数比例分。
// 带说话人的段在文本前加「说话人 N：」。
func BuildTranscriptSRT(segments []TranscribeSegment) string {
	var b strings.Builder
	n := 0
	for _, cue := range transcriptCues(segments) {
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtClock(cue.Start), srtClock(cue.End), cue.Text)
	}
	return b.String()
}

// transcriptCues 段落 → 逐句 cue（长段按标点拆开、时间按字数比例分）。
func transcriptCues(segments []TranscribeSegment) []model.MediaCue {
	out := make([]model.MediaCue, 0, len(segments))
	for _, s := range segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		start, end := s.Start, s.End
		if end < start {
			end = start
		}
		if end-start < 0.2 {
			end = start + 0.2
		}
		parts := splitCueText(text, srtLineMax)
		totalLen := 0
		for _, p := range parts {
			totalLen += utf8.RuneCountInString(p)
		}
		cursor := start
		for i, p := range parts {
			pStart := cursor
			pEnd := end
			if i < len(parts)-1 && totalLen > 0 {
				pEnd = pStart + (end-start)*float64(utf8.RuneCountInString(p))/float64(totalLen)
			}
			cursor = pEnd
			label := p
			if sp := strings.TrimSpace(s.Speaker); sp != "" {
				label = speakerLabel(sp) + "：" + p
			}
			out = append(out, model.MediaCue{Start: round3(pStart), End: round3(pEnd), Text: label})
		}
	}
	return out
}

// transcriptPlainText 不带时间的整段文本（存到任务 result_text）。
func transcriptPlainText(segments []TranscribeSegment) string {
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// evenTranscriptSegments 只有整段文本时按句拆开、时间按字数比例铺满 duration。
func evenTranscriptSegments(text string, duration float64) []TranscribeSegment {
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return nil
	}
	if duration <= 0 {
		duration = float64(len(sentences)) * 3
	}
	total := 0
	for _, s := range sentences {
		total += utf8.RuneCountInString(s)
	}
	out := make([]TranscribeSegment, 0, len(sentences))
	cursor := 0.0
	for i, s := range sentences {
		end := duration
		if i < len(sentences)-1 && total > 0 {
			end = cursor + duration*float64(utf8.RuneCountInString(s))/float64(total)
		}
		out = append(out, TranscribeSegment{Start: cursor, End: end, Text: s})
		cursor = end
	}
	return out
}

// splitSentences 按中英文句末标点与换行拆句，标点跟在句尾。
func splitSentences(text string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, r := range text {
		if r == '\n' || r == '\r' {
			flush()
			continue
		}
		cur.WriteRune(r)
		if isSentenceEnd(r) {
			flush()
		}
	}
	flush()
	return out
}

// splitCueText 一条 cue 的文本控制在 max 字以内：先按句末标点拆，还超就按逗号 / 空格拆，再不行硬切。
func splitCueText(text string, max int) []string {
	if utf8.RuneCountInString(text) <= max {
		return []string{text}
	}
	var out []string
	for _, sentence := range splitSentences(text) {
		if utf8.RuneCountInString(sentence) <= max {
			out = append(out, sentence)
			continue
		}
		out = append(out, splitByPause(sentence, max)...)
	}
	return out
}

// splitByPause 按逗号 / 顿号 / 空格找最靠近上限的断点，找不到就硬切。
func splitByPause(text string, max int) []string {
	var out []string
	runes := []rune(text)
	for len(runes) > max {
		cut := -1
		for i := max; i >= max/3; i-- {
			if i < len(runes) && isPause(runes[i-1]) {
				cut = i
				break
			}
		}
		if cut <= 0 {
			cut = max
		}
		out = append(out, strings.TrimSpace(string(runes[:cut])))
		runes = []rune(strings.TrimSpace(string(runes[cut:])))
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '.', '!', '?', '；', ';', '…':
		return true
	}
	return false
}

func isPause(r rune) bool {
	switch r {
	case '，', ',', '、', ' ', '：', ':', '—':
		return true
	}
	return unicode.IsSpace(r)
}

// speakerLabel 网关的 speaker 可能是 "0" / "SPEAKER_1" / "A"，统一成「说话人 X」。
func speakerLabel(sp string) string {
	s := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(sp)), "SPEAKER_")
	s = strings.TrimPrefix(s, "SPEAKER ")
	if s == "" {
		return "说话人"
	}
	return "说话人 " + s
}

// srtClock 秒 → 00:00:00,000。
func srtClock(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(math.Round(sec * 1000))
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// truncateRunes 按字数截断（truncate 是按字节，中文会切到半个字）。
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
