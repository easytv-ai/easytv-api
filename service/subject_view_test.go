package service

import (
	"strings"
	"testing"

	"easytv-api/model"
)

func TestSubjectPromptLocksIdentity(t *testing.T) {
	views := subjectViewPrompts[model.SubjectKindCharacter]
	if len(views) != 3 {
		t.Fatalf("角色参考图应是 3 张，得到 %d", len(views))
	}
	for _, v := range views {
		if strings.Contains(v.Suffix, "角色设定图") || strings.Contains(v.Suffix, "三连") {
			t.Fatalf("%s 的提示词会把模型带成拼贴：%s", v.Label, v.Suffix)
		}
	}
	plain := subjectPrompt("短发，黑色工装", views[1].Suffix, false)
	if strings.Contains(plain, "参考图") {
		t.Fatalf("没有参考图时不应要求沿用参考图：%s", plain)
	}
	locked := subjectPrompt("短发，黑色工装", views[1].Suffix, true)
	if !strings.Contains(locked, "参考图中的同一个主体") {
		t.Fatalf("有参考图时应锁住同一个人：%s", locked)
	}
}

func TestSubjectWaitNode(t *testing.T) {
	if got := subjectWaitNode(`{"count":1}`); got != "" {
		t.Fatalf("没有挂起标记应为空，得到 %q", got)
	}
	if got := subjectWaitNode(`{"count":1,"subject_wait":"subject-8-正面"}`); got != "subject-8-正面" {
		t.Fatalf("挂起标记解析错误：%q", got)
	}
	prefix, ok := subjectNodePrefix("subject-8-侧面")
	if !ok || prefix != "subject-8-" {
		t.Fatalf("节点前缀解析错误：%q %v", prefix, ok)
	}
	if _, ok := subjectNodePrefix("canvas-1"); ok {
		t.Fatal("非主体节点不应识别为参考图任务")
	}
}
