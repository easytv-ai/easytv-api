package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"easytv-api/model"
)

// ===== 对话式工作流生成的图校验器与编译器（API.md 32，agent-contract 2.5 / 第 4 节）=====

// 布局常量（与 service/screenplay.go 同一套，逐字一致）
const (
	AgentColGap     = 400 // 列间距
	AgentRowGap     = 270 // 行间距
	AgentNodeWText  = 252 // text / audio 卡片宽
	AgentNodeWMedia = 320 // image / video / director / panorama 卡片宽
	AgentNodeH      = 220 // 卡片高

	// agentMaxEdges 连线数上限（与 TOO_LARGE 判定同一处）。
	agentMaxEdges = 120
	// agentMaxGraphBytes graph JSON 字节上限（model/canvas_workflow.go:36 同值）。
	agentMaxGraphBytes = 512 * 1024
)

// AgentValidateOptions 校验与编译的全部外部输入（无包级可变状态，便于并发与测试）。
type AgentValidateOptions struct {
	Catalog     AgentModelCatalog // 必填；生产传 DBModelCatalog{}
	IsMember    bool              // 会员价（service.IsMemberForPricing）
	MaxNodes    int               // 默认 60
	MaxPoints   int               // 0 = 不限
	UserId      int               // 预留：素材归属校验
	TeamId      int
	ChangeIndex int // 该图属于第几条 change（写进 AgentIssue.ChangeIndex）
}

// agentAssetForbidden 3.3 第 6 条：data 里禁止出现的素材与产物字段。
// content 只对生成类节点禁止（text 节点的 content 是必填）。
func agentAssetForbidden(kind, key string) bool {
	switch key {
	case "assetId", "refAssetId", "refUrl", "refItems", "lastAssetId", "lastUrl",
		"styleAssetId", "styleUrl", "markAssetId", "markUrl", "markElements",
		"voiceAssetId", "voiceUrl", "castAssetId", "castUrl", "setAssetId", "setUrl",
		"subjectIds", "subjectVersions",
		"taskId", "taskStatus", "taskProgress", "taskError":
		return true
	case "content":
		return kind != "text"
	}
	return false
}

// agentDataKeyAllowed data 的键白名单 = Required ∪ Optional（其余键一律丢弃）。
func agentDataKeyAllowed(schema *KindSchema, key string) bool {
	if schema == nil {
		return false
	}
	for _, k := range schema.Required {
		if k == key {
			return true
		}
	}
	for _, k := range schema.Optional {
		if k == key {
			return true
		}
	}
	return false
}

// agentMaxNodesOf MaxNodes 缺省 60。
func agentMaxNodesOf(opts AgentValidateOptions) int {
	if opts.MaxNodes > 0 {
		return opts.MaxNodes
	}
	return 60
}

var agentSizePattern = regexp.MustCompile(`^\d{1,5}x\d{1,5}$`)

// agentString 把 data 里的值渲染成字符串（缺失/非法返回空串）。
func agentString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		if x == math.Trunc(x) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

// agentInt 把 data 里的值转成整数。
func agentInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	}
	return 0, false
}

func agentContainsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func agentContainsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

// agentFilterParams data.params 只留该 kind 白名单里的键（其余一律丢弃）。
// 字符串值顺带去空白：查表/比对时本来就会 TrimSpace，输出也必须是同一个值，
// 否则前端拿着 " 9:16" 去匹配档位下拉会落到默认值上。
func agentFilterParams(kind string, raw any) map[string]any {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	allowed := map[string]bool{}
	for _, k := range agentParamKeys(kind) {
		allowed[k] = true
	}
	out := map[string]any{}
	for k, v := range m {
		if !allowed[k] {
			continue
		}
		if s, ok := v.(string); ok {
			v = strings.TrimSpace(s)
			// resolution 的判定走 ToUpper（agentCheckParams），输出不跟着归一的话，
			// 模型写的 "1080p" 会原样落到节点上，前端在档位表里找不到就重置成默认档，
			// AI 选的分辨率被静默丢掉。其余枚举本来就按原样精确比对，不动。
			if k == "resolution" {
				v = strings.ToUpper(v.(string))
			}
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// agentStrictlyEarlier 前端真实排序键（useCanvasConnect.ts:46）：y 优先、x 决胜。
func agentStrictlyEarlier(a, b [2]int) bool {
	return a[1] < b[1] || (a[1] == b[1] && a[0] < b[0])
}

// agentResolveCells 按输入顺序消解同格冲突：后来者 row++ 直到空位。
func agentResolveCells(order []string, pos map[string][2]int) {
	used := map[[2]int]bool{}
	for _, id := range order {
		p, ok := pos[id]
		if !ok {
			continue
		}
		cell := [2]int{p[0] / AgentColGap, p[1] / AgentRowGap}
		for used[cell] {
			p[1] += AgentRowGap
			cell = [2]int{p[0] / AgentColGap, p[1] / AgentRowGap}
		}
		pos[id] = p
		used[cell] = true
	}
}

// agentEdgeCycle 图里有环时返回环上的一个节点 id（DFS 三色法，抄 service/batch.go 的 batchCycle）。
func agentEdgeCycle(edges []WorkflowEdge) string {
	adj := map[string][]string{}
	ids := []string{}
	seen := map[string]bool{}
	for _, e := range edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
		if !seen[e.Source] {
			seen[e.Source], ids = true, append(ids, e.Source)
		}
	}
	state := map[string]int{} // 0 未访问 1 访问中 2 完成
	var visit func(string) string
	visit = func(id string) string {
		switch state[id] {
		case 1:
			return id
		case 2:
			return ""
		}
		state[id] = 1
		for _, nxt := range adj[id] {
			if c := visit(nxt); c != "" {
				return c
			}
		}
		state[id] = 2
		return ""
	}
	for _, id := range ids {
		if c := visit(id); c != "" {
			return c
		}
	}
	return ""
}

// ValidateAgentGraph 校验一张已编译的图。永不 panic；graph 为 nil 时返回 BAD_GRAPH。
// 返回值的 OK 只表示「这张图没有 error 级问题」（OK = len(Errors) == 0），不判断 changes 数量——
// 那是 BuildAgentPlan 的事。TOO_MANY_CHANGES / UNKNOWN_ACTION / NO_CHANGES 不在这里产生。
func ValidateAgentGraph(graph *CanvasWorkflowGraph, opts AgentValidateOptions) *AgentValidateReport {
	_, report := compileAgentGraph(graph, opts)
	return report
}

// CompileAgentGraph 把模型给的图编译成可直接落画布的图。流水线顺序是冻结的（见 agent-contract 2.5）：
// 结构 → kind 白名单 → 读语义提示 → data 规范化 → 网格吸附 → 首尾帧排序修正 → 连线检查 → 逐项校验 → 估价。
// 返回编译后的图与报告；报告里 ok=false 时调用方负责丢弃该条 change。
func CompileAgentGraph(graph *CanvasWorkflowGraph, opts AgentValidateOptions) (*CanvasWorkflowGraph, *AgentValidateReport) {
	return compileAgentGraph(graph, opts)
}

func compileAgentGraph(graph *CanvasWorkflowGraph, opts AgentValidateOptions) (*CanvasWorkflowGraph, *AgentValidateReport) {
	report := &AgentValidateReport{
		Errors:   []AgentIssue{},
		Warnings: []AgentIssue{},
		Estimate: AgentEstimate{Items: []AgentEstimateItem{}},
	}
	addErr := func(code, nodeId, field, value, msg, hint string) {
		report.Errors = append(report.Errors, AgentIssue{Code: code, Severity: AgentIssueSeverity(code),
			NodeId: nodeId, Field: field, Value: value, Message: msg, Hint: hint, ChangeIndex: opts.ChangeIndex})
	}
	addWarn := func(code, nodeId, field, value, msg, hint string) {
		report.Warnings = append(report.Warnings, AgentIssue{Code: code, Severity: AgentIssueSeverity(code),
			NodeId: nodeId, Field: field, Value: value, Message: msg, Hint: hint, ChangeIndex: opts.ChangeIndex})
	}

	// ---- 1. 结构检查 ----
	if graph == nil {
		addErr(AgentCodeBadGraph, "", "", "", "没有拿到工作流数据", "输出完整的 graph 对象（version/nodes/edges）")
		report.OK = false
		return nil, report
	}
	// kind 先去空白再进流水线：AgentKindSchema 内部会 TrimSpace，如果带着原始值往下走，
	// " video " 会绕过 UNKNOWN_KIND 判定、躲开 n.Kind == "video" 的语义规则（VIDEO_NEEDS_FRAME / FRAME_ORDER），
	// 还会把未注册的 kind 原样写进 normalized（前端拿到非法 kind）。整个流水线只用 nodes 这份规范化副本。
	nodes := make([]WorkflowNode, len(graph.Nodes))
	copy(nodes, graph.Nodes)
	for i := range nodes {
		nodes[i].Kind = strings.TrimSpace(nodes[i].Kind)
	}
	if graph.Version != 1 {
		addErr(AgentCodeBadGraph, "", "version", fmt.Sprint(graph.Version), "graph.version 必须是 1", "把 version 写成 1")
	}
	if len(nodes) == 0 {
		addErr(AgentCodeBadGraph, "", "nodes", "", "graph.nodes 不能为空", "至少给一个节点")
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		if strings.TrimSpace(n.Id) == "" {
			addErr(AgentCodeBadGraph, n.Id, "id", n.Id, "节点缺少 id", "id 只用 n1..nN，长度不超过 32")
		} else if ids[n.Id] {
			addErr(AgentCodeDuplicateId, n.Id, "id", n.Id, fmt.Sprintf("节点 id %s 重复了", n.Id), "每个节点的 id 必须唯一")
		}
		if strings.TrimSpace(n.Kind) == "" {
			addErr(AgentCodeBadGraph, n.Id, "kind", "", "节点缺少 kind", "kind 只能是 "+strings.Join(AgentCreatableKinds(), "/"))
		}
		if n.Data == nil {
			addErr(AgentCodeBadGraph, n.Id, "data", "", "节点的 data 必须是对象", "data 里写 label/prompt/model 等字段")
		}
		if n.Id != "" {
			ids[n.Id] = true
		}
	}
	if len(report.Errors) > 0 {
		report.OK = false
		return nil, report
	}

	// ---- 2. kind 白名单 ----
	for _, n := range nodes {
		if !AgentKindCreatable(n.Kind) {
			addErr(AgentCodeUnknownKind, n.Id, "kind", n.Kind, fmt.Sprintf("不能新建 %s 节点", n.Kind),
				"只能新建 "+strings.Join(AgentCreatableKinds(), "/")+"；"+strings.Join(agentNonCreatableKinds(), "/")+" 只能出现在已有画布上")
		}
	}
	if len(report.Errors) > 0 {
		report.OK = false
		return nil, report
	}

	// ---- 3. 读语义提示（必须从原始 data 读：下一步的白名单会把 frameRole 丢掉）----
	frameFirst, frameLast := map[string]bool{}, map[string]bool{}
	videoMode := map[string]string{}
	for _, n := range nodes {
		if n.Kind == "image" {
			switch strings.TrimSpace(agentString(n.Data["frameRole"])) {
			case "first":
				frameFirst[n.Id] = true
			case "last":
				frameLast[n.Id] = true
			}
		}
		if n.Kind == "video" || n.Kind == "director" {
			videoMode[n.Id] = strings.TrimSpace(agentString(n.Data["videoMode"]))
		}
	}

	// ---- 4. data 规范化 ----
	dataOf := map[string]map[string]any{}
	for _, n := range nodes {
		schema := AgentKindSchema(n.Kind)
		data := map[string]any{}
		for k, v := range n.Data {
			if agentAssetForbidden(n.Kind, k) {
				addErr(AgentCodeAssetForbidden, n.Id, k, clipRunes(agentString(v), 80),
					fmt.Sprintf("节点 data 里不能出现 %s", k), "素材与产物由用户在画布上选，模型只写提示词与参数")
				continue
			}
			if !agentDataKeyAllowed(schema, k) {
				continue
			}
			data[k] = v
		}
		if params, ok := data["params"]; ok {
			if filtered := agentFilterParams(n.Kind, params); filtered != nil {
				data["params"] = filtered
			} else {
				delete(data, "params")
			}
		}
		// model / videoMode 是标识与枚举值：查表时本来就会 TrimSpace，输出也必须是去空白后的值，
		// 否则前端拿着 " gpt-image-1 " 去提交、或拿着 " i2v" 去匹配视频模式，都会落到默认值上。
		if m, ok := data["model"].(string); ok {
			data["model"] = strings.TrimSpace(m)
		}
		if v, ok := data["videoMode"].(string); ok {
			data["videoMode"] = strings.TrimSpace(v)
		}
		data["kind"] = n.Kind
		data["mode"] = "idle"
		// frameRole 是编译期提示，前端不认识，输出前删掉（第 6 步用的是上面的局部变量）
		delete(data, "frameRole")
		dataOf[n.Id] = data
	}

	// ---- 5. 网格吸附：col = round((x-minX)/400)、row = round((y-minY)/270)，同格冲突按输入顺序下移 ----
	minX, minY := nodes[0].X, nodes[0].Y
	for _, n := range nodes {
		if n.X < minX {
			minX = n.X
		}
		if n.Y < minY {
			minY = n.Y
		}
	}
	pos := map[string][2]int{}
	order := make([]string, 0, len(nodes))
	for _, n := range nodes {
		col := int(math.Round(float64(n.X-minX) / float64(AgentColGap)))
		row := int(math.Round(float64(n.Y-minY) / float64(AgentRowGap)))
		if col < 0 {
			col = 0
		}
		if row < 0 {
			row = 0
		}
		pos[n.Id] = [2]int{col * AgentColGap, row * AgentRowGap}
		order = append(order, n.Id)
	}
	agentResolveCells(order, pos)

	// ---- 6. 首尾帧排序修正（用第 3 步的 frameRole，在第 5 步吸附后的网格坐标上比较）----
	kindOf := map[string]string{}
	for _, n := range nodes {
		kindOf[n.Id] = n.Kind
	}
	for _, n := range nodes {
		if n.Kind != "video" && n.Kind != "director" {
			continue
		}
		first, last := "", ""
		for _, e := range graph.Edges {
			if e.Target != n.Id || (first != "" && last != "") {
				continue
			}
			if kindOf[e.Source] != "image" {
				continue
			}
			if first == "" && frameFirst[e.Source] {
				first = e.Source
			}
			if last == "" && frameLast[e.Source] {
				last = e.Source
			}
		}
		if first == "" || last == "" || first == last {
			continue
		}
		if agentStrictlyEarlier(pos[first], pos[last]) {
			continue
		}
		oldFY, oldLY := pos[first][1], pos[last][1]
		// a. 交换 F 与 L 的整对 (x, y)：只交换 y 兑现不了「F 严格早于 L」
		pos[first], pos[last] = pos[last], pos[first]
		// b. 从 y 较大的那个开始，按输入顺序对整张图重跑一次同格冲突消解
		agentResolveCells(order, pos)
		// c. 复查；仍不满足就把 L 放到 F 下一行再消解一次（每个 V 最多两轮，保证终止）
		if !agentStrictlyEarlier(pos[first], pos[last]) {
			pos[last] = [2]int{pos[last][0], pos[first][1] + AgentRowGap}
			agentResolveCells(order, pos)
		}
		addWarn(AgentCodeFrameOrder, n.Id, "frameRole",
			fmt.Sprintf("%s:%d→%d, %s:%d→%d", first, oldFY, pos[first][1], last, oldLY, pos[last][1]),
			"首帧节点没有排在尾帧前面，已按首尾帧角色调整位置",
			"把 frameRole=first 的图片放在更靠上的位置，前端按位置从上到下取首帧")
	}

	out := make([]WorkflowNode, 0, len(nodes))
	for _, n := range nodes {
		w, h := n.Width, n.Height
		if w <= 0 {
			w = agentNodeWidth(n.Kind)
		}
		if h <= 0 {
			h = AgentNodeH
		}
		out = append(out, WorkflowNode{Id: n.Id, Kind: n.Kind, X: pos[n.Id][0], Y: pos[n.Id][1], Width: w, Height: h, Data: dataOf[n.Id]})
	}

	// ---- 7. 连线检查 ----
	kept := make([]WorkflowEdge, 0, len(graph.Edges))
	seenEdge := map[[2]string]bool{}
	for _, e := range graph.Edges {
		sourceKind, okS := kindOf[e.Source]
		targetKind, okT := kindOf[e.Target]
		if !okS || !okT {
			addErr(AgentCodeEdgeDangling, "", "edges", e.Source+" → "+e.Target, "连线指向了不存在的节点", "source/target 只能用本图 nodes 里的 id")
			continue
		}
		if e.Source == e.Target {
			addErr(AgentCodeSelfLoop, e.Source, "edges", e.Source+" → "+e.Target, "节点不能连到自己", "去掉这条连线")
			continue
		}
		key := [2]string{e.Source, e.Target}
		if seenEdge[key] {
			addErr(AgentCodeDuplicateEdge, e.Source, "edges", e.Source+" → "+e.Target, "同一条连线出现了两次", "去掉重复的连线")
			continue
		}
		seenEdge[key] = true
		if targetKind == "text" && sourceKind != "text" {
			addWarn(AgentCodeUselessEdge, e.Target, "edges", e.Source+" → "+e.Target,
				"文本节点只接受文本上游，这条连线不会生效，已丢弃", "把这段文字直接写进文本节点的 content")
			continue
		}
		if targetKind != "text" && !AgentKindAcceptable(targetKind, sourceKind) {
			addErr(AgentCodeRoleKindMismatch, e.Target, "edges", e.Source+" → "+e.Target,
				fmt.Sprintf("%s 节点不能接 %s 上游", targetKind, sourceKind),
				fmt.Sprintf("%s 只能接 %s", targetKind, strings.Join(AgentKindSchema(targetKind).AcceptsFrom, "/")))
		}
		// 前端每个节点只有一个 in / out 端口（第 4 节开头）：handle 统一成 out/in，
		// 模型写 " out " / "foo" 会让前端连不上线（buildWorkflowGraph 也是写死 out/in）。
		sh, th := strings.TrimSpace(e.SourceHandle), strings.TrimSpace(e.TargetHandle)
		if sh != "out" {
			sh = "out"
		}
		if th != "in" {
			th = "in"
		}
		kept = append(kept, WorkflowEdge{Source: e.Source, Target: e.Target, SourceHandle: sh, TargetHandle: th})
	}
	if cycle := agentEdgeCycle(kept); cycle != "" {
		addErr(AgentCodeCycle, cycle, "edges", cycle, "连线成环了，生成时会互相等待", "去掉一条回边的连线")
	}
	inDeg, outDeg := map[string]int{}, map[string]int{}
	for _, e := range kept {
		outDeg[e.Source]++
		inDeg[e.Target]++
	}
	for _, n := range nodes {
		schema := AgentKindSchema(n.Kind)
		if n.Kind == "text" || schema == nil || len(schema.AcceptsFrom) == 0 {
			continue
		}
		if inDeg[n.Id] == 0 && outDeg[n.Id] == 0 {
			addWarn(AgentCodeOrphanNode, n.Id, "", "", "该节点没有任何连线，生成后不会被使用", "给它连一条上游或删掉")
		}
	}

	// ---- 8. 逐项校验 ----
	if n := len(nodes); n > agentMaxNodesOf(opts) {
		addErr(AgentCodeTooLarge, "", "nodes", strconv.Itoa(n),
			fmt.Sprintf("节点太多（%d 个，最多 %d 个）", n, agentMaxNodesOf(opts)), "拆成多次对话，一次别超过上限")
	}
	if n := len(graph.Edges); n > agentMaxEdges {
		addErr(AgentCodeTooLarge, "", "edges", strconv.Itoa(n),
			fmt.Sprintf("连线太多（%d 条，最多 %d 条）", n, agentMaxEdges), "合并中间节点，减少连线")
	}
	if n := len(agentJSON(graph)); n > agentMaxGraphBytes {
		addErr(AgentCodeTooLarge, "", "graph", strconv.Itoa(n),
			fmt.Sprintf("工作流数据太大（%d 字节，上限 %d）", n, agentMaxGraphBytes), "缩短提示词，减少节点")
	}

	for _, n := range nodes {
		schema := AgentKindSchema(n.Kind)
		data := dataOf[n.Id]
		for _, key := range schema.Required {
			if strings.TrimSpace(agentString(data[key])) == "" {
				addErr(AgentCodeMissingRequired, n.Id, key, "", fmt.Sprintf("节点缺少必填的 %s", key), agentRequiredHint(schema, key))
			}
		}
		var hint *AgentModelHint
		if name := strings.TrimSpace(agentString(data["model"])); name != "" && opts.Catalog != nil {
			h, err := opts.Catalog.Resolve(schema.TaskType, name)
			switch {
			case err == nil:
				hint = h
				if h.Type != schema.TaskType {
					addErr(AgentCodeModelTypeMismatch, n.Id, "model", name,
						fmt.Sprintf("模型 %s 是%s模型，不能用在%s节点上", name, taskTypeLabel(h.Type), schema.Label),
						"换成"+taskTypeLabel(schema.TaskType)+"模型")
				}
			case errors.Is(err, ErrAgentModelNotPriced):
				addErr(AgentCodeModelNotPriced, n.Id, "model", name,
					fmt.Sprintf("模型 %s 暂未定价，不能用于生成", name), "用 search_models 查一个已定价的模型")
			default:
				addErr(AgentCodeModelNotFound, n.Id, "model", name,
					fmt.Sprintf("模型 %s 未接入网关", name), "用 search_models 查一个可用的模型")
			}
		}
		if params, ok := data["params"].(map[string]any); ok {
			agentCheckParams(n.Id, params, hint, addErr)
		}
		if n.Kind == "video" || n.Kind == "director" {
			mode := videoMode[n.Id]
			if (mode == "fl2v" || mode == "i2v") && !agentHasUpstreamKind(kept, kindOf, n.Id, "image") {
				addErr(AgentCodeVideoNeedsFrame, n.Id, "videoMode", mode,
					fmt.Sprintf("videoMode=%s 需要一张上游图片当首帧", mode), "加一个 image 节点（写好 prompt 与 model）并连到它")
			}
		}
		for _, field := range []string{"prompt", "content"} {
			v := agentString(data[field])
			if strings.Contains(v, "{{ref:") || strings.Contains(v, "{{cam:") || strings.Contains(v, "{{mark:") {
				addErr(AgentCodeRefTokenLeft, n.Id, field, clipRunes(v, 80),
					"提示词里不能出现 {{ref:}}/{{cam:}}/{{mark:}} 这类占位符", "用大白话把素材、机位、标记区域描述出来")
			}
		}
	}

	// ---- 9. 估价 ----
	if opts.Catalog != nil {
		for _, n := range nodes {
			schema := AgentKindSchema(n.Kind)
			if schema.TaskType == "" || schema.TaskType == model.TaskTypeText {
				continue
			}
			name := strings.TrimSpace(agentString(dataOf[n.Id]["model"]))
			if name == "" {
				continue
			}
			paramsJSON := agentJSON(dataOf[n.Id]["params"])
			if paramsJSON == "" {
				paramsJSON = "{}"
			}
			points, err := opts.Catalog.Estimate(schema.TaskType, name, paramsJSON, opts.IsMember)
			if err != nil {
				continue
			}
			report.Estimate.Items = append(report.Estimate.Items, AgentEstimateItem{
				NodeId: n.Id, Kind: n.Kind, Type: schema.TaskType, Model: name, Points: points,
			})
			report.Estimate.Points += points
		}
	}
	if opts.MaxPoints > 0 && report.Estimate.Points > opts.MaxPoints {
		addErr(AgentCodeOverBudget, "", "estimate", strconv.Itoa(report.Estimate.Points),
			fmt.Sprintf("这套方案预计 %d 积分，超过本次上限 %d 积分", report.Estimate.Points, opts.MaxPoints),
			"减节点、换更省的模型，或把时长调短")
	}

	report.OK = len(report.Errors) == 0
	normalized := &CanvasWorkflowGraph{Version: 1, Nodes: out, Edges: kept}
	report.Normalized = normalized
	return normalized, report
}

// agentNonCreatableKinds 只能出现在已有画布上的 kind（提示词用）。
func agentNonCreatableKinds() []string {
	out := []string{}
	for _, s := range agentKindSchemas {
		if !s.Creatable {
			out = append(out, s.Kind)
		}
	}
	return out
}

func agentNodeWidth(kind string) int {
	switch kind {
	case "text", "audio":
		return AgentNodeWText
	}
	return AgentNodeWMedia
}

func agentRequiredHint(schema *KindSchema, key string) string {
	switch key {
	case "prompt":
		return "把画面/声音要求写清楚，用中文，不要留空"
	case "model":
		return "用 search_models 查一个可用的" + taskTypeLabel(schema.TaskType) + "模型"
	case "content":
		return "文本节点要写正文内容"
	}
	return "补上这个字段"
}

// agentHasUpstreamKind 节点是否有某个 kind 的上游。
func agentHasUpstreamKind(edges []WorkflowEdge, kindOf map[string]string, nodeId, kind string) bool {
	for _, e := range edges {
		if e.Target == nodeId && kindOf[e.Source] == kind {
			return true
		}
	}
	return false
}

// agentCheckParams data.params 逐项校验（时长 / 分辨率必须命中模型档位，枚举必须在白名单里）。
func agentCheckParams(nodeId string, params map[string]any, hint *AgentModelHint, addErr func(code, nodeId, field, value, msg, hint string)) {
	for key, raw := range params {
		switch key {
		case "duration":
			n, ok := agentInt(raw)
			if !ok || n <= 0 {
				addErr(AgentCodeParamOutOfRange, nodeId, "params.duration", agentString(raw), "时长必须是整数秒", "duration 写成 5 这样的整数")
				continue
			}
			if hint != nil && len(hint.Durations) > 0 && !agentContainsInt(hint.Durations, n) {
				addErr(AgentCodeParamOutOfRange, nodeId, "params.duration", strconv.Itoa(n),
					fmt.Sprintf("%s 只支持 %s 秒，当前请求 %d 秒", hint.Model, agentIntListText(hint.Durations), n),
					"换成模型支持的时长档位")
			}
		case "resolution":
			v := strings.ToUpper(strings.TrimSpace(agentString(raw)))
			allowed := []string{"480P", "720P", "1080P"}
			if hint != nil && len(hint.Resolutions) > 0 {
				allowed = hint.Resolutions
			}
			if v == "" || !agentContainsString(allowed, v) {
				addErr(AgentCodeParamOutOfRange, nodeId, "params.resolution", agentString(raw),
					fmt.Sprintf("分辨率只能是 %s", strings.Join(allowed, "/")), "换成支持的分辨率")
			}
		case "size":
			if v := strings.TrimSpace(agentString(raw)); !agentSizePattern.MatchString(v) {
				addErr(AgentCodeParamOutOfRange, nodeId, "params.size", v, "size 要写成 1024x1536 这样的宽x高", "例：1024x1536")
			}
		default:
			spec, ok := agentParamSpecs[key]
			if !ok || spec.Type != "enum" {
				continue
			}
			v := strings.TrimSpace(agentString(raw))
			if !agentContainsString(spec.Enum, v) {
				addErr(AgentCodeParamOutOfRange, nodeId, "params."+key, v,
					fmt.Sprintf("%s 只能是 %s", key, strings.Join(spec.Enum, "/")), "换成白名单里的取值")
			}
		}
	}
}

func agentIntListText(list []int) string {
	parts := make([]string, 0, len(list))
	for _, n := range list {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, "/")
}
