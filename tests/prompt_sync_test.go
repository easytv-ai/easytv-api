package tests

import (
	"strings"
	"testing"

	"easytv-api/service"
)

func TestParseMarkdownPrompts(t *testing.T) {
	md := "# 仓库标题\n\n介绍文字\n\n" +
		"## 电影感人像\n\n" +
		"![效果图](https://example.com/a.png)\n\n" +
		"```\nA cinematic portrait, 85mm, shallow depth of field\n```\n\n" +
		"### 无代码块的条目\n\n" +
		"直接写在段落里的提示词\n\n" +
		"## 缺正文的条目\n\n"
	items := service.ParseMarkdownPrompts(md)
	if len(items) != 2 {
		t.Fatalf("应解析出 2 条（缺正文的要跳过），got %d: %+v", len(items), items)
	}
	if items[0].Title != "电影感人像" {
		t.Fatalf("标题错了：%q", items[0].Title)
	}
	if !strings.Contains(items[0].Content, "cinematic portrait") {
		t.Fatalf("正文应取代码块内容：%q", items[0].Content)
	}
	if items[0].Cover != "https://example.com/a.png" {
		t.Fatalf("封面应取段内第一张图：%q", items[0].Cover)
	}
	// 没有代码块时退回段落纯文本，且图片语法要被清掉
	if items[1].Content != "直接写在段落里的提示词" {
		t.Fatalf("无代码块的正文错了：%q", items[1].Content)
	}
	// 一级标题是仓库名，不该被当成提示词
	for _, item := range items {
		if item.Title == "仓库标题" {
			t.Fatal("一级标题不应被解析成条目")
		}
	}
}

func TestParseJSONPrompts(t *testing.T) {
	raw := `[
		{"title": "赛博城市", "prompt": "neon city at night", "tags": ["科幻", "夜景"]},
		{"name": "产品图", "content": "white background product shot", "cover_url": "https://example.com/b.png"},
		{"title": "只有标题"},
		{"content": "只有正文"}
	]`
	items, err := service.ParseJSONPrompts(raw)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(items) != 2 {
		t.Fatalf("缺标题或缺正文的都该跳过，got %d: %+v", len(items), items)
	}
	if items[0].Title != "赛博城市" || len(items[0].Tags) != 2 {
		t.Fatalf("第一条解析错了：%+v", items[0])
	}
	// name / content / cover_url 这套别名也要认
	if items[1].Title != "产品图" || items[1].Content == "" || items[1].Cover == "" {
		t.Fatalf("字段别名没认出来：%+v", items[1])
	}
}

func TestParseJSONPromptsBadInput(t *testing.T) {
	if _, err := service.ParseJSONPrompts("not json"); err == nil {
		t.Fatal("非 JSON 应该报错")
	}
	// 合法 JSON 但不是数组，也该报错而不是 panic
	if _, err := service.ParseJSONPrompts(`{"title":"x"}`); err == nil {
		t.Fatal("对象而非数组应该报错")
	}
}
