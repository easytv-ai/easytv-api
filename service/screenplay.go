package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 成片工作室（T23）：策划案生成、积分预估、按策划案建画布。

// TextChatter 能同步跑一次文本对话的 provider（EasyAPIProvider 满足）。
type TextChatter interface {
	Chat(modelName, prompt string) (string, error)
}

const defaultPlanPrompt = `你是短视频导演助手。根据用户给的一句话或剧本，输出一份可直接生成的短片策划案，严格只输出 JSON，不要任何解释或 Markdown 代码块。
JSON 结构：
{"title":"片名","style":"整体风格与色调（一句话）","aspect":"16:9 或 9:16",
 "characters":[{"key":"c1","name":"角色名","look":"用于文生图的外貌、服装、气质描述"}],
 "scenes":[{"key":"s1","name":"场景名","look":"用于文生图的环境、光线、氛围描述"}],
 "shots":[{"no":1,"scene":"s1","characters":["c1"],"prompt":"这一镜的画面描述（含构图与情绪）","motion":"镜头运动，如 缓慢推近","line":"旁白，可为空","dialogue":[{"speaker":"说话人物的 key，旁白用 narrator","text":"一句台词","emotion":"情绪"}],"seconds":5}]}
说明：有人物说话的镜用 dialogue 按句列出（谁说、说什么），一镜里可以有多句；只有旁白时 dialogue 留空数组、旁白写在 line。
要求：角色不超过 %d 个，场景不超过 %d 个，镜头 %d 到 %d 个，总时长约 %d 秒，每镜 3 到 10 秒；prompt 用中文，具体、可视化、避免抽象词；风格要求：%s。`

// PickDefaultModel 按类型取第一个生效且已定价的模型（管理端排序）。
func PickDefaultModel(taskType string) string {
	prices, _ := model.GetModelPrices(true)
	for _, p := range prices {
		if p.Type == taskType && FindProvider(taskType, p.Model) != nil {
			return p.Model
		}
	}
	return ""
}

// PickUpscaleModel 超分模型（F4 超分 / API.md 23「高清」）：已定价的视频模型里带 upscale 能力的那个。
// 能力来自 model_price.abilities，没打标签时按模型名推断（upscale / 超分），与旧行为一致。
func PickUpscaleModel() string {
	for _, name := range PickModelsByAbility(model.AbilityUpscale) {
		if p, err := model.GetModelPrice(name); err == nil && p.Type == model.TaskTypeVideo {
			return name
		}
	}
	return ""
}

// planModel 策划案用的文本模型：setting 指定优先。
func planModel() string {
	if m := strings.TrimSpace(setting.GetSettingString(setting.ComposePlanModel)); m != "" && FindProvider(model.TaskTypeText, m) != nil {
		return m
	}
	return PickDefaultModel(model.TaskTypeText)
}

// GenerateScreenplay 同步生成策划案并扣分（成功才扣）。teamId 为请求空间（API.md 20）：团队空间扣团队积分池。
// GenerateScreenplay 生成策划案；bibleCtx 非空时把剧集设定附在用户输入前（API.md 27.3），episodeNo 记集号。
func GenerateScreenplay(userId, teamId int, input string, targetSeconds int, style string, projectId int) (*model.Screenplay, error) {
	return GenerateScreenplayWithContext(userId, teamId, input, targetSeconds, style, projectId, "", 0)
}

func GenerateScreenplayWithContext(userId, teamId int, input string, targetSeconds int, style string, projectId int, bibleCtx string, episodeNo int) (*model.Screenplay, error) {
	modelName := planModel()
	if modelName == "" {
		return nil, errors.New("暂无可用的文本模型")
	}
	provider := FindProvider(model.TaskTypeText, modelName)
	chatter, ok := provider.(TextChatter)
	if !ok {
		return nil, errors.New("当前网关不支持策划案生成")
	}
	if err := CanUseTeamPool(userId, teamId); err != nil {
		return nil, err
	}
	payer, err := PayerForTeam(userId, teamId)
	if err != nil {
		return nil, err
	}
	isMember := IsMemberForPricing(userId, teamId)
	points, _, err := EstimateTaskPoints(model.TaskTypeText, modelName, "{}", isMember)
	if err != nil {
		return nil, errors.New("文本模型未定价")
	}
	if points > 0 && model.GetPointsBalance(nil, payer) < points {
		return nil, fmt.Errorf("积分不足，生成策划案需要 %d 积分", points)
	}
	minShots, maxShots := shotRange(targetSeconds)
	styleText := style
	if styleText == "" {
		styleText = "由内容决定"
	}
	sys := strings.TrimSpace(setting.GetSettingString(setting.ComposePlanPrompt))
	if sys == "" {
		sys = fmt.Sprintf(defaultPlanPrompt, model.ScreenplayMaxCharacters, model.ScreenplayMaxScenes, minShots, maxShots, targetSeconds, styleText)
	}
	sp := &model.Screenplay{
		UserId: userId, ProjectId: projectId, Input: input, TargetSeconds: targetSeconds, Style: style,
		Model: modelName, Status: model.ScreenplayStatusGenerating, TaskNo: model.NewTaskNo(), EpisodeNo: episodeNo,
	}
	if err := sp.Insert(); err != nil {
		return nil, err
	}
	if bibleCtx != "" {
		input = "【剧集设定，请沿用人物 key、关系与道具归属，不要让不知情的角色泄密】\n" + bibleCtx + "\n\n【本集要拍的内容】\n" + input
	}
	raw, err := chatter.Chat(modelName, sys+"\n\n用户输入：\n"+input)
	if err != nil {
		_ = model.UpdateScreenplayFields(sp.Id, map[string]any{"status": model.ScreenplayStatusFailed, "error": truncate(err.Error(), 500)})
		return nil, fmt.Errorf("策划案生成失败：%s", err.Error())
	}
	plan, perr := ParsePlanText(raw)
	if perr == nil {
		perr = plan.Normalize(targetSeconds)
	}
	if perr != nil {
		// 解析失败 / 超限 / 引用错误重试一次，把原因告诉模型（超限不再静默截断，复审 D03）
		note := "上一次输出不是合法 JSON，请只输出 JSON。"
		if isPlanLimitErr(perr) {
			note = "上一次输出不符合要求（" + perr.Error() + "），请严格遵守数量上限，镜头只能引用已定义的人物与场景 key。"
		}
		raw2, err2 := chatter.Chat(modelName, sys+"\n\n注意："+note+"\n\n用户输入：\n"+input)
		if err2 == nil {
			if plan, perr = ParsePlanText(raw2); perr == nil {
				perr = plan.Normalize(targetSeconds)
			}
		}
	}
	if perr != nil {
		msg := "模型输出无法解析为策划案"
		if isPlanLimitErr(perr) {
			msg = "模型输出" + perr.Error()
		}
		_ = model.UpdateScreenplayFields(sp.Id, map[string]any{"status": model.ScreenplayStatusFailed, "error": truncate(msg, 500)})
		return nil, errors.New(msg + "，请换个说法再试")
	}
	if plan.Title == "" {
		plan.Title = truncate(input, 30)
	}
	planJSON, _ := json.Marshal(plan)
	// 扣分：成功后按文本模型单价结算一次
	if points > 0 {
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			return model.ConsumePoints(tx, payer, points, model.PointLogSettle, model.PointsBizTask, sp.TaskNo, "策划案生成 "+modelName, 0)
		})
		if err != nil {
			common.SysError(fmt.Sprintf("screenplay consume points failed user=%d: %v", userId, err))
			points = 0
		}
	}
	_ = model.UpdateScreenplayFields(sp.Id, map[string]any{
		"plan": string(planJSON), "status": model.ScreenplayStatusReady, "points_used": points,
	})
	sp.Plan, sp.Status, sp.PointsUsed = string(planJSON), model.ScreenplayStatusReady, points
	sp.FillView()
	Track(userId, "create_plan", map[string]any{"screenplay_id": sp.Id, "shots": len(plan.Shots), "points": points})
	return sp, nil
}

// isPlanLimitErr 超限或引用错误（原稿完整，不是 JSON 解析问题）。
func isPlanLimitErr(err error) bool {
	return errors.Is(err, model.ErrPlanLimit) || errors.Is(err, model.ErrPlanRef)
}

func shotRange(targetSeconds int) (int, int) {
	switch {
	case targetSeconds <= 15:
		return 2, 4
	case targetSeconds <= 30:
		return 4, 7
	default:
		return 6, model.ScreenplayMaxShots
	}
}

// ParsePlanText 从模型输出里取 JSON（容忍 ```json 代码块与前后废话）。
func ParsePlanText(raw string) (*model.Plan, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	p := &model.Plan{}
	if err := json.Unmarshal([]byte(s), p); err != nil {
		return nil, model.ErrPlanInvalid
	}
	return p, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ---- 预估与建画布 ----

// BuildModels 建画布用的三类模型；空取默认。
type BuildModels struct {
	Image string `json:"image_model"`
	Video string `json:"video_model"`
	Audio string `json:"audio_model"`
}

func (m *BuildModels) fill() {
	if m.Image == "" {
		m.Image = PickDefaultModel(model.TaskTypeImage)
	}
	if m.Video == "" {
		m.Video = PickDefaultModel(model.TaskTypeVideo)
	}
	if m.Audio == "" {
		m.Audio = PickDefaultModel(model.TaskTypeAudio)
	}
}

// PlanEstimate POST /user/screenplay/:id/estimate 响应。
type PlanEstimate struct {
	Characters int    `json:"characters"`
	ShotsImage int    `json:"shots_image"`
	ShotsVideo int    `json:"shots_video"`
	Voice      int    `json:"voice"`
	Total      int    `json:"total"`
	ImageModel string `json:"image_model"`
	VideoModel string `json:"video_model"`
	AudioModel string `json:"audio_model"`
}

// EstimatePlan 逐层按模型定价汇总（含促销价、会员价，会员价按 teamId 空间判断）。未定价的层记 0 并保留模型名为空。
func EstimatePlan(userId, teamId int, plan *model.Plan, m BuildModels) PlanEstimate {
	m.fill()
	isMember := IsMemberForPricing(userId, teamId)
	est := PlanEstimate{ImageModel: m.Image, VideoModel: m.Video, AudioModel: m.Audio}
	imgUnit := 0
	if m.Image != "" {
		imgUnit, _, _ = EstimateTaskPoints(model.TaskTypeImage, m.Image, `{"count":1}`, isMember)
	}
	est.Characters = imgUnit * len(plan.Characters)
	est.ShotsImage = imgUnit * len(plan.Shots)
	if m.Video != "" {
		for _, s := range plan.Shots {
			p, _, _ := EstimateTaskPoints(model.TaskTypeVideo, m.Video, fmt.Sprintf(`{"duration":%d,"count":1}`, s.Seconds), isMember)
			est.ShotsVideo += p
		}
	}
	if m.Audio != "" {
		unit, _, _ := EstimateTaskPoints(model.TaskTypeAudio, m.Audio, "{}", isMember)
		for _, s := range plan.Shots {
			if s.Line != "" {
				est.Voice += unit
			}
		}
	}
	est.Total = est.Characters + est.ShotsImage + est.ShotsVideo + est.Voice
	return est
}

// 画布节点尺寸与布局（与前端 CANVAS_STAGE_SIZE 一致）
const (
	nodeWText  = 252
	nodeWMedia = 320
	nodeH      = 220
	colGap     = 400
	rowGap     = 270
)

type graphNode struct {
	Id       string         `json:"id"`
	Type     string         `json:"type"`
	Position map[string]int `json:"position"`
	Width    int            `json:"width"`
	Height   int            `json:"height"`
	Style    map[string]any `json:"style"`
	Data     map[string]any `json:"data"`
}

type graphEdge struct {
	Id           string `json:"id"`
	Type         string `json:"type"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle"`
	TargetHandle string `json:"targetHandle"`
}

func newNode(id, kind, label, prompt, content string, x, y int, extra map[string]any) graphNode {
	w := nodeWMedia
	if kind == "text" || kind == "audio" {
		w = nodeWText
	}
	data := map[string]any{"kind": kind, "label": label, "content": content, "prompt": prompt, "mode": "idle"}
	for k, v := range extra {
		data[k] = v
	}
	return graphNode{
		Id: id, Type: "easytv", Position: map[string]int{"x": x, "y": y}, Width: w, Height: nodeH,
		Style: map[string]any{"width": fmt.Sprintf("%dpx", w), "height": fmt.Sprintf("%dpx", nodeH)}, Data: data,
	}
}

func newEdge(src, dst string) graphEdge {
	return graphEdge{Id: "e-" + src + "-" + dst, Type: "easytv", Source: src, Target: dst, SourceHandle: "out", TargetHandle: "in"}
}

// BuildPlanGraph 把策划案铺成画布：
// 第 1 列文本节点（整份策划案）；第 2 列角色图；第 3 列每镜分镜图（连角色图作参考）；第 4 列镜头视频（连分镜图作首帧）；第 5 列有台词的镜头配音。
func BuildPlanGraph(plan *model.Plan, m BuildModels) string {
	m.fill()
	nodes := []graphNode{}
	edges := []graphEdge{}
	nodes = append(nodes, newNode("text-1", "text", "策划案", "", planSummary(plan), 0, 0, map[string]any{"sp": map[string]any{"layer": "plan"}}))

	charNode := map[string]string{}
	imgIdx := 0
	for i, c := range plan.Characters {
		imgIdx++
		id := fmt.Sprintf("image-%d", imgIdx)
		charNode[c.Key] = id
		prompt := fmt.Sprintf("%s，%s，%s，角色三视图参考，干净背景", c.Name, c.Look, plan.Style)
		nodes = append(nodes, newNode(id, "image", "角色 · "+c.Name, prompt, "", colGap, i*rowGap,
			map[string]any{"model": m.Image, "sp": map[string]any{"layer": "character", "key": c.Key}}))
		edges = append(edges, newEdge("text-1", id))
	}
	sceneLook := map[string]string{}
	for _, s := range plan.Scenes {
		sceneLook[s.Key] = s.Look
	}
	audioIdx := 0
	for i, s := range plan.Shots {
		imgIdx++
		imgId := fmt.Sprintf("image-%d", imgIdx)
		imgPrompt, vidPrompt := shotPrompts(plan, s, sceneLook)
		nodes = append(nodes, newNode(imgId, "image", fmt.Sprintf("分镜 %d", s.No), imgPrompt, "", colGap*2, i*rowGap,
			map[string]any{"model": m.Image, "sp": map[string]any{"layer": "shot_image", "shot": s.No, "shot_id": s.Id, "seconds": s.Seconds}}))
		for _, ck := range s.Characters {
			if cid, ok := charNode[ck]; ok {
				edges = append(edges, newEdge(cid, imgId))
			}
		}
		vidId := fmt.Sprintf("video-%d", s.No)
		nodes = append(nodes, newNode(vidId, "video", fmt.Sprintf("镜头 %d", s.No), vidPrompt, "", colGap*3, i*rowGap,
			map[string]any{"model": m.Video, "refNodeId": imgId, "seconds": s.Seconds, "line": s.Line,
				"sp": map[string]any{"layer": "shot_video", "shot": s.No, "shot_id": s.Id, "seconds": s.Seconds}}))
		edges = append(edges, newEdge(imgId, vidId))
		if s.Line != "" {
			audioIdx++
			audId := fmt.Sprintf("audio-%d", audioIdx)
			nodes = append(nodes, newNode(audId, "audio", fmt.Sprintf("配音 %d", s.No), s.Line, "", colGap*4, i*rowGap,
				voiceNodeExtra(plan, s, m.Audio)))
			edges = append(edges, newEdge(vidId, audId))
		}
	}
	g := map[string]any{"nodes": nodes, "edges": edges, "viewport": map[string]any{"x": 40, "y": 40, "zoom": 0.6}}
	b, _ := json.Marshal(g)
	return string(b)
}

// BuildScreenplayCanvas 建项目 + 画布，回写 screenplay。新建的项目落在请求空间 teamId（API.md 20）。
func BuildScreenplayCanvas(userId, teamId int, sp *model.Screenplay, m BuildModels) (*model.Canvas, PlanEstimate, error) {
	plan, err := sp.ParsePlan()
	if err != nil {
		return nil, PlanEstimate{}, err
	}
	m.fill()
	if m.Image == "" || m.Video == "" {
		return nil, PlanEstimate{}, errors.New("图片或视频模型未配置")
	}
	est := EstimatePlan(userId, teamId, plan, m)
	name := plan.Title
	if name == "" {
		name = "未命名成片"
	}
	projectId := sp.ProjectId
	if projectId > 0 {
		if _, err := model.GetUserProject(userId, projectId); err != nil {
			projectId = 0
		}
	}
	if projectId == 0 {
		p := &model.Project{UserId: userId, TeamId: teamId, Name: name, Description: "成片工作室"}
		if err := p.Insert(); err != nil {
			return nil, est, err
		}
		projectId = p.Id
	}
	cv := &model.Canvas{UserId: userId, ProjectId: projectId, Name: name, Graph: BuildPlanGraph(plan, m)}
	if err := cv.Insert(); err != nil {
		return nil, est, err
	}
	model.TouchProject(projectId)
	// built_revision 记下画布基于哪一稿（复审 D02）
	_ = model.UpdateScreenplayFields(sp.Id, map[string]any{"status": model.ScreenplayStatusBuilt, "canvas_id": cv.Id, "project_id": projectId, "built_revision": sp.Revision})
	TrackOnce(userId, model.TrackEventFirstCanvas, map[string]any{"canvas_id": cv.Id, "from": "screenplay"})
	Track(userId, "create_build", map[string]any{"screenplay_id": sp.Id, "canvas_id": cv.Id, "estimate": est.Total})
	return cv, est, nil
}

// ---- 改稿后同步到既有画布（复审 D02，API.md 24.1）----

// ScreenplaySync POST /user/screenplay/:id/build {sync:true} 的响应。
type ScreenplaySync struct {
	CanvasId     int   `json:"canvas_id"`
	ProjectId    int   `json:"project_id"`
	SyncedShots  []int `json:"synced_shots"`
	AddedShots   []int `json:"added_shots"`
	RemovedShots []int `json:"removed_shots"`
	Revision     int   `json:"revision"`
}

// shotPrompts 与 BuildPlanGraph 同一套分镜图 / 视频提示词拼法。
func shotPrompts(plan *model.Plan, s model.PlanShot, sceneLook map[string]string) (imgPrompt, vidPrompt string) {
	parts := []string{s.Prompt}
	if look := sceneLook[s.Scene]; look != "" {
		parts = append(parts, look)
	}
	for _, ck := range s.Characters {
		for _, c := range plan.Characters {
			if c.Key == ck {
				look := c.Name + "：" + c.Look
				// 本镜人物状态（API.md 25.2）：服装 / 持物 / 伤痕随剧情变化，只加在这一镜
				if st := strings.TrimSpace(s.States[ck]); st != "" {
					look += "（本镜：" + st + "）"
				}
				parts = append(parts, look)
			}
		}
	}
	parts = append(parts, plan.Style)
	motion := s.Motion
	if motion == "" {
		motion = "镜头缓慢推进"
	}
	return strings.Join(parts, "，"), motion + "，" + s.Prompt
}

func planSummary(plan *model.Plan) string {
	summary := []string{"《" + plan.Title + "》", "风格：" + plan.Style}
	for _, s := range plan.Shots {
		line := fmt.Sprintf("镜 %d（%d 秒）：%s", s.No, s.Seconds, s.Prompt)
		if len(s.Dialogue) > 0 {
			parts := make([]string, 0, len(s.Dialogue))
			for _, d := range s.Dialogue {
				parts = append(parts, plan.SpeakerName(d.Speaker)+"："+d.Text)
			}
			line += " ｜ 对白：" + strings.Join(parts, " / ")
		} else if s.Line != "" {
			line += " ｜ 台词：" + s.Line
		}
		summary = append(summary, line)
	}
	return strings.Join(summary, "\n")
}

func nodeToMap(n graphNode) map[string]any {
	b, _ := json.Marshal(n)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

func edgeToMap(e graphEdge) map[string]any {
	b, _ := json.Marshal(e)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

// setVoiceSegments 配音分段与缺省音色：同步时跟着对白改。
func setVoiceSegments(data map[string]any, plan *model.Plan, s model.PlanShot) {
	segs := VoiceSegments(plan, s)
	if len(segs) == 0 {
		delete(data, "segments")
		delete(data, "voice")
		return
	}
	data["segments"] = segs
	if v := singleVoice(segs); v != "" {
		data["voice"] = v
	} else {
		delete(data, "voice")
	}
}

// setLines 多语言台词：有就写 data.lines，没有就去掉旧的。
func setLines(data map[string]any, lines map[string]string) {
	if len(lines) == 0 {
		delete(data, "lines")
		return
	}
	data["lines"] = lines
}

// syncPlanGraph 把 plan 按镜号同步进既有画布 graph（纯函数）：
// 既有镜头只改提示词 / 台词（含多语言）/ 秒数，不动素材、任务状态与位置；有台词但没有配音节点的补建配音节点；
// plan 里新增的镜头补建分镜图 → 视频（→ 配音）节点；画布里有而 plan 里没有的镜号只列出，不删节点。
// 返回新 graph（未变化时与输入相同）与同步结果。
// spLabelPattern 策划案生成的默认标签（镜头 N / 分镜 N / 配音 N），只有这类标签才随镜号重排；用户自己改过的名字不动
var spLabelPattern = regexp.MustCompile(`^(镜头|分镜|配音) \d+$`)

func syncPlanGraph(plan *model.Plan, graph string, m BuildModels) (string, ScreenplaySync, error) {
	res := ScreenplaySync{SyncedShots: []int{}, AddedShots: []int{}, RemovedShots: []int{}}
	var g map[string]any
	if graph == "" || json.Unmarshal([]byte(graph), &g) != nil {
		return graph, res, errors.New("画布数据无法解析")
	}
	rawNodes, _ := g["nodes"].([]any)
	rawEdges, _ := g["edges"].([]any)
	nodes := make([]map[string]any, 0, len(rawNodes))
	for _, raw := range rawNodes {
		if n, ok := raw.(map[string]any); ok {
			nodes = append(nodes, n)
		}
	}
	dataOf := func(n map[string]any) map[string]any {
		d, _ := n["data"].(map[string]any)
		if d == nil {
			d = map[string]any{}
			n["data"] = d
		}
		return d
	}
	spOf := func(n map[string]any) map[string]any {
		sp, _ := dataOf(n)["sp"].(map[string]any)
		return sp
	}
	// 镜头节点的两套索引：有 shot_id（新建 / 同步过的画布）按 id 找，老画布只有序号按 no 找
	videoByShot, imageByShot, voiceByShot := map[int]map[string]any{}, map[int]map[string]any{}, map[int]map[string]any{}
	videoById, imageById, voiceById := map[string]map[string]any{}, map[string]map[string]any{}, map[string]map[string]any{}
	charNode := map[string]string{}
	var planNode map[string]any
	maxIdx := map[string]int{}
	for _, n := range nodes {
		id, _ := n["id"].(string)
		if i := strings.LastIndex(id, "-"); i > 0 {
			if v, err := strconv.Atoi(id[i+1:]); err == nil && v > maxIdx[id[:i]] {
				maxIdx[id[:i]] = v
			}
		}
		sp := spOf(n)
		if sp == nil {
			continue
		}
		layer, _ := sp["layer"].(string)
		shot := toInt(sp["shot"])
		shotId, _ := sp["shot_id"].(string)
		index := func(byNo map[int]map[string]any, byId map[string]map[string]any) {
			if shotId != "" && byId[shotId] == nil {
				byId[shotId] = n
			}
			if shot > 0 && byNo[shot] == nil {
				byNo[shot] = n
			}
		}
		switch layer {
		case "plan":
			if planNode == nil {
				planNode = n
			}
		case "character":
			if key, _ := sp["key"].(string); key != "" {
				charNode[key] = id
			}
		case "shot_image":
			index(imageByShot, imageById)
		case "shot_video":
			index(videoByShot, videoById)
		case "voice":
			index(voiceByShot, voiceById)
		}
	}
	// 匹配模式：plan 里只要有一个镜头 id 能在画布上找到，就按 id 匹配（序号只给没有 shot_id 的老节点兜底）；
	// 一个都对不上（老画布、或客户端把 id 丢了）就退回纯序号匹配，别把整份画布当成「全删全建」。
	idMode := false
	for _, s := range plan.Shots {
		if s.Id != "" && videoById[s.Id] != nil {
			idMode = true
			break
		}
	}
	claimed := map[string]bool{}
	pick := func(s model.PlanShot, byNo map[int]map[string]any, byId map[string]map[string]any) map[string]any {
		if idMode && s.Id != "" {
			if n := byId[s.Id]; n != nil {
				return n
			}
		}
		n := byNo[s.No]
		if n == nil {
			return nil
		}
		if idMode {
			if sp := spOf(n); sp != nil {
				if sid, _ := sp["shot_id"].(string); sid != "" && sid != s.Id {
					return nil // 这个序号位置上的节点属于另一个镜头（可能已被删掉），不能顶上来覆盖
				}
			}
		}
		if nid, _ := n["id"].(string); claimed[nid] {
			return nil
		}
		return n
	}
	relabel := func(n map[string]any, label string, s model.PlanShot) {
		if sp := spOf(n); sp != nil {
			sp["shot"], sp["shot_id"] = s.No, s.Id
		}
		d := dataOf(n)
		if cur, _ := d["label"].(string); cur == "" || spLabelPattern.MatchString(cur) {
			d["label"] = label
		}
	}
	nextId := func(prefix string) string {
		maxIdx[prefix]++
		return fmt.Sprintf("%s-%d", prefix, maxIdx[prefix])
	}
	sceneLook := map[string]string{}
	for _, s := range plan.Scenes {
		sceneLook[s.Key] = s.Look
	}
	claimedVideo := map[string]bool{}
	for i, s := range plan.Shots {
		imgPrompt, vidPrompt := shotPrompts(plan, s, sceneLook)
		video := pick(s, videoByShot, videoById)
		if video != nil {
			vid, _ := video["id"].(string)
			claimed[vid], claimedVideo[vid] = true, true
			d := dataOf(video)
			d["prompt"], d["line"], d["seconds"] = vidPrompt, s.Line, s.Seconds
			setLines(d, s.Lines)
			relabel(video, fmt.Sprintf("镜头 %d", s.No), s)
			if sp := spOf(video); sp != nil {
				sp["seconds"] = s.Seconds
			}
			if img := pick(s, imageByShot, imageById); img != nil {
				if iid, _ := img["id"].(string); iid != "" {
					claimed[iid] = true
				}
				dataOf(img)["prompt"] = imgPrompt
				relabel(img, fmt.Sprintf("分镜 %d", s.No), s)
				if sp := spOf(img); sp != nil {
					sp["seconds"] = s.Seconds
				}
			}
			if voice := pick(s, voiceByShot, voiceById); voice != nil {
				if aid, _ := voice["id"].(string); aid != "" {
					claimed[aid] = true
				}
				vd := dataOf(voice)
				vd["prompt"], vd["text"] = s.Line, s.Line
				setLines(vd, s.Lines)
				setVoiceSegments(vd, plan, s)
				relabel(voice, fmt.Sprintf("配音 %d", s.No), s)
			} else if s.Line != "" {
				vidId, _ := video["id"].(string)
				pos, _ := video["position"].(map[string]any)
				y := int(toFloat(pos["y"]))
				audId := nextId("audio")
				aud := newNode(audId, "audio", fmt.Sprintf("配音 %d", s.No), s.Line, "", colGap*4, y, voiceNodeExtra(plan, s, m.Audio))
				nodes = append(nodes, nodeToMap(aud))
				rawEdges = append(rawEdges, edgeToMap(newEdge(vidId, audId)))
			}
			res.SyncedShots = append(res.SyncedShots, s.No)
			continue
		}
		// 新增镜头：与 BuildPlanGraph 同样的三层节点，行位置按 plan 序号
		y := i * rowGap
		imgId := nextId("image")
		img := newNode(imgId, "image", fmt.Sprintf("分镜 %d", s.No), imgPrompt, "", colGap*2, y,
			map[string]any{"model": m.Image, "sp": map[string]any{"layer": "shot_image", "shot": s.No, "shot_id": s.Id, "seconds": s.Seconds}})
		nodes = append(nodes, nodeToMap(img))
		for _, ck := range s.Characters {
			if cid, ok := charNode[ck]; ok {
				rawEdges = append(rawEdges, edgeToMap(newEdge(cid, imgId)))
			}
		}
		vidId := fmt.Sprintf("video-%d", s.No)
		if s.No <= maxIdx["video"] {
			vidId = nextId("video")
		} else {
			maxIdx["video"] = s.No
		}
		extra := map[string]any{"model": m.Video, "refNodeId": imgId, "seconds": s.Seconds, "line": s.Line,
			"sp": map[string]any{"layer": "shot_video", "shot": s.No, "shot_id": s.Id, "seconds": s.Seconds}}
		if len(s.Lines) > 0 {
			extra["lines"] = s.Lines
		}
		nodes = append(nodes, nodeToMap(newNode(vidId, "video", fmt.Sprintf("镜头 %d", s.No), vidPrompt, "", colGap*3, y, extra)))
		rawEdges = append(rawEdges, edgeToMap(newEdge(imgId, vidId)))
		if s.Line != "" {
			audId := nextId("audio")
			nodes = append(nodes, nodeToMap(newNode(audId, "audio", fmt.Sprintf("配音 %d", s.No), s.Line, "", colGap*4, y, voiceNodeExtra(plan, s, m.Audio))))
			rawEdges = append(rawEdges, edgeToMap(newEdge(vidId, audId)))
		}
		res.AddedShots = append(res.AddedShots, s.No)
	}
	// 删掉的镜头：带序号的视频节点里没有被本次 plan 认领的（按认领结果算，不只看序号是否还在）
	for no, n := range videoByShot {
		if vid, _ := n["id"].(string); !claimedVideo[vid] {
			res.RemovedShots = append(res.RemovedShots, no)
		}
	}
	sort.Ints(res.RemovedShots)
	if planNode != nil {
		dataOf(planNode)["content"] = planSummary(plan)
	}
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	g["nodes"], g["edges"] = out, rawEdges
	b, err := json.Marshal(g)
	if err != nil {
		return graph, res, err
	}
	return string(b), res, nil
}

// SyncScreenplayCanvas 改稿后把 plan 同步进已建画布（不重新生成素材、不删节点），并把 built_revision 推到当前 revision。
func SyncScreenplayCanvas(userId int, sp *model.Screenplay, cv *model.Canvas, m BuildModels) (*ScreenplaySync, error) {
	plan, err := sp.ParsePlan()
	if err != nil {
		return nil, err
	}
	m.fill()
	graph, res, err := syncPlanGraph(plan, cv.Graph, m)
	if err != nil {
		return nil, err
	}
	res.CanvasId, res.ProjectId, res.Revision = cv.Id, cv.ProjectId, sp.Revision
	if len(graph) > model.CanvasGraphMaxBytes {
		return nil, errors.New("画布数据超过 2MB，无法同步")
	}
	if graph != cv.Graph {
		newVersion, _, err := model.SaveCanvas(cv.UserId, cv.Id, cv.Version, map[string]any{"graph": graph})
		if err != nil {
			return nil, err
		}
		_ = model.SnapshotCanvas(cv.UserId, cv.Id, newVersion, cv.Name, graph, model.CanvasVersionSourceManual, fmt.Sprintf("同步剧本第 %d 稿", sp.Revision))
		model.TouchProject(cv.ProjectId)
	}
	if err := model.UpdateScreenplayFields(sp.Id, map[string]any{"built_revision": sp.Revision}); err != nil {
		return nil, err
	}
	Track(userId, "create_sync", map[string]any{"screenplay_id": sp.Id, "canvas_id": cv.Id, "revision": sp.Revision,
		"synced": len(res.SyncedShots), "added": len(res.AddedShots), "removed": len(res.RemovedShots)})
	return &res, nil
}
