package service

import "testing"

func TestSubtitleFromGraphNode(t *testing.T) {
	graph := `{"nodes":[
		{"id":"text-3","data":{"kind":"text","content":"1\n00:00:00,000 --> 00:00:02,000\n你好\n"}},
		{"id":"text-4","data":{"kind":"text","content":"只是普通文本"}},
		{"id":"image-1","data":{"kind":"image","content":"https://x/a.png"}}]}`
	if srt, err := subtitleFromGraphNode(graph, "text-3"); err != nil || srt == "" {
		t.Fatalf("SRT 节点应取到：%v", err)
	}
	if _, err := subtitleFromGraphNode(graph, "text-4"); err == nil {
		t.Fatal("没有时间轴的文本应报错")
	}
	if _, err := subtitleFromGraphNode(graph, "image-1"); err == nil {
		t.Fatal("非文本节点应报错")
	}
	if _, err := subtitleFromGraphNode(graph, "text-9"); err == nil {
		t.Fatal("不存在的节点应报错")
	}
}
