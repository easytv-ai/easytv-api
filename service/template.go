package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

// 可复刻模板（API.md 14.3）：作品开源时给来源画布拍一张「模型 + 定价 + 能力」快照；
// 做同款前预览：用到哪些模型、现在还能不能用、价格和能力有没有变、全部重新生成预计多少积分、哪些参考素材需要重新选。

// TemplateModel 画布里用到的一个模型。
type TemplateModel struct {
	Model         string   `json:"model"`
	Type          string   `json:"type"`
	Label         string   `json:"label"`
	Nodes         int      `json:"nodes"`
	Unit          string   `json:"unit"`
	PointsPerUnit int      `json:"points_per_unit"`
	Durations     []int    `json:"durations,omitempty"`
	Resolutions   []string `json:"resolutions,omitempty"`
	ImageInput    string   `json:"image_input,omitempty"`
}

// TemplateMeta 快照。
type TemplateMeta struct {
	CapturedAt      int64           `json:"captured_at"`
	NodeCount       int             `json:"node_count"`
	GenerativeNodes int             `json:"generative_nodes"`
	Models          []TemplateModel `json:"models"`
}

// nodeTaskType 节点 kind → 生成任务类型；非生成节点返回空。
func nodeTaskType(kind string) string {
	switch kind {
	case "image", "character", "scene", "edit", "panorama":
		return model.TaskTypeImage
	case "video", "director":
		return model.TaskTypeVideo
	case "audio":
		return model.TaskTypeAudio
	case "text":
		return model.TaskTypeText
	}
	return ""
}

type templateNode struct {
	Id   string         `json:"id"`
	Data map[string]any `json:"data"`
}

func parseTemplateNodes(graph string) []templateNode {
	var g struct {
		Nodes []templateNode `json:"nodes"`
	}
	_ = json.Unmarshal([]byte(graph), &g)
	return g.Nodes
}

// nodeModel 节点用的模型与参数：策划案建的节点在 data.model 上；自由画布的节点只记 taskId，按作者的那次任务取。
type nodeModel struct {
	Type, Model, Params string
}

func templateNodeModels(cv *model.Canvas, nodes []templateNode) map[string]nodeModel {
	out := map[string]nodeModel{}
	taskIds := []int{}
	for _, n := range nodes {
		kind, _ := n.Data["kind"].(string)
		typ := nodeTaskType(kind)
		if typ == "" {
			continue
		}
		if name, _ := n.Data["model"].(string); strings.TrimSpace(name) != "" {
			out[n.Id] = nodeModel{Type: typ, Model: name}
			continue
		}
		if id := toInt(n.Data["taskId"]); id > 0 {
			taskIds = append(taskIds, id)
		}
	}
	if len(taskIds) > 0 {
		var tasks []*model.GenerationTask
		model.DB.Select("id, user_id, node_id, type, model, params").Where("id IN ? AND user_id = ?", taskIds, cv.UserId).Find(&tasks)
		byId := map[int]*model.GenerationTask{}
		for _, t := range tasks {
			byId[t.Id] = t
		}
		for _, n := range nodes {
			if _, ok := out[n.Id]; ok {
				continue
			}
			if t := byId[toInt(n.Data["taskId"])]; t != nil && t.Model != "" {
				out[n.Id] = nodeModel{Type: t.Type, Model: t.Model, Params: t.Params}
			}
		}
	}
	return out
}

// BuildTemplateMeta 统计画布用到的模型并附上当前定价与能力。
func BuildTemplateMeta(cv *model.Canvas) *TemplateMeta {
	nodes := parseTemplateNodes(cv.Graph)
	return buildTemplateMeta(nodes, templateNodeModels(cv, nodes))
}

func buildTemplateMeta(nodes []templateNode, models map[string]nodeModel) *TemplateMeta {
	meta := &TemplateMeta{CapturedAt: common.GetTimestamp(), Models: []TemplateModel{}}
	byKey := map[string]*TemplateModel{}
	for _, n := range nodes {
		meta.NodeCount++
		nm, ok := models[n.Id]
		if !ok {
			continue
		}
		typ, name := nm.Type, nm.Model
		meta.GenerativeNodes++
		key := typ + "|" + name
		m := byKey[key]
		if m == nil {
			m = &TemplateModel{Model: name, Type: typ}
			if price, err := model.GetModelPrice(name); err == nil && price.Type == typ {
				m.Label, m.Unit, m.PointsPerUnit = price.Label, price.Unit, price.PointsPerUnit
				m.Durations, m.Resolutions = price.AllowedDurations(), price.AllowedResolutions()
				if typ == model.TaskTypeImage {
					m.ImageInput = price.EffectiveImageInput()
				}
			}
			byKey[key] = m
		}
		m.Nodes++
	}
	for _, m := range byKey {
		meta.Models = append(meta.Models, *m)
	}
	sort.Slice(meta.Models, func(i, j int) bool { return meta.Models[i].Nodes > meta.Models[j].Nodes })
	return meta
}

// SnapshotWorkTemplate 作品开源时给来源画布拍快照（失败静默，预览会退回实时统计）。
func SnapshotWorkTemplate(workId, authorId, sourceCanvasId int) {
	if sourceCanvasId <= 0 {
		return
	}
	cv, err := model.GetUserCanvas(authorId, sourceCanvasId)
	if err != nil {
		return
	}
	b, _ := json.Marshal(BuildTemplateMeta(cv))
	model.DB.Model(&model.Work{}).Where("id = ?", workId).Update("template_meta", string(b))
}

// ClonePreviewModel 预览里的一个模型：快照时的信息 + 现在的状态。
type ClonePreviewModel struct {
	TemplateModel
	Available bool     `json:"available"`
	Priced    bool     `json:"priced"`
	Changes   []string `json:"changes"`
}

// ClonePreviewMissing 做同款后需要重新选素材的节点。
type ClonePreviewMissing struct {
	NodeId string   `json:"node_id"`
	Label  string   `json:"label"`
	Fields []string `json:"fields"`
}

// ClonePreview 做同款预览。
type ClonePreview struct {
	WorkId          int                   `json:"work_id"`
	HasSnapshot     bool                  `json:"has_snapshot"`
	CapturedAt      int64                 `json:"captured_at"`
	NodeCount       int                   `json:"node_count"`
	GenerativeNodes int                   `json:"generative_nodes"`
	EstimatePoints  int                   `json:"estimate_points"`
	Unpriced        int                   `json:"unpriced_nodes"`
	Models          []ClonePreviewModel   `json:"models"`
	MissingRefs     []ClonePreviewMissing `json:"missing_refs"`
	SubjectNodes    int                   `json:"subject_nodes"`
}

// referenceUrlFields 复制后资产 ID 会被清掉、只剩地址的参考素材字段（见 SanitizeSharedGraph）。
var referenceUrlFields = map[string]string{
	"refUrl": "参考图", "lastUrl": "尾帧", "styleUrl": "风格参考", "markUrl": "局部修改标记",
	"voiceUrl": "音色参考", "castUrl": "角色参考", "setUrl": "场景参考",
}

// BuildClonePreview 基于来源画布与快照生成预览；viewerId 用于按会员价估算（未登录按标准价）。
func BuildClonePreview(w *model.Work, src *model.Canvas, viewerId int) *ClonePreview {
	nodes := parseTemplateNodes(src.Graph)
	nodeModels := templateNodeModels(src, nodes)
	current := buildTemplateMeta(nodes, nodeModels)
	var snap TemplateMeta
	hasSnap := w.TemplateMeta != "" && json.Unmarshal([]byte(w.TemplateMeta), &snap) == nil
	snapByKey := map[string]TemplateModel{}
	for _, m := range snap.Models {
		snapByKey[m.Type+"|"+m.Model] = m
	}
	// 做同款落在个人空间，按自己的会员估价（API.md 20）
	isMember := viewerId > 0 && IsMemberForPricing(viewerId, 0)
	p := &ClonePreview{WorkId: w.Id, HasSnapshot: hasSnap, CapturedAt: snap.CapturedAt, NodeCount: current.NodeCount,
		GenerativeNodes: current.GenerativeNodes, Models: []ClonePreviewModel{}, MissingRefs: []ClonePreviewMissing{}}

	for _, m := range current.Models {
		pm := ClonePreviewModel{TemplateModel: m, Available: FindProvider(m.Type, m.Model) != nil, Priced: m.Unit != "", Changes: []string{}}
		if old, ok := snapByKey[m.Type+"|"+m.Model]; ok {
			if old.PointsPerUnit > 0 && m.PointsPerUnit > 0 && old.PointsPerUnit != m.PointsPerUnit {
				pm.Changes = append(pm.Changes, fmt.Sprintf("标准价 %d → %d 积分", old.PointsPerUnit, m.PointsPerUnit))
			}
			if fmt.Sprint(old.Durations) != fmt.Sprint(m.Durations) {
				pm.Changes = append(pm.Changes, "支持的时长有变化")
			}
			if fmt.Sprint(old.Resolutions) != fmt.Sprint(m.Resolutions) {
				pm.Changes = append(pm.Changes, "支持的分辨率有变化")
			}
			if old.ImageInput != "" && old.ImageInput != m.ImageInput {
				pm.Changes = append(pm.Changes, "参考图能力有变化")
			}
		}
		if !pm.Available {
			pm.Changes = append(pm.Changes, "模型已下线，做同款后需要换模型")
		} else if !pm.Priced {
			pm.Changes = append(pm.Changes, "模型暂未定价，暂时不能生成")
		}
		p.Models = append(p.Models, pm)
	}

	for _, n := range nodes {
		if nm, ok := nodeModels[n.Id]; ok {
			typ, name := nm.Type, nm.Model
			// 优先用作者那次任务的参数（时长 / 分辨率），只按一份计
			params := `{"count":1}`
			if nm.Params != "" {
				params = WithSingleCount(nm.Params)
			} else if typ == model.TaskTypeVideo {
				seconds := toInt(n.Data["seconds"])
				if seconds <= 0 {
					seconds = 5
				}
				params = fmt.Sprintf(`{"count":1,"duration":%d}`, seconds)
			}
			if points, _, err := EstimateTaskPoints(typ, name, params, isMember); err == nil {
				p.EstimatePoints += points
			} else {
				p.Unpriced++
			}
		}
		var fields []string
		for key, label := range referenceUrlFields {
			if v, _ := n.Data[key].(string); strings.TrimSpace(v) != "" {
				fields = append(fields, label)
			}
		}
		if items, ok := n.Data["refItems"].([]any); ok && len(items) > 0 {
			fields = append(fields, fmt.Sprintf("%d 张手动参考图", len(items)))
		}
		if ids, ok := n.Data["subjectIds"].([]any); ok && len(ids) > 0 {
			p.SubjectNodes++
			fields = append(fields, "主体引用")
		}
		if len(fields) > 0 {
			sort.Strings(fields)
			label, _ := n.Data["label"].(string)
			p.MissingRefs = append(p.MissingRefs, ClonePreviewMissing{NodeId: n.Id, Label: label, Fields: fields})
		}
	}
	return p
}
