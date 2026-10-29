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
	"sort"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 成片合成（T24）：把画布里已生成的镜头视频按顺序拼接，烧字幕、混 BGM、免费用户加水印，输出进资产库。
// 合成不计积分；worker 并发 2，单任务超时 10 分钟。

const (
	composeWorkers     = 2
	composeJobTimeout  = 10 * time.Minute
	composeClipMaxByte = 200 << 20
	composeOutW        = 1280
	composeOutH        = 720
)

var composeSem = make(chan struct{}, composeWorkers)

// ComposeRequest POST /user/canvas/:id/compose 请求。
type ComposeRequest struct {
	ClipOrder  []string `json:"clip_order"`
	Subtitles  *bool    `json:"subtitles"`
	BgmAssetId int      `json:"bgm_asset_id"`
	// Voice 是否把镜头的独立配音混进成片（F05），缺省 true
	Voice        *bool `json:"voice"`
	ScreenplayId int   `json:"screenplay_id"`
	// 多语言成片（F5）：lang 指定语种，subtitle_text 给整份 SRT（优先）
	Lang         string `json:"lang"`
	SubtitleText string `json:"subtitle_text"`
	// Timeline 按时间线合成（API.md 12.3）：给了就只合启用的镜头，顺序 / 候选 / 入出点 / 音量 / 逐镜字幕 / 画幅 / 分辨率 / BGM 都以它为准，
	// 上面的 clip_order / subtitles / voice / bgm_asset_id / lang 不再生效
	Timeline *model.Timeline `json:"timeline"`
	// Preview 低清整段试听（API.md 25.4）：480p、快速编码，规则与正式导出一致，用来验证混音 / 字幕 / 节奏
	Preview bool `json:"preview"`
	// Package 交付包（API.md 26.2）：成片之外再出无字幕母版、独立 SRT、对白轨、素材清单
	Package bool `json:"package"`
}

type graphVideoNode struct {
	id      string
	assetId int
	line    string
	seconds float64
	shot    float64
	x, y    float64
	hasShot bool
}

// collectVideoClips 从画布 graph 取已成功的视频节点，按 clip_order / 镜号 / 位置排序。
func collectVideoClips(cv *model.Canvas, clipOrder []string) ([]model.ComposeClip, error) {
	var g struct {
		Nodes []composeGraphNode `json:"nodes"`
		Edges []composeGraphEdge `json:"edges"`
	}
	if cv.Graph == "" || json.Unmarshal([]byte(cv.Graph), &g) != nil {
		return nil, errors.New("画布数据无法解析")
	}
	nodes := []graphVideoNode{}
	for _, n := range g.Nodes {
		kind, _ := n.Data["kind"].(string)
		if kind != "video" {
			continue
		}
		assetId := toInt(n.Data["assetId"])
		if assetId <= 0 {
			continue
		}
		vn := graphVideoNode{id: n.Id, assetId: assetId, x: n.Position.X, y: n.Position.Y}
		vn.line, _ = n.Data["line"].(string)
		vn.seconds = toFloat(n.Data["seconds"])
		if sp, ok := n.Data["sp"].(map[string]any); ok {
			if shot := toFloat(sp["shot"]); shot > 0 {
				vn.shot, vn.hasShot = shot, true
			}
		}
		nodes = append(nodes, vn)
	}
	if len(nodes) == 0 {
		return nil, errors.New("画布里还没有生成成功的镜头视频")
	}
	if len(clipOrder) > 0 {
		pos := map[string]int{}
		for i, id := range clipOrder {
			pos[id] = i
		}
		sort.SliceStable(nodes, func(i, j int) bool {
			pi, oki := pos[nodes[i].id]
			pj, okj := pos[nodes[j].id]
			if oki && okj {
				return pi < pj
			}
			return oki && !okj
		})
	} else {
		sort.SliceStable(nodes, func(i, j int) bool {
			if nodes[i].hasShot && nodes[j].hasShot && nodes[i].shot != nodes[j].shot {
				return nodes[i].shot < nodes[j].shot
			}
			if nodes[i].y != nodes[j].y {
				return nodes[i].y < nodes[j].y
			}
			return nodes[i].x < nodes[j].x
		})
	}
	voiceAssets := voiceAssetsByVideo(g.Nodes, g.Edges)
	if len(nodes) > ComposeMaxClips {
		return nil, fmt.Errorf("一次最多合成 %d 个镜头，当前画布有 %d 个已生成的视频，请删掉多余的镜头或分段合成", ComposeMaxClips, len(nodes))
	}
	clips := make([]model.ComposeClip, 0, len(nodes))
	var broken []string
	for _, n := range nodes {
		asset, err := canvasAsset(cv, n.assetId)
		if err != nil || asset.Type != model.AssetTypeVideo || !isFetchableUrl(asset.Url) {
			broken = append(broken, n.id)
			continue
		}
		dur := asset.Duration
		if dur <= 0 {
			dur = n.seconds
		}
		clip := model.ComposeClip{NodeId: n.id, AssetId: asset.Id, Url: asset.Url, Duration: dur, Text: n.line}
		if voiceId := voiceAssets[n.id]; voiceId > 0 {
			if a, err := canvasAsset(cv, voiceId); err == nil && a.Type == model.AssetTypeAudio && isFetchableUrl(a.Url) {
				clip.VoiceAssetId, clip.VoiceUrl = a.Id, a.Url
			}
		}
		clips = append(clips, clip)
	}
	// 不能悄悄少几镜还报成功：列出来让用户重新生成或删掉
	if len(broken) > 0 {
		Track(cv.UserId, model.TrackEventRefMissing, map[string]any{"source": "compose_clip", "count": len(broken), "canvas_id": cv.Id})
		return nil, fmt.Errorf("有 %d 个镜头的视频不可用（已删除或地址失效）：%s，请重新生成或删掉这些节点", len(broken), strings.Join(broken, "、"))
	}
	return clips, nil
}

// ComposeMaxClips 单次合成的镜头上限（ffmpeg 拼接耗时与 10 分钟超时决定）。
const ComposeMaxClips = 24

type composeGraphNode struct {
	Id       string `json:"id"`
	Position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position"`
	Data map[string]any `json:"data"`
}

type composeGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// voiceAssetsByVideo 镜头视频节点 → 配音资产：优先看「视频 → 配音」连线（策划案建画布时连好的），
// 没连线的按 sp.shot 镜号对上 sp.layer=voice 的配音节点。
func voiceAssetsByVideo(nodes []composeGraphNode, edges []composeGraphEdge) map[string]int {
	byId := map[string]composeGraphNode{}
	voiceByShot := map[float64]int{}
	for _, n := range nodes {
		byId[n.Id] = n
		if nodeKind(n) == "audio" && nodeLayer(n) == "voice" && nodeShot(n) > 0 && toInt(n.Data["assetId"]) > 0 {
			if _, ok := voiceByShot[nodeShot(n)]; !ok {
				voiceByShot[nodeShot(n)] = toInt(n.Data["assetId"])
			}
		}
	}
	out := map[string]int{}
	for _, e := range edges {
		src, okSrc := byId[e.Source]
		dst, okDst := byId[e.Target]
		if !okSrc || !okDst || out[e.Source] > 0 {
			continue
		}
		if nodeKind(src) == "video" && nodeKind(dst) == "audio" && toInt(dst.Data["assetId"]) > 0 {
			out[e.Source] = toInt(dst.Data["assetId"])
		}
	}
	for _, n := range nodes {
		if nodeKind(n) == "video" && out[n.Id] == 0 && nodeShot(n) > 0 {
			if id := voiceByShot[nodeShot(n)]; id > 0 {
				out[n.Id] = id
			}
		}
	}
	return out
}

func nodeKind(n composeGraphNode) string {
	kind, _ := n.Data["kind"].(string)
	return kind
}

func nodeLayer(n composeGraphNode) string {
	sp, _ := n.Data["sp"].(map[string]any)
	layer, _ := sp["layer"].(string)
	return layer
}

func nodeShot(n composeGraphNode) float64 {
	sp, _ := n.Data["sp"].(map[string]any)
	return toFloat(sp["shot"])
}

// isFetchableUrl 合成能取到的地址：绝对地址，或本地存储的 /upload/ 相对地址。
func isFetchableUrl(url string) bool {
	return strings.HasPrefix(url, "http") || strings.HasPrefix(url, "/upload/")
}

// CreateComposeJob 校验并入队。teamId 为合成所在空间（画布项目的团队空间或请求空间，API.md 20）：
// 决定水印（团队版会员免水印）、存储额度与成片资产的团队归属。
func CreateComposeJob(userId, teamId int, cv *model.Canvas, req ComposeRequest) (*model.ComposeJob, error) {
	if !FFmpegAvailable() {
		return nil, errors.New("服务器未安装 ffmpeg，暂不能合成")
	}
	if DefaultStorage == nil {
		return nil, errors.New("存储未配置，暂不能合成")
	}
	if err := CheckStorage(userId, teamId, 0); err != nil {
		return nil, err
	}
	// 先卡单用户，再卡全局：否则一个用户连点就能占满全局队列，别人一个都排不进来
	if n := model.CountQueuedComposeJobsByUser(userId); n >= model.ComposeMaxQueuePerUser {
		return nil, fmt.Errorf("你已有 %d 个成片在排队，等其中一个完成后再提交", n)
	}
	if n := model.CountQueuedComposeJobs(); n >= model.ComposeMaxQueue {
		return nil, fmt.Errorf("合成排队中，前面还有 %d 个任务，请稍后再试", n)
	}
	if req.Timeline != nil {
		return createTimelineComposeJob(userId, teamId, cv, req)
	}
	clips, err := collectVideoClips(cv, req.ClipOrder)
	if err != nil {
		return nil, err
	}
	if req.BgmAssetId > 0 {
		a, err := model.GetUserAsset(userId, req.BgmAssetId)
		if err != nil || a.Type != model.AssetTypeAudio {
			return nil, errors.New("BGM 需要是你资产库里的音频")
		}
	}
	subtitles := true
	if req.Subtitles != nil {
		subtitles = *req.Subtitles
	}
	aspect := "16:9"
	lang := NormalizeLang(req.Lang)
	if req.ScreenplayId > 0 {
		if sp, err := model.GetUserScreenplay(userId, req.ScreenplayId); err == nil {
			if plan, err := sp.ParsePlan(); err == nil {
				if plan.Aspect != "" {
					aspect = plan.Aspect
				}
				// 多语言：按镜号把该语种台词填进片段字幕
				if lang != "" && lang != "zh" {
					byShot := map[int]string{}
					for _, s := range plan.Shots {
						byShot[s.No] = LineForLang(s, lang)
					}
					for i := range clips {
						if no := shotNoOfNode(clips[i].NodeId); no > 0 {
							if v := byShot[no]; v != "" {
								clips[i].Text = v
							}
						}
					}
				}
			}
		}
	}
	applyVoiceChoice(clips, req.Voice == nil || *req.Voice, lang)
	watermark := setting.GetGrowthConfig().ComposeWatermark && !IsMemberForPricing(userId, teamId)
	b, _ := json.Marshal(clips)
	job := &model.ComposeJob{
		UserId: userId, TeamId: teamId, CanvasId: cv.Id, ScreenplayId: req.ScreenplayId, Clips: string(b),
		Subtitles: subtitles, BgmAssetId: req.BgmAssetId, BgmVolume: 0.2, Aspect: aspect, Watermark: watermark,
		Lang: lang, SubtitleText: truncate(strings.TrimSpace(req.SubtitleText), 20000),
		Status: model.ComposeStatusQueued, Preview: req.Preview, Package: req.Package && !req.Preview,
		WorkerTag: common.WorkerTag(),
	}
	if err := job.Insert(); err != nil {
		return nil, err
	}
	job.FillView()
	Track(userId, "compose_start", map[string]any{"job_id": job.Id, "canvas_id": cv.Id, "clips": len(clips)})
	return job, nil
}

// RunComposeWorker 周期领取任务；由 main 的后台 goroutine 调用。
func RunComposeWorker() {
	for {
		select {
		case composeSem <- struct{}{}:
		default:
			return // 并发已满
		}
		job := model.ClaimComposeJob()
		if job == nil {
			<-composeSem
			return
		}
		go func(j *model.ComposeJob) {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("compose job %d panic: %v", j.Id, r))
					finishComposeFail(j, "合成异常")
				}
				<-composeSem
			}()
			runComposeJob(j)
		}(job)
	}
}

// ResetStaleComposeJobs 把超时仍停在合成中的任务标失败。活着的 worker 到 composeJobTimeout 会自己结束，
// 再多等 2 分钟还是 running 的，说明执行它的进程已经没了（重启 / 崩溃）。
func ResetStaleComposeJobs() {
	if n := model.ResetStaleComposeJobs(common.GetTimestamp() - int64((composeJobTimeout + 2*time.Minute).Seconds())); n > 0 {
		common.SysLog(fmt.Sprintf("compose: reset %d stale jobs", n))
	}
}

func finishComposeFail(j *model.ComposeJob, msg string) {
	_ = model.UpdateComposeJob(j.Id, map[string]any{"status": model.ComposeStatusFailed, "error": truncate(msg, 500), "finished_at": common.GetTimestamp()})
	Track(j.UserId, "compose_fail", map[string]any{"job_id": j.Id, "error": truncate(msg, 120)})
	model.PushNotification(j.UserId, model.NotificationTypeTask, "成片合成失败", truncate(msg, 200), fmt.Sprintf("/canvas?id=%d", j.CanvasId))
}

func setProgress(j *model.ComposeJob, p int) {
	_ = model.UpdateComposeJob(j.Id, map[string]any{"progress": p})
}

func runComposeJob(j *model.ComposeJob) {
	ctx, cancel := context.WithTimeout(context.Background(), composeJobTimeout)
	defer cancel()
	dir, err := os.MkdirTemp("", fmt.Sprintf("easytv-compose-%d-", j.Id))
	if err != nil {
		finishComposeFail(j, "创建临时目录失败")
		return
	}
	defer os.RemoveAll(dir)

	clips := j.ParseClips()
	if len(clips) == 0 {
		finishComposeFail(j, "没有可合成的镜头")
		return
	}
	bgmUrl := ""
	// 音量明确为 0 的 BGM 不混（复审 D07）：不下载也不进滤镜
	if j.BgmAssetId > 0 && j.BgmVolume != 0 {
		if a, err := model.GetUserAsset(j.UserId, j.BgmAssetId); err == nil {
			bgmUrl = a.Url
		}
	}
	res, err := renderCompose(ctx, dir, clips, renderOptions{
		Aspect: j.Aspect, Resolution: j.Resolution, Subtitles: j.Subtitles, SubtitleText: j.SubtitleText,
		BgmUrl: bgmUrl, BgmVolume: j.BgmVolume, Watermark: j.Watermark, JobId: j.Id, Preview: j.Preview, Package: j.Package,
		Progress: func(p int) { setProgress(j, p) },
	})
	if err != nil {
		finishComposeFail(j, err.Error())
		return
	}
	final, total, warning, outW, outH := res.Path, res.Total, res.Warning, res.Width, res.Height
	qcJSON, _ := json.Marshal(res.QC)

	// 4. 上传并入资产库
	f, err := os.Open(final)
	if err != nil {
		finishComposeFail(j, "读取成片失败")
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	key := NewStorageKey(fmt.Sprintf("compose-%d.mp4", j.Id))
	url, err := DefaultStorage.Save(key, f)
	if err != nil {
		finishComposeFail(j, "上传成片失败："+err.Error())
		return
	}
	dur, _ := ProbeMedia(ctx, final)
	if dur <= 0 {
		dur = total
	}
	cv, _ := model.GetUserCanvas(j.UserId, j.CanvasId)
	projectId := 0
	if cv != nil {
		projectId = cv.ProjectId
	}
	// 成片归属沿用创建时存下的空间（worker 没有请求上下文）
	asset := &model.Asset{
		UserId: j.UserId, TeamId: j.TeamId, ProjectId: projectId, Type: model.AssetTypeVideo, Name: composeAssetName(j),
		Url: url, StorageKey: key, Mime: "video/mp4", Source: model.AssetSourceCompose, Duration: dur, Width: outW, Height: outH,
	}
	if st != nil {
		asset.Size = st.Size()
	}
	if err := asset.Insert(); err != nil {
		_ = DefaultStorage.Remove(key)
		finishComposeFail(j, "写入资产失败")
		return
	}
	go EnsureAssetCover(asset)
	fields := map[string]any{
		"status": model.ComposeStatusDone, "progress": 100, "output_asset_id": asset.Id, "output_url": url,
		"duration": dur, "warning": truncate(warning, 250), "qc": string(qcJSON), "finished_at": common.GetTimestamp(),
	}
	if res.Package != nil {
		pkg, pkgWarn := uploadComposePackage(j, cv, clips, res, projectId, url)
		if pkgWarn != "" {
			fields["warning"] = truncate(strings.TrimSpace(warning+" "+pkgWarn), 250)
		}
		if b, err := json.Marshal(pkg); err == nil {
			fields["package_json"] = string(b)
		}
	}
	_ = model.UpdateComposeJob(j.Id, fields)
	Track(j.UserId, "compose_ok", map[string]any{"job_id": j.Id, "asset_id": asset.Id, "duration": dur, "clips": len(clips)})
	if j.Preview {
		model.PushNotification(j.UserId, model.NotificationTypeTask, "预览已合成", fmt.Sprintf("《%s》的低清预览已生成，回到合成步骤试听", assetTitle(cv)), fmt.Sprintf("/create?compose=%d", j.Id))
	} else {
		model.PushNotification(j.UserId, model.NotificationTypeTask, "成片已合成", fmt.Sprintf("《%s》已进入资产库，可以发布为作品了", assetTitle(cv)), fmt.Sprintf("/create?compose=%d", j.Id))
	}
}

// renderOptions 渲染一条成片需要的参数（不含数据库操作，测试可直接调用）。
type renderOptions struct {
	Aspect, Resolution string
	Subtitles          bool
	SubtitleText       string
	BgmUrl             string
	// BgmVolume 负数表示未设置（回落 0.2）；0 表示静音，此时 BgmUrl 不会被混入（复审 D07）
	BgmVolume float64
	Watermark bool
	JobId     int
	Progress  func(int)
	// Preview 低清试听：480p + 快速编码
	Preview bool
	// Package 交付包：额外保留无字幕母版、SRT 文本、对白轨
	Package bool
}

// packageFiles 交付包在临时目录里的产物。
type packageFiles struct {
	MasterPath string
	SRT        string
	VoicePath  string
}

type renderResult struct {
	Path          string
	Total         float64
	Warning       string
	Package       *packageFiles
	Width, Height int
	QC            ComposeQC
}

// renderCompose 下载片段 → 逐段裁切 / 混音 / 统一编码 → 拼接 → 字幕 / BGM / 水印 → 质检。
func renderCompose(ctx context.Context, dir string, clips []model.ComposeClip, opt renderOptions) (*renderResult, error) {
	progress := opt.Progress
	if progress == nil {
		progress = func(int) {}
	}
	outW, outH := composeOutputSize(opt.Aspect, opt.Resolution)
	if opt.Preview {
		outW, outH = composePreviewSize(opt.Aspect)
	}
	enc := encodeArgs(opt.Preview)

	// 1. 下载并统一每段编码（同分辨率 / 帧率 / 音轨），记录真实时长
	normalized := make([]string, 0, len(clips))
	total := 0.0
	voiceSkipped := 0
	voiceParts := make([]voiceTrackPart, 0, len(clips))
	for i := range clips {
		src := filepath.Join(dir, fmt.Sprintf("src%02d.mp4", i))
		if err := DownloadTo(ctx, clips[i].Url, src, composeClipMaxByte); err != nil {
			return nil, fmt.Errorf("下载镜头 %d 失败：%v", i+1, err)
		}
		dur, hasAudio := ProbeMedia(ctx, src)
		if dur <= 0 {
			dur = clips[i].Duration
		}
		if dur <= 0 {
			dur = 5
		}
		// 入出点裁切：钳在素材时长内，至少留 0.3 秒。seg 是用户选定的画面段长，渲染时先严格截到 [in, in+seg]（复审 D01）
		in, seg := trimRange(clips[i].In, clips[i].Out, dur)
		dur = seg
		voice := ""
		if clips[i].VoiceUrl != "" {
			voice = filepath.Join(dir, fmt.Sprintf("voice%02d%s", i, extOf(clips[i].VoiceUrl, ".mp3")))
			if err := DownloadTo(ctx, clips[i].VoiceUrl, voice, 50<<20); err != nil {
				return nil, fmt.Errorf("下载镜头 %d 的配音失败：%v", i+1, err)
			}
			voiceDur, _ := ProbeMedia(ctx, voice)
			// 配音比画面长：冻结末帧补齐（最多补 10 秒），字幕时间轴跟着用补齐后的时长
			dur = clipDurationWithVoice(dur, voiceDur)
		}
		if clips[i].VoiceSkipped {
			voiceSkipped++
		}
		clips[i].Duration = dur
		dst := filepath.Join(dir, fmt.Sprintf("clip%02d.mp4", i))
		// 缺省音量：有配音时原声压到 35%，没配音保持原音量
		origVol, voiceVol := 1.0, 1.0
		if voice != "" {
			origVol = 0.35
		}
		if clips[i].OrigVolume != nil {
			origVol = *clips[i].OrigVolume
		}
		if clips[i].VoiceVolume != nil {
			voiceVol = *clips[i].VoiceVolume
		}
		if err := normalizeClip(ctx, src, dst, outW, outH, clipRender{in: in, seg: seg, dur: dur, hasAudio: hasAudio, voice: voice, origVol: origVol, voiceVol: voiceVol, enc: enc}); err != nil {
			return nil, fmt.Errorf("镜头 %d 转码失败：%v", i+1, err)
		}
		normalized = append(normalized, dst)
		voiceParts = append(voiceParts, voiceTrackPart{Path: voice, Dur: dur, Volume: voiceVol})
		total += dur
		progress(10 + int(float64(i+1)/float64(len(clips))*50))
	}

	// 2. 拼接
	listPath := filepath.Join(dir, "list.txt")
	var lb strings.Builder
	for _, p := range normalized {
		lb.WriteString("file '" + p + "'\n")
	}
	if err := os.WriteFile(listPath, []byte(lb.String()), 0o600); err != nil {
		return nil, errors.New("写入拼接列表失败")
	}
	merged := filepath.Join(dir, "merged.mp4")
	if out, err := RunFFmpeg(ctx, "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", "-movflags", "+faststart", merged); err != nil {
		return nil, errors.New("拼接失败：" + FFmpegError(out, err).Error())
	}
	progress(70)

	// 3. 字幕 / BGM / 水印一次滤镜处理；失败则去掉字幕与水印再试一次
	final := filepath.Join(dir, "final.mp4")
	warning := ""
	if voiceSkipped > 0 {
		warning = fmt.Sprintf("%d 个镜头的配音语种与成片语言不一致，未混入", voiceSkipped)
	}
	srtPath := ""
	if opt.Subtitles {
		// 前端给了整份 SRT 就直接用（多语言 / 手工校对过的字幕）
		if custom := strings.TrimSpace(opt.SubtitleText); custom != "" {
			srtPath = filepath.Join(dir, "subs.srt")
			_ = os.WriteFile(srtPath, []byte(custom), 0o600)
		} else if srt := BuildSRT(clips); srt != "" {
			srtPath = filepath.Join(dir, "subs.srt")
			_ = os.WriteFile(srtPath, []byte(srt), 0o600)
		}
	}
	bgmUrl, bgmVol := resolveBgm(opt.BgmUrl, opt.BgmVolume)
	bgmPath := ""
	if bgmUrl != "" {
		bgmPath = filepath.Join(dir, "bgm"+extOf(bgmUrl, ".mp3"))
		if err := DownloadTo(ctx, bgmUrl, bgmPath, 50<<20); err != nil {
			bgmPath = ""
			warning = strings.TrimSpace(warning + " BGM 下载失败，已跳过")
		}
	}
	if miss := missingCJKFontWarning(srtPath); miss != "" {
		warning = strings.TrimSpace(warning + " " + miss)
	}
	args, useFilters := finalPassArgs(merged, final, srtPath, bgmPath, bgmVol, opt.Watermark, total, outW, outH, enc)
	if out, err := RunFFmpeg(ctx, args...); err != nil {
		if !useFilters {
			return nil, errors.New("合成失败：" + LastLine(out))
		}
		common.SysError(fmt.Sprintf("compose job %d filter pass failed, retry plain: %s", opt.JobId, LastLine(out)))
		args2, _ := finalPassArgs(merged, final, "", bgmPath, bgmVol, false, total, outW, outH, enc)
		if out2, err2 := RunFFmpeg(ctx, args2...); err2 != nil {
			return nil, errors.New("合成失败：" + LastLine(out2))
		}
		warning = strings.TrimSpace(warning + " 字幕或水印未能生效（服务器缺少字体或字幕库）")
	}
	progress(90)

	// 质检：时长、分辨率、音轨与预期不符就写进 warning，别让用户拿到坏片才发现
	qc := checkComposeOutput(ctx, final, total, outW, outH)
	if len(qc.Issues) > 0 {
		warning = strings.TrimSpace(warning + " " + strings.Join(qc.Issues, "；"))
	}
	res := &renderResult{Path: final, Total: total, Warning: warning, Width: outW, Height: outH, QC: qc}
	if opt.Package {
		// 交付包（API.md 26.2）：母版 = 拼接后、加字幕 / BGM / 水印之前的那份；对白轨单独混
		pkg := &packageFiles{MasterPath: merged}
		if srtPath != "" {
			if b, err := os.ReadFile(srtPath); err == nil {
				pkg.SRT = string(b)
			}
		}
		if vp, err := renderVoiceTrack(ctx, dir, voiceParts); err != nil {
			warning = strings.TrimSpace(warning + " 对白轨未能生成：" + err.Error())
			res.Warning = warning
		} else {
			pkg.VoicePath = vp
		}
		res.Package = pkg
	}
	return res, nil
}

// voiceTrackPart 对白轨的一段：该镜的配音文件（可空）与该镜最终时长。
type voiceTrackPart struct {
	Path   string
	Dur    float64
	Volume float64
}

// renderVoiceTrack 把各镜配音按时间线位置铺成一条独立对白轨（延后 0.15 秒与混音一致，没配音的镜补静音）。
// 没有任何配音时返回空路径、无错误。
func renderVoiceTrack(ctx context.Context, dir string, parts []voiceTrackPart) (string, error) {
	has := false
	for _, p := range parts {
		if p.Path != "" {
			has = true
			break
		}
	}
	if !has {
		return "", nil
	}
	args := []string{"-y"}
	var fc strings.Builder
	labels := make([]string, 0, len(parts))
	idx := 0
	for i, p := range parts {
		dur := fmt.Sprintf("%.3f", p.Dur)
		if p.Path != "" {
			args = append(args, "-i", p.Path)
			vol := p.Volume
			if vol <= 0 {
				vol = 1
			}
			fmt.Fprintf(&fc, "[%d:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,adelay=150|150,volume=%.2f,apad=whole_dur=%s,atrim=0:%s,asetpts=PTS-STARTPTS[v%d];", idx, vol, dur, dur, i)
			idx++
		} else {
			fmt.Fprintf(&fc, "anullsrc=channel_layout=stereo:sample_rate=48000,atrim=0:%s,asetpts=PTS-STARTPTS[v%d];", dur, i)
		}
		labels = append(labels, fmt.Sprintf("[v%d]", i))
	}
	fmt.Fprintf(&fc, "%sconcat=n=%d:v=0:a=1[out]", strings.Join(labels, ""), len(parts))
	out := filepath.Join(dir, "voice-track.m4a")
	args = append(args, "-filter_complex", fc.String(), "-map", "[out]", "-c:a", "aac", "-b:a", "160k", out)
	if o, err := RunFFmpeg(ctx, args...); err != nil {
		return "", FFmpegError(o, err)
	}
	return out, nil
}

// resolveBgm 区分「未设置」与「明确为 0」（复审 D07）：音量负数（未设置）回落 0.2；0 表示静音，直接不混 BGM（返回空地址）。
func resolveBgm(url string, volume float64) (string, float64) {
	if volume < 0 {
		volume = 0.2
	}
	if volume == 0 {
		return "", 0
	}
	return url, volume
}

func assetTitle(cv *model.Canvas) string {
	if cv == nil || cv.Name == "" {
		return "成片"
	}
	return cv.Name
}

// finalPassArgs 生成最后一次 ffmpeg 参数；返回是否用了字幕 / 水印滤镜。
func finalPassArgs(in, out, srt, bgm string, bgmVolume float64, watermark bool, total float64, outW, outH int, enc []string) ([]string, bool) {
	if enc == nil {
		enc = encodeArgs(false)
	}
	args := []string{"-y", "-i", in}
	if bgm != "" {
		args = append(args, "-stream_loop", "-1", "-i", bgm)
	}
	vf := []string{}
	// 字体：setting 指定优先，否则自动找系统里的中日韩字体（compose_font.go）
	fontFile, fontName := ComposeFont()
	if srt != "" {
		style := fmt.Sprintf("FontSize=%d,Outline=1,Shadow=0,MarginV=36,PrimaryColour=&H00FFFFFF,OutlineColour=&H80000000", subtitleFontSize(outW, outH))
		if fontName != "" {
			style = "FontName=" + fontName + "," + style
		}
		sub := "subtitles=" + ffEscape(srt)
		// fontsdir 让 libass 直接从字体所在目录加载，不依赖服务器是否跑过 fc-cache
		if dir := fontsDirOf(fontFile); dir != "" {
			sub += ":fontsdir=" + ffEscape(dir)
		}
		vf = append(vf, sub+":force_style='"+style+"'")
	}
	if watermark {
		dt := "drawtext=text='EasyTV.ai':fontcolor=white@0.85:fontsize=28:x=w-tw-28:y=h-th-24:box=1:boxcolor=black@0.35:boxborderw=8"
		if fontFile != "" {
			dt += ":fontfile=" + ffEscape(fontFile)
		}
		vf = append(vf, dt)
	}
	useFilters := len(vf) > 0
	if bgm != "" {
		fade := total - 3
		if fade < 0 {
			fade = 0
		}
		// amix duration=first 让混音跟随成片音轨长度，不再用 -t 截断（避免尾帧被切）
		fc := fmt.Sprintf("[1:a]volume=%.2f,afade=t=out:st=%.1f:d=3[bg];[0:a][bg]amix=inputs=2:duration=first:dropout_transition=2:normalize=0[a]", bgmVolume, fade)
		if len(vf) > 0 {
			fc += ";[0:v]" + strings.Join(vf, ",") + "[v]"
			args = append(args, "-filter_complex", fc, "-map", "[v]", "-map", "[a]")
		} else {
			args = append(args, "-filter_complex", fc, "-map", "0:v", "-map", "[a]")
		}
	} else if len(vf) > 0 {
		args = append(args, "-vf", strings.Join(vf, ","), "-c:a", "copy")
	} else {
		args = append(args, "-c", "copy")
	}
	if bgm != "" || len(vf) > 0 {
		args = append(args, "-c:v", "libx264")
		args = append(args, enc...)
		args = append(args, "-pix_fmt", "yuv420p")
		if bgm != "" {
			args = append(args, "-c:a", "aac", "-b:a", "160k")
		}
	}
	args = append(args, "-movflags", "+faststart", out)
	return args, useFilters
}

// clipRender 单段转码参数。
type clipRender struct {
	// in 入点；seg 用户选定的画面段长（出点 - 入点），0 表示与 dur 相同；dur 输出段长（配音更长时 > seg，末帧冻结补齐）
	in, seg, dur      float64
	hasAudio          bool
	voice             string
	origVol, voiceVol float64
	// enc 编码参数（preset / crf），nil 用正式导出档
	enc []string
}

// normalizeClip 统一分辨率（等比缩放 + 黑边）、30fps、AAC 立体声；无音轨的补静音。
// 先把画面与原声严格截到 [in, in+seg]，再对截断后的画面 tpad 冻结末帧到 dur（复审 D01）：
// 不管配音多长，原出点之后的素材都不会出现。voice 非空时混入配音（配音延后 0.15 秒）。
func normalizeClip(ctx context.Context, src, dst string, w, h int, r clipRender) error {
	seg := r.seg
	if seg <= 0 || seg > r.dur {
		seg = r.dur
	}
	scale := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=30,setsar=1,format=yuv420p", w, h, w, h)
	// -ss 放在输入前时输出时间戳从 0 起算，所以 trim 只需要 duration
	vtrim := fmt.Sprintf("trim=duration=%.2f,setpts=PTS-STARTPTS,", seg)
	atrim := fmt.Sprintf("atrim=duration=%.2f,asetpts=PTS-STARTPTS,", seg)
	args := []string{"-y"}
	if r.in > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.2f", r.in))
	}
	args = append(args, "-i", src)
	silence := "anullsrc=channel_layout=stereo:sample_rate=48000"
	if r.voice == "" {
		orig := "[0:a]"
		if !r.hasAudio {
			args = append(args, "-f", "lavfi", "-i", silence)
			orig = "[1:a]"
		}
		fc := fmt.Sprintf("[0:v]%s%s,tpad=stop_mode=clone:stop_duration=10[v];%s%saresample=48000,aformat=channel_layouts=stereo,volume=%.2f,apad[a]", vtrim, scale, orig, atrim, r.origVol)
		args = append(args, "-filter_complex", fc, "-map", "[v]", "-map", "[a]")
	} else {
		args = append(args, "-i", r.voice)
		orig := "[0:a]"
		if !r.hasAudio {
			args = append(args, "-f", "lavfi", "-i", silence)
			orig = "[2:a]"
		}
		fc := fmt.Sprintf("[0:v]%s%s,tpad=stop_mode=clone:stop_duration=10[v];"+
			"%s%saresample=48000,aformat=channel_layouts=stereo,volume=%.2f,apad[o];"+
			"[1:a]aresample=48000,aformat=channel_layouts=stereo,adelay=150|150,volume=%.2f,apad[vo];"+
			"[o][vo]amix=inputs=2:duration=longest:normalize=0[a]", vtrim, scale, orig, atrim, r.origVol, r.voiceVol)
		args = append(args, "-filter_complex", fc, "-map", "[v]", "-map", "[a]")
	}
	enc := r.enc
	if enc == nil {
		enc = encodeArgs(false)
	}
	args = append(args, "-t", fmt.Sprintf("%.2f", r.dur), "-r", "30", "-c:v", "libx264")
	args = append(args, enc...)
	args = append(args,
		"-c:a", "aac", "-ar", "48000", "-ac", "2", "-b:a", "128k", "-movflags", "+faststart", dst)
	if out, err := RunFFmpeg(ctx, args...); err != nil {
		return FFmpegError(out, err)
	}
	return nil
}

// trimRange 入出点钳到素材时长内：返回入点与段长，段长至少 0.3 秒；out=0 表示到结尾。
func trimRange(in, out, dur float64) (float64, float64) {
	if in < 0 || in >= dur-0.3 {
		in = 0
	}
	end := dur
	if out > in+0.3 && out < dur {
		end = out
	}
	return in, end - in
}

// encodeArgs x264 编码档：正式导出 veryfast/crf 20；预览 ultrafast/crf 30（体积小、出得快，只看内容不看画质）。
func encodeArgs(preview bool) []string {
	if preview {
		return []string{"-preset", "ultrafast", "-crf", "30"}
	}
	return []string{"-preset", "veryfast", "-crf", "20"}
}

// composePreviewSize 低清试听的输出尺寸（480p 档）。
func composePreviewSize(aspect string) (int, int) {
	switch aspect {
	case "9:16":
		return 480, 854
	case "1:1":
		return 480, 480
	}
	return 854, 480
}

// composeOutputSize 画幅 × 分辨率档位 → 输出宽高（偶数）。
func composeOutputSize(aspect, resolution string) (int, int) {
	long, short := 1280, 720
	if resolution == model.TimelineRes1080 {
		long, short = 1920, 1080
	}
	switch aspect {
	case "9:16":
		return short, long
	case "1:1":
		if resolution == model.TimelineRes1080 {
			return 1080, 1080
		}
		return 960, 960
	}
	return long, short
}

// ComposeQC 合成后质检结果（API.md 12.3）。
type ComposeQC struct {
	ExpectedDuration float64  `json:"expected_duration"`
	Duration         float64  `json:"duration"`
	Width            int      `json:"width"`
	Height           int      `json:"height"`
	HasAudio         bool     `json:"has_audio"`
	Issues           []string `json:"issues"`
}

func checkComposeOutput(ctx context.Context, path string, expected float64, w, h int) ComposeQC {
	qc := ComposeQC{ExpectedDuration: math.Round(expected*100) / 100, Issues: []string{}}
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_type,width,height", "-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		qc.Issues = append(qc.Issues, "成片无法读取")
		return qc
	}
	var r struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	_ = json.Unmarshal(out, &r)
	qc.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	qc.Duration = math.Round(qc.Duration*100) / 100
	for _, s := range r.Streams {
		switch s.CodecType {
		case "video":
			qc.Width, qc.Height = s.Width, s.Height
		case "audio":
			qc.HasAudio = true
		}
	}
	if math.Abs(qc.Duration-expected) > 0.6 {
		qc.Issues = append(qc.Issues, fmt.Sprintf("成片时长 %.1f 秒与预期 %.1f 秒不符", qc.Duration, expected))
	}
	if qc.Width != w || qc.Height != h {
		qc.Issues = append(qc.Issues, fmt.Sprintf("成片分辨率 %dx%d 与设置 %dx%d 不符", qc.Width, qc.Height, w, h))
	}
	if !qc.HasAudio {
		qc.Issues = append(qc.Issues, "成片没有音轨")
	}
	return qc
}

// clipDurationWithVoice 配音（含 0.15 秒延后与 0.3 秒收尾）比画面长时，这一段延长到配音结束，最多延长 10 秒。
func clipDurationWithVoice(videoDur, voiceDur float64) float64 {
	need := voiceDur + 0.45
	if voiceDur <= 0 || need <= videoDur {
		return videoDur
	}
	if need > videoDur+10 {
		return videoDur + 10
	}
	return need
}

// BuildSRT 每段台词铺满该段时长（导出供测试）。字幕两端内缩 srtInset，短片段按比例缩小，保证 start < end（复审 D06）。
func BuildSRT(clips []model.ComposeClip) string {
	var b strings.Builder
	t := 0.0
	n := 0
	for _, c := range clips {
		switch {
		case len(c.Cues) > 0 && c.Duration > 0:
			// 逐条字幕：起止跟着配音各段走（已含 0.15 秒延后），不超出本镜
			for _, cue := range c.Cues {
				text := strings.TrimSpace(cue.Text)
				start := t + cue.Start
				end := start + cue.Dur
				if text == "" || start >= t+c.Duration || cue.Dur <= 0 {
					continue
				}
				if end > t+c.Duration {
					end = t + c.Duration
				}
				if end <= start {
					continue
				}
				n++
				fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtTime(start), srtTime(end), text)
			}
		case strings.TrimSpace(c.Text) != "" && c.Duration > 0:
			n++
			start, end := srtCueRange(t, c.Duration)
			fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtTime(start), srtTime(end), strings.TrimSpace(c.Text))
		}
		t += c.Duration
	}
	return b.String()
}

// srtCueRange 一段字幕的起止：内缩量取 min(0.2 秒, 时长的 10%)；内缩后仍不满足 start < end 时整段铺满。
func srtCueRange(t, dur float64) (float64, float64) {
	inset := math.Min(0.2, dur*0.1)
	start, end := t+inset, t+dur-inset
	if end <= start {
		return t, t + dur
	}
	return start, end
}

func srtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(sec*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// subtitleFontSize 字幕字号。SRT 经 libass 渲染时字号按 288 高的虚拟画布换算，固定 22 在 1280×720 上约 55 像素，
// 但竖屏 1920 高会被放大近三倍、一句话折成三行。这里让字高约为画面宽度的 4.3%，再换算回虚拟画布。
func subtitleFontSize(w, h int) int {
	if w <= 0 || h <= 0 {
		return 22
	}
	k := 0.043
	if h > w {
		k = 0.05 // 竖屏手机上看，字稍大一点，20 字左右仍能一行放下
	}
	size := int(math.Round(float64(w) * k * 288 / float64(h)))
	if size < 6 {
		size = 6
	}
	return size
}

// ffEscape 转义滤镜参数里的路径特殊字符。
func ffEscape(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`, `,`, `\,`, `[`, `\[`, `]`, `\]`)
	return r.Replace(p)
}

func extOf(url, def string) string {
	if i := strings.LastIndex(url, "."); i > 0 && len(url)-i <= 5 && !strings.Contains(url[i:], "/") {
		return url[i:]
	}
	return def
}

// createTimelineComposeJob 按时间线建合成任务。
func createTimelineComposeJob(userId, teamId int, cv *model.Canvas, req ComposeRequest) (*model.ComposeJob, error) {
	tl := *req.Timeline
	tl.Normalize()
	tl.Lang = NormalizeLang(tl.Lang)
	clips, err := clipsFromTimeline(cv, &tl)
	if err != nil {
		return nil, err
	}
	if tl.BgmAssetId > 0 {
		a, err := model.GetUserAsset(userId, tl.BgmAssetId)
		if err != nil || a.Type != model.AssetTypeAudio {
			return nil, errors.New("BGM 需要是你资产库里的音频")
		}
	}
	applyVoiceChoice(clips, tl.Voice, tl.Lang)
	// 整份字幕：请求里直接给的 SRT 优先；其次时间线指定的字幕节点（语音转字幕产物）；都没有则逐镜台词
	subtitleText := strings.TrimSpace(req.SubtitleText)
	if subtitleText == "" && tl.Subtitles && tl.SubtitleNodeId != "" {
		srt, err := subtitleFromGraphNode(cv.Graph, tl.SubtitleNodeId)
		if err != nil {
			return nil, err
		}
		subtitleText = srt
	}
	screenplayId := req.ScreenplayId
	if sp, err := model.GetScreenplayByCanvas(userId, cv.Id); err == nil && screenplayId == 0 {
		screenplayId = sp.Id
	}
	watermark := setting.GetGrowthConfig().ComposeWatermark && !IsMemberForPricing(userId, teamId)
	b, _ := json.Marshal(clips)
	job := &model.ComposeJob{
		UserId: userId, TeamId: teamId, CanvasId: cv.Id, ScreenplayId: screenplayId, Clips: string(b),
		Subtitles: tl.Subtitles, BgmAssetId: tl.BgmAssetId, BgmVolume: tl.BgmVolume, Aspect: tl.Aspect, Resolution: tl.Resolution,
		Watermark: watermark, Lang: tl.Lang, Status: model.ComposeStatusQueued, Preview: req.Preview, Package: req.Package && !req.Preview,
		SubtitleText: truncate(subtitleText, 20000),
		WorkerTag:    common.WorkerTag(),
	}
	if err := job.Insert(); err != nil {
		return nil, err
	}
	job.FillView()
	Track(userId, "compose_start", map[string]any{"job_id": job.Id, "canvas_id": cv.Id, "clips": len(clips), "timeline": true, "resolution": tl.Resolution, "subtitle_node": tl.SubtitleNodeId != ""})
	return job, nil
}

// clipsFromTimeline 时间线里启用的镜头 → 合成片段。镜头视频可以换成候选（必须是自己的视频资产），
// 配音按 voice_mode 混入 / 覆盖原声 / 不要；字幕优先逐镜字幕，其次该语种台词，最后节点台词。
func clipsFromTimeline(cv *model.Canvas, tl *model.Timeline) ([]model.ComposeClip, error) {
	g, err := parseComposeGraph(cv)
	if err != nil {
		return nil, err
	}
	byId := map[string]composeGraphNode{}
	for _, n := range g.Nodes {
		byId[n.Id] = n
	}
	voiceAssets := voiceAssetsByVideo(g.Nodes, g.Edges)
	linesByShot := map[int]string{}
	dialogueByShot := map[int][]model.PlanLine{}
	if sp, err := model.GetScreenplayByCanvas(cv.UserId, cv.Id); err == nil {
		if plan, err := sp.ParsePlan(); err == nil {
			for _, s := range plan.Shots {
				if tl.Lang != "" && tl.Lang != "zh" {
					linesByShot[s.No] = LineForLang(s, tl.Lang)
				}
				if len(s.Dialogue) > 0 {
					dialogueByShot[s.No] = s.Dialogue
				}
			}
		}
	}
	enabled := 0
	for _, c := range tl.Clips {
		if c.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return nil, errors.New("时间线里没有启用的镜头")
	}
	if enabled > ComposeMaxClips {
		return nil, fmt.Errorf("一次最多合成 %d 个镜头，时间线启用了 %d 个", ComposeMaxClips, enabled)
	}
	clips := make([]model.ComposeClip, 0, enabled)
	var broken []string
	for _, c := range tl.Clips {
		if !c.Enabled {
			continue
		}
		n, ok := byId[c.NodeId]
		if !ok || nodeKind(n) != "video" {
			broken = append(broken, c.NodeId)
			continue
		}
		assetId := c.AssetId
		if assetId == 0 {
			assetId = toInt(n.Data["assetId"])
		}
		asset, err := canvasAsset(cv, assetId)
		if assetId <= 0 || err != nil || asset.Type != model.AssetTypeVideo || !isFetchableUrl(asset.Url) {
			broken = append(broken, c.NodeId)
			continue
		}
		clip := model.ComposeClip{ClipId: c.Id, NodeId: c.NodeId, AssetId: asset.Id, Url: asset.Url, Duration: asset.Duration, In: c.In, Out: c.Out}
		if clip.Duration <= 0 {
			clip.Duration = toFloat(n.Data["seconds"])
		}
		line, _ := n.Data["line"].(string)
		if v := linesByShot[int(nodeShot(n))]; v != "" {
			line = v
		}
		clip.Text = line
		if c.Subtitle != nil {
			clip.Text = *c.Subtitle
		}
		orig, voiceVol := c.OrigVolume, c.VoiceVolume
		if c.VoiceMode != model.TimelineVoiceOff {
			if voiceId := voiceAssets[c.NodeId]; voiceId > 0 {
				if a, err := canvasAsset(cv, voiceId); err == nil && a.Type == model.AssetTypeAudio && isFetchableUrl(a.Url) {
					clip.VoiceAssetId, clip.VoiceUrl = a.Id, a.Url
					// 分段配音（API.md 25.1）：字幕按对白条出；用户手改了逐镜字幕就仍整镜一句
					if c.Subtitle == nil {
						clip.Cues = voiceCuesForClip(a, dialogueByShot[int(nodeShot(n))], tl.Lang)
					}
				}
			}
		}
		if c.VoiceMode == model.TimelineVoiceOnly && clip.VoiceUrl != "" {
			orig = 0
		}
		clip.OrigVolume, clip.VoiceVolume = &orig, &voiceVol
		clips = append(clips, clip)
	}
	if len(broken) > 0 {
		Track(cv.UserId, model.TrackEventRefMissing, map[string]any{"source": "compose_clip", "count": len(broken), "canvas_id": cv.Id})
		return nil, fmt.Errorf("有 %d 个镜头的视频不可用（已删除或地址失效）：%s，请重新生成、换候选或取消勾选", len(broken), strings.Join(broken, "、"))
	}
	return clips, nil
}

// applyVoiceChoice 用户关了配音就全部去掉；配音语种与成片语言不一致（中文配音配英文成片）的不混，标 VoiceSkipped。
func applyVoiceChoice(clips []model.ComposeClip, enabled bool, lang string) {
	for i := range clips {
		if clips[i].VoiceAssetId == 0 {
			continue
		}
		if !enabled {
			clips[i].VoiceAssetId, clips[i].VoiceUrl = 0, ""
			continue
		}
		if a, err := model.GetAssetByIdUnscoped(clips[i].VoiceAssetId); err == nil && a.TaskId > 0 {
			if t, err := model.GetTaskById(a.TaskId); err == nil && !sameLang(t.Lang, lang) {
				clips[i].VoiceAssetId, clips[i].VoiceUrl, clips[i].VoiceSkipped = 0, "", true
			}
		}
	}
}

// sameLang 空串与 zh 都算中文。
func sameLang(a, b string) bool {
	norm := func(v string) string {
		if v == "" {
			return "zh"
		}
		return v
	}
	return norm(a) == norm(b)
}

// shotNoOfNode 从 video-3 这类节点 id 里取镜号。
func shotNoOfNode(nodeId string) int {
	i := strings.LastIndex(nodeId, "-")
	if i < 0 || i == len(nodeId)-1 {
		return 0
	}
	n, err := strconv.Atoi(nodeId[i+1:])
	if err != nil {
		return 0
	}
	return n
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// voiceCuesForClip 从配音资产对应任务的 result_text 取各段起止，换成成片字幕条：
// 起点加上混音时的 0.15 秒延后；成片语种非中文时按序号换成对白译文（没有译文的条目保留原文）。
func voiceCuesForClip(voice *model.Asset, dialogue []model.PlanLine, lang string) []model.ComposeCue {
	if voice == nil || voice.TaskId <= 0 {
		return nil
	}
	t, err := model.GetTaskById(voice.TaskId)
	if err != nil || strings.TrimSpace(t.ResultText) == "" {
		return nil
	}
	var res model.VoiceCuesResult
	if json.Unmarshal([]byte(t.ResultText), &res) != nil || len(res.Cues) == 0 {
		return nil
	}
	out := make([]model.ComposeCue, 0, len(res.Cues))
	for i, cue := range res.Cues {
		text := cue.Text
		if lang != "" && lang != "zh" && i < len(dialogue) {
			if v := strings.TrimSpace(dialogue[i].Lines[lang]); v != "" {
				text = v
			}
		}
		out = append(out, model.ComposeCue{Start: cue.Start + 0.15, Dur: cue.Dur, Text: text})
	}
	return out
}

// TimelineEstimate 时间线的预计时长（API.md 25.4）：按入出点与配音延长算，和真正合成同一套规则。
type TimelineEstimate struct {
	Total float64                `json:"total"`
	Clips []TimelineClipEstimate `json:"clips"`
}

type TimelineClipEstimate struct {
	Id       string  `json:"id"`
	NodeId   string  `json:"node_id"`
	Duration float64 `json:"duration"`
	// VoiceExtended 配音比画面长、被自动延长的秒数
	VoiceExtended float64 `json:"voice_extended"`
}

// EstimateTimeline 只读估算，素材缺失的片段按 0 计。
func EstimateTimeline(cv *model.Canvas, tl *model.Timeline) TimelineEstimate {
	est := TimelineEstimate{Clips: []TimelineClipEstimate{}}
	g, err := parseComposeGraph(cv)
	if err != nil {
		return est
	}
	byId := map[string]composeGraphNode{}
	for _, n := range g.Nodes {
		byId[n.Id] = n
	}
	voiceAssets := voiceAssetsByVideo(g.Nodes, g.Edges)
	for _, c := range tl.Clips {
		if !c.Enabled {
			continue
		}
		item := TimelineClipEstimate{Id: c.Id, NodeId: c.NodeId}
		n, ok := byId[c.NodeId]
		if ok {
			assetId := c.AssetId
			if assetId == 0 {
				assetId = toInt(n.Data["assetId"])
			}
			dur := 0.0
			if a, err := canvasAsset(cv, assetId); err == nil {
				dur = a.Duration
			}
			if dur <= 0 {
				dur = toFloat(n.Data["seconds"])
			}
			if dur > 0 {
				_, seg := trimRange(c.In, c.Out, dur)
				item.Duration = seg
				if tl.Voice && c.VoiceMode != model.TimelineVoiceOff {
					if voiceId := voiceAssets[c.NodeId]; voiceId > 0 {
						if a, err := canvasAsset(cv, voiceId); err == nil && a.Duration > 0 {
							ext := clipDurationWithVoice(seg, a.Duration)
							item.VoiceExtended = math.Round((ext-seg)*100) / 100
							item.Duration = ext
						}
					}
				}
			}
		}
		item.Duration = math.Round(item.Duration*100) / 100
		est.Total += item.Duration
		est.Clips = append(est.Clips, item)
	}
	est.Total = math.Round(est.Total*100) / 100
	return est
}

// composeAssetName 成片资产名：预览带前缀，资产库里一眼能分开。
func composeAssetName(j *model.ComposeJob) string {
	if j.Preview {
		return fmt.Sprintf("预览 #%d.mp4", j.Id)
	}
	return fmt.Sprintf("成片 #%d.mp4", j.Id)
}

// ComposePackage 交付包（API.md 26.2）：各文件的资产 id 与地址。
type ComposePackage struct {
	MasterAssetId   int    `json:"master_asset_id"`
	MasterUrl       string `json:"master_url"`
	SrtAssetId      int    `json:"srt_asset_id"`
	SrtUrl          string `json:"srt_url"`
	VoiceAssetId    int    `json:"voice_asset_id"`
	VoiceUrl        string `json:"voice_url"`
	ManifestAssetId int    `json:"manifest_asset_id"`
	ManifestUrl     string `json:"manifest_url"`
}

// PackageManifest 素材清单：另一位剪辑师拿到就能定位每个镜头、替换一句台词并重出字幕版。
type PackageManifest struct {
	Title       string            `json:"title"`
	ComposeId   int               `json:"compose_id"`
	CanvasId    int               `json:"canvas_id"`
	Aspect      string            `json:"aspect"`
	Resolution  string            `json:"resolution"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	Fps         int               `json:"fps"`
	Duration    float64           `json:"duration"`
	Lang        string            `json:"lang"`
	Clips       []PackageClip     `json:"clips"`
	Files       map[string]string `json:"files"`
	GeneratedAt int64             `json:"generated_at"`
}

type PackageClip struct {
	Seq          int     `json:"seq"`
	ShotNo       int     `json:"shot_no"`
	NodeId       string  `json:"node_id"`
	ClipId       string  `json:"clip_id,omitempty"`
	AssetId      int     `json:"asset_id"`
	TaskId       int     `json:"task_id,omitempty"`
	Model        string  `json:"model,omitempty"`
	In           float64 `json:"in"`
	Out          float64 `json:"out"`
	Duration     float64 `json:"duration"`
	VoiceAssetId int     `json:"voice_asset_id,omitempty"`
	Subtitle     string  `json:"subtitle"`
}

// uploadComposePackage 把母版 / SRT / 对白轨 / 清单存成资产；任一失败只写 warning，不影响成片。
func uploadComposePackage(j *model.ComposeJob, cv *model.Canvas, clips []model.ComposeClip, res *renderResult, projectId int, finalUrl string) (ComposePackage, string) {
	pkg := ComposePackage{}
	var warns []string
	save := func(path, name, mime, typ string, dur float64) (*model.Asset, error) {
		if path == "" {
			return nil, nil
		}
		return SaveAssetFromFile(path, AssetDraft{
			UserId: j.UserId, TeamId: j.TeamId, ProjectId: projectId, Type: typ, Name: name, Mime: mime,
			Source: model.AssetSourceCompose, Duration: dur, Width: res.Width, Height: res.Height,
		})
	}
	if a, err := save(res.Package.MasterPath, fmt.Sprintf("母版（无字幕） #%d.mp4", j.Id), "video/mp4", model.AssetTypeVideo, res.Total); err != nil {
		warns = append(warns, "母版保存失败")
	} else if a != nil {
		pkg.MasterAssetId, pkg.MasterUrl = a.Id, a.Url
		go EnsureAssetCover(a)
	}
	if res.Package.SRT != "" {
		p := filepath.Join(filepath.Dir(res.Path), "package.srt")
		if err := os.WriteFile(p, []byte(res.Package.SRT), 0o600); err == nil {
			if a, err := save(p, fmt.Sprintf("字幕 #%d.srt", j.Id), "application/x-subrip", model.AssetTypeText, 0); err != nil {
				warns = append(warns, "SRT 保存失败")
			} else if a != nil {
				pkg.SrtAssetId, pkg.SrtUrl = a.Id, a.Url
			}
		}
	}
	if res.Package.VoicePath != "" {
		if a, err := save(res.Package.VoicePath, fmt.Sprintf("对白轨 #%d.m4a", j.Id), "audio/mp4", model.AssetTypeAudio, res.Total); err != nil {
			warns = append(warns, "对白轨保存失败")
		} else if a != nil {
			pkg.VoiceAssetId, pkg.VoiceUrl = a.Id, a.Url
		}
	}
	manifest := PackageManifest{
		Title: assetTitle(cv), ComposeId: j.Id, CanvasId: j.CanvasId, Aspect: j.Aspect, Resolution: j.Resolution,
		Width: res.Width, Height: res.Height, Fps: 30, Duration: res.Total, Lang: j.Lang, GeneratedAt: common.GetTimestamp(),
		Files: map[string]string{"final": finalUrl, "master": pkg.MasterUrl, "srt": pkg.SrtUrl, "voice_track": pkg.VoiceUrl},
	}
	for i, c := range clips {
		pc := PackageClip{Seq: i + 1, ShotNo: shotNoOfNode(c.NodeId), NodeId: c.NodeId, ClipId: c.ClipId, AssetId: c.AssetId,
			In: c.In, Out: c.Out, Duration: c.Duration, VoiceAssetId: c.VoiceAssetId, Subtitle: c.Text}
		if a, err := model.GetAssetByIdUnscoped(c.AssetId); err == nil && a.TaskId > 0 {
			pc.TaskId = a.TaskId
			if t, err := model.GetTaskById(a.TaskId); err == nil {
				pc.Model = t.Model
			}
		}
		manifest.Clips = append(manifest.Clips, pc)
	}
	if b, err := json.MarshalIndent(manifest, "", "  "); err == nil {
		p := filepath.Join(filepath.Dir(res.Path), "manifest.json")
		if err := os.WriteFile(p, b, 0o600); err == nil {
			if a, err := save(p, fmt.Sprintf("素材清单 #%d.json", j.Id), "application/json", model.AssetTypeText, 0); err != nil {
				warns = append(warns, "素材清单保存失败")
			} else if a != nil {
				pkg.ManifestAssetId, pkg.ManifestUrl = a.Id, a.Url
			}
		}
	}
	return pkg, strings.Join(warns, "；")
}

// subtitleFromGraphNode 从画布节点取整份 SRT（API.md 35.4 字幕来源）：节点得是文本节点、正文得是 SRT（有 "-->" 时间轴）。
func subtitleFromGraphNode(graph, nodeId string) (string, error) {
	var g struct {
		Nodes []composeGraphNode `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(graph), &g); err != nil {
		return "", errors.New("画布内容无法解析")
	}
	for _, n := range g.Nodes {
		if n.Id != nodeId {
			continue
		}
		kind, _ := n.Data["kind"].(string)
		content, _ := n.Data["content"].(string)
		content = strings.TrimSpace(content)
		if kind != "text" || content == "" {
			return "", errors.New("选中的字幕节点不是文本节点或内容为空，请重新选择字幕来源")
		}
		if !strings.Contains(content, "-->") {
			return "", errors.New("选中的字幕节点里不是 SRT 字幕（缺少时间轴），请选语音转字幕产出的节点")
		}
		return content, nil
	}
	return "", errors.New("字幕来源节点已不在画布上，请重新选择")
}
