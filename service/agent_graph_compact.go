package service

import (
	"encoding/json"
	"strings"
)

// compactAgentGraph 把模板 graph 压成模型「可抄的结构」：只留节点 id / kind / label / model / 短提示词与连线的 source→target，
// 去掉坐标、尺寸、产物地址这些对搭图没用的字段。原来直接 clipRunes(graph, 2000) 会把 JSON 从中间切断，
// 模型拿到的是不合法的 JSON 还被标成「可抄的结构」（复核发现）。
// 压缩后仍超 max 时先去掉提示词再试，还超就按节点截断（保持合法 JSON，并在 note 里说明）。
func compactAgentGraph(graph string, max int) string {
	graph = strings.TrimSpace(graph)
	if graph == "" {
		return ""
	}
	var g struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if json.Unmarshal([]byte(graph), &g) != nil || len(g.Nodes) == 0 {
		// 不是我们认识的结构：退回老的字符截断，至少不把整段塞进去
		return clipRunes(graph, max)
	}
	nodes := make([]map[string]any, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, compactAgentNode(n, true))
	}
	edges := make([]map[string]any, 0, len(g.Edges))
	for _, e := range g.Edges {
		src, _ := e["source"].(string)
		dst, _ := e["target"].(string)
		if src == "" || dst == "" {
			continue
		}
		edges = append(edges, map[string]any{"source": src, "target": dst})
	}
	out := marshalCompactGraph(nodes, edges, "")
	if len([]rune(out)) <= max {
		return out
	}
	// 去掉提示词再试
	for i := range nodes {
		delete(nodes[i], "prompt")
	}
	out = marshalCompactGraph(nodes, edges, "")
	if len([]rune(out)) <= max {
		return out
	}
	// 还超：按节点截断，只保留前面能放下的节点与它们之间的连线
	total := len(nodes)
	for keep := total - 1; keep >= 1; keep-- {
		kept := nodes[:keep]
		ids := make(map[string]bool, keep)
		for _, n := range kept {
			if id, _ := n["id"].(string); id != "" {
				ids[id] = true
			}
		}
		keptEdges := make([]map[string]any, 0, len(edges))
		for _, e := range edges {
			if ids[e["source"].(string)] && ids[e["target"].(string)] {
				keptEdges = append(keptEdges, e)
			}
		}
		out = marshalCompactGraph(kept, keptEdges, "已截断，只列前 "+itoa(keep)+" 个节点，共 "+itoa(total)+" 个")
		if len([]rune(out)) <= max {
			return out
		}
	}
	return clipRunes(out, max)
}

// compactAgentNode 节点只留搭图要用的键；画布节点的字段大多在 data 里，顶层与 data 都看。
func compactAgentNode(n map[string]any, withPrompt bool) map[string]any {
	data, _ := n["data"].(map[string]any)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := n[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
			if data != nil {
				if v, ok := data[k].(string); ok && strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
		return ""
	}
	out := map[string]any{}
	if v := pick("id"); v != "" {
		out["id"] = v
	}
	if v := pick("kind", "type"); v != "" {
		out["kind"] = v
	}
	if v := pick("label"); v != "" {
		out["label"] = clipRunes(v, 40)
	}
	if v := pick("model"); v != "" {
		out["model"] = v
	}
	if withPrompt {
		if v := pick("prompt"); v != "" {
			out["prompt"] = clipRunes(v, 80)
		}
	}
	return out
}

func marshalCompactGraph(nodes, edges []map[string]any, note string) string {
	payload := map[string]any{"nodes": nodes, "edges": edges}
	if note != "" {
		payload["note"] = note
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(raw)
}
