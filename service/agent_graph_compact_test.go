package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactAgentGraphKeepsOnlyStructure(t *testing.T) {
	graph := `{"nodes":[
		{"id":"text-1","type":"easytv","position":{"x":10,"y":20},"width":320,"data":{"kind":"text","label":"脚本","prompt":"` + strings.Repeat("很长的提示词", 40) + `","content":"https://oss/x.txt"}},
		{"id":"image-1","type":"easytv","data":{"kind":"image","label":"分镜","model":"gpt-image-2","assetId":9}}],
		"edges":[{"id":"e1","source":"text-1","target":"image-1","sourceHandle":"out"},{"id":"e2","source":"","target":"image-1"}]}`
	out := compactAgentGraph(graph, 1200)
	var g struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("压缩后应是合法 JSON：%v\n%s", err, out)
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("节点 / 连线数不对：%s", out)
	}
	if _, ok := g.Nodes[0]["position"]; ok {
		t.Fatalf("坐标应去掉：%s", out)
	}
	if g.Nodes[0]["kind"] != "text" || g.Nodes[1]["model"] != "gpt-image-2" {
		t.Fatalf("kind / model 应保留：%s", out)
	}
	if p, _ := g.Nodes[0]["prompt"].(string); len([]rune(p)) > 81 {
		t.Fatalf("提示词应截到 80 字：%d", len([]rune(p)))
	}
}

func TestCompactAgentGraphShrinksUnderMax(t *testing.T) {
	nodes := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		nodes = append(nodes, `{"id":"image-`+itoa(i)+`","data":{"kind":"image","label":"分镜`+itoa(i)+`","prompt":"这是第几个镜头的提示词，写得比较长一些"}}`)
	}
	graph := `{"nodes":[` + strings.Join(nodes, ",") + `],"edges":[{"source":"image-0","target":"image-1"},{"source":"image-58","target":"image-59"}]}`
	out := compactAgentGraph(graph, 600)
	if n := len([]rune(out)); n > 600 {
		t.Fatalf("应压到 600 字内，实际 %d", n)
	}
	var g struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
		Note  string           `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("截断后仍应是合法 JSON：%v\n%s", err, out)
	}
	if len(g.Nodes) == 0 || len(g.Nodes) >= 60 || !strings.Contains(g.Note, "已截断") {
		t.Fatalf("应按节点截断并注明：%s", out)
	}
	if len(g.Edges) != 1 {
		t.Fatalf("被截掉节点的连线也应去掉：%s", out)
	}
	if _, ok := g.Nodes[0]["prompt"]; ok {
		t.Fatalf("超限时应先去掉提示词：%s", out)
	}
}

func TestCompactAgentGraphFallsBackOnUnknownShape(t *testing.T) {
	if got := compactAgentGraph("", 100); got != "" {
		t.Fatalf("空图应为空：%q", got)
	}
	raw := strings.Repeat("x", 300)
	if got := compactAgentGraph(raw, 100); len([]rune(got)) > 101 {
		t.Fatalf("不认识的结构退回字符截断：%d", len([]rune(got)))
	}
}

func TestAppendAgentToolFeedClipsLine(t *testing.T) {
	feed := appendAgentToolFeed(nil, strings.Repeat("模", agentToolFeedLineMax+500))
	if n := len([]rune(feed[0])); n > agentToolFeedLineMax+1 {
		t.Fatalf("每条工具结果应封顶：%d", n)
	}
}
