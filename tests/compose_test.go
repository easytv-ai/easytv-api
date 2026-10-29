package tests

import (
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
)

// TestBuildSRT 字幕按片段时长顺延，无台词的片段跳过编号但占时间。
func TestBuildSRT(t *testing.T) {
	srt := service.BuildSRT([]model.ComposeClip{
		{Duration: 5, Text: "第一句"},
		{Duration: 4, Text: ""},
		{Duration: 6, Text: "第三句"},
	})
	if !strings.Contains(srt, "1\n00:00:00,200 --> 00:00:04,800\n第一句") {
		t.Fatalf("first cue wrong:\n%s", srt)
	}
	if !strings.Contains(srt, "2\n00:00:09,200 --> 00:00:14,800\n第三句") {
		t.Fatalf("second cue should start after 9s:\n%s", srt)
	}
	if service.BuildSRT([]model.ComposeClip{{Duration: 5}}) != "" {
		t.Fatal("no text should produce empty srt")
	}
}

// TestComposeQueueCaps 单用户上限必须小于全局上限，否则一个用户就能占满队列。
func TestComposeQueueCaps(t *testing.T) {
	if model.ComposeMaxQueuePerUser <= 0 {
		t.Fatal("单用户队列上限必须是正数")
	}
	if model.ComposeMaxQueuePerUser >= model.ComposeMaxQueue {
		t.Fatalf("单用户上限 %d 不该 >= 全局上限 %d，否则限制形同虚设",
			model.ComposeMaxQueuePerUser, model.ComposeMaxQueue)
	}
}
