package service

import (
	"encoding/json"
	"errors"
	"sort"

	"easytv-api/model"
)

// 镜头视图（API.md 12.1）：把画布里散落的「分镜图 → 视频 → 配音」节点按镜头归拢，
// 带上当前产物与历史候选，成片时间线和向导都从这里取数，不用各自解析 graph。

const shotCandidatesPerNode = 8

// ShotMedia 镜头里某一层的当前产物。
type ShotMedia struct {
	NodeId     string                `json:"node_id"`
	AssetId    int                   `json:"asset_id"`
	Url        string                `json:"url"`
	CoverUrl   string                `json:"cover_url"`
	Duration   float64               `json:"duration"`
	TaskStatus string                `json:"task_status"`
	Candidates []model.NodeCandidate `json:"candidates"`
}

// Shot 一个镜头。
type Shot struct {
	No           int        `json:"no"`
	Line         string     `json:"line"`
	Seconds      float64    `json:"seconds"`
	Video        ShotMedia  `json:"video"`
	Image        *ShotMedia `json:"image"`
	Voice        *ShotMedia `json:"voice"`
	CharacterIds []string   `json:"character_node_ids"`
}

type composeGraph struct {
	Nodes []composeGraphNode `json:"nodes"`
	Edges []composeGraphEdge `json:"edges"`
}

func parseComposeGraph(cv *model.Canvas) (*composeGraph, error) {
	g := &composeGraph{}
	if cv.Graph == "" || json.Unmarshal([]byte(cv.Graph), g) != nil {
		return nil, errors.New("画布数据无法解析")
	}
	return g, nil
}

// BuildShots 按镜号（sp.shot，没有则按画布位置）排好的镜头列表。只看视频节点，没生成过的也列出来。
func BuildShots(cv *model.Canvas) ([]Shot, error) {
	g, err := parseComposeGraph(cv)
	if err != nil {
		return nil, err
	}
	byId := map[string]composeGraphNode{}
	for _, n := range g.Nodes {
		byId[n.Id] = n
	}
	incoming := map[string][]string{}
	for _, e := range g.Edges {
		incoming[e.Target] = append(incoming[e.Target], e.Source)
	}
	voiceByVideo := voiceNodesByVideo(g.Nodes, g.Edges)

	videos := []composeGraphNode{}
	for _, n := range g.Nodes {
		if nodeKind(n) == "video" {
			videos = append(videos, n)
		}
	}
	sort.SliceStable(videos, func(i, j int) bool {
		si, sj := nodeShot(videos[i]), nodeShot(videos[j])
		if si > 0 && sj > 0 && si != sj {
			return si < sj
		}
		if videos[i].Position.Y != videos[j].Position.Y {
			return videos[i].Position.Y < videos[j].Position.Y
		}
		return videos[i].Position.X < videos[j].Position.X
	})

	shots := make([]Shot, 0, len(videos))
	nodeIds := []string{}
	assetIds := []int{}
	for i, v := range videos {
		s := Shot{No: int(nodeShot(v)), Video: mediaOf(v)}
		if s.No <= 0 {
			s.No = i + 1
		}
		s.Line, _ = v.Data["line"].(string)
		s.Seconds = toFloat(v.Data["seconds"])
		nodeIds = append(nodeIds, v.Id)
		assetIds = append(assetIds, s.Video.AssetId)
		if img := shotImageOf(v, byId, incoming); img != nil {
			m := mediaOf(*img)
			s.Image = &m
			nodeIds = append(nodeIds, img.Id)
			assetIds = append(assetIds, m.AssetId)
			for _, src := range incoming[img.Id] {
				if n, ok := byId[src]; ok && nodeKind(n) == "image" {
					s.CharacterIds = append(s.CharacterIds, n.Id)
				}
			}
		}
		if vid, ok := voiceByVideo[v.Id]; ok {
			m := mediaOf(byId[vid])
			s.Voice = &m
			nodeIds = append(nodeIds, vid)
			assetIds = append(assetIds, m.AssetId)
		}
		if s.CharacterIds == nil {
			s.CharacterIds = []string{}
		}
		shots = append(shots, s)
	}

	owners := canvasAssetOwners(cv)
	assets := model.GetAssetsByIdsForUsers(owners, assetIds)
	candidates, _ := model.GetNodeCandidates(owners, cv.Id, nodeIds, shotCandidatesPerNode)
	// 审片结论（API.md 25.5）挂到候选上
	if reviews, err := model.GetShotReviews(cv.Id); err == nil && len(reviews) > 0 {
		for nodeId := range candidates {
			for i := range candidates[nodeId] {
				candidates[nodeId][i].Review = reviews[candidates[nodeId][i].AssetId]
			}
		}
	}
	fill := func(m *ShotMedia) {
		if m == nil {
			return
		}
		if a := assets[m.AssetId]; a != nil {
			m.Url, m.CoverUrl, m.Duration = a.Url, a.CoverUrl, a.Duration
		} else {
			m.AssetId = 0
		}
		m.Candidates = candidates[m.NodeId]
		if m.Candidates == nil {
			m.Candidates = []model.NodeCandidate{}
		}
	}
	for i := range shots {
		fill(&shots[i].Video)
		fill(shots[i].Image)
		fill(shots[i].Voice)
	}
	return shots, nil
}

func mediaOf(n composeGraphNode) ShotMedia {
	status, _ := n.Data["taskStatus"].(string)
	return ShotMedia{NodeId: n.Id, AssetId: toInt(n.Data["assetId"]), TaskStatus: status}
}

// shotImageOf 视频的分镜图：优先 data.refNodeId（策划案建图时写的），否则取连进来的第一个图片节点。
func shotImageOf(video composeGraphNode, byId map[string]composeGraphNode, incoming map[string][]string) *composeGraphNode {
	if ref, _ := video.Data["refNodeId"].(string); ref != "" {
		if n, ok := byId[ref]; ok && nodeKind(n) == "image" {
			return &n
		}
	}
	for _, src := range incoming[video.Id] {
		if n, ok := byId[src]; ok && nodeKind(n) == "image" {
			return &n
		}
	}
	return nil
}

// voiceNodesByVideo 视频节点 → 配音节点 id（不要求已生成）：先看连线，再按镜号兜底。
func voiceNodesByVideo(nodes []composeGraphNode, edges []composeGraphEdge) map[string]string {
	byId := map[string]composeGraphNode{}
	voiceByShot := map[float64]string{}
	for _, n := range nodes {
		byId[n.Id] = n
		if nodeKind(n) == "audio" && nodeLayer(n) == "voice" && nodeShot(n) > 0 {
			if _, ok := voiceByShot[nodeShot(n)]; !ok {
				voiceByShot[nodeShot(n)] = n.Id
			}
		}
	}
	out := map[string]string{}
	for _, e := range edges {
		src, okSrc := byId[e.Source]
		dst, okDst := byId[e.Target]
		if okSrc && okDst && nodeKind(src) == "video" && nodeKind(dst) == "audio" && out[e.Source] == "" {
			out[e.Source] = e.Target
		}
	}
	for _, n := range nodes {
		if nodeKind(n) == "video" && out[n.Id] == "" && nodeShot(n) > 0 {
			if id := voiceByShot[nodeShot(n)]; id != "" {
				out[n.Id] = id
			}
		}
	}
	return out
}

// DefaultTimeline 没保存过时间线时按镜头视图生成：已有视频的镜头启用，其余排在后面不启用。
func DefaultTimeline(cv *model.Canvas, shots []Shot) *model.Timeline {
	tl := &model.Timeline{Aspect: "16:9", Resolution: model.TimelineRes720, Subtitles: true, Voice: true, BgmVolume: 0.2}
	if sp, err := model.GetScreenplayByCanvas(cv.UserId, cv.Id); err == nil {
		if plan, err := sp.ParsePlan(); err == nil && model.ValidAspect(plan.Aspect) {
			tl.Aspect = plan.Aspect
		}
	}
	for _, s := range shots {
		tl.Clips = append(tl.Clips, defaultTimelineClip(s))
	}
	tl.Normalize()
	return tl
}

// defaultTimelineClip 新镜头的缺省设置：有视频就启用；有配音时原声压到 35%，没配音保持原音量。
func defaultTimelineClip(s Shot) model.TimelineClip {
	orig := 1.0
	if s.Voice != nil && s.Voice.AssetId > 0 {
		orig = 0.35
	}
	return model.TimelineClip{NodeId: s.Video.NodeId, Enabled: s.Video.AssetId > 0, VoiceMode: model.TimelineVoiceMix, OrigVolume: orig, VoiceVolume: 1}
}

// MergeTimeline 保存过的时间线与当前画布对齐：删掉节点已不存在的片段，还没有任何片段引用的新镜头追加在末尾（有视频就启用）。
// 同一节点的多条片段（A/B/A）全部保留，去重交给 Normalize 按片段 id 做（复审 D04）。
func MergeTimeline(saved *model.Timeline, shots []Shot) *model.Timeline {
	exists := map[string]Shot{}
	for _, s := range shots {
		exists[s.Video.NodeId] = s
	}
	out := *saved
	out.Clips = make([]model.TimelineClip, 0, len(shots))
	referenced := map[string]bool{}
	for _, c := range saved.Clips {
		if _, ok := exists[c.NodeId]; ok {
			referenced[c.NodeId] = true
			out.Clips = append(out.Clips, c)
		}
	}
	for _, s := range shots {
		if !referenced[s.Video.NodeId] {
			out.Clips = append(out.Clips, defaultTimelineClip(s))
		}
	}
	out.Normalize()
	return &out
}

// BuildCharacterMedia 画布里的角色图节点（sp.layer=character）及其候选与审片结论（API.md 26.1 角色图审片）。
func BuildCharacterMedia(cv *model.Canvas) []ShotMedia {
	out := []ShotMedia{}
	g, err := parseComposeGraph(cv)
	if err != nil {
		return out
	}
	nodeIds := []string{}
	for _, n := range g.Nodes {
		if nodeKind(n) != "image" {
			continue
		}
		sp, _ := n.Data["sp"].(map[string]any)
		if sp == nil || sp["layer"] != "character" {
			continue
		}
		out = append(out, mediaOf(n))
		nodeIds = append(nodeIds, n.Id)
	}
	if len(out) == 0 {
		return out
	}
	owners := canvasAssetOwners(cv)
	candidates, _ := model.GetNodeCandidates(owners, cv.Id, nodeIds, shotCandidatesPerNode)
	reviews, _ := model.GetShotReviews(cv.Id)
	assetIds := []int{}
	for _, m := range out {
		assetIds = append(assetIds, m.AssetId)
	}
	assets := model.GetAssetsByIdsForUsers(owners, assetIds)
	for i := range out {
		if a, ok := assets[out[i].AssetId]; ok && a != nil {
			out[i].Url, out[i].CoverUrl = a.Url, a.CoverUrl
		}
		list := candidates[out[i].NodeId]
		if list == nil {
			list = []model.NodeCandidate{}
		}
		for k := range list {
			list[k].Review = reviews[list[k].AssetId]
		}
		out[i].Candidates = list
	}
	return out
}
