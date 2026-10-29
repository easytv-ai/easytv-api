package tests

import (
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
)

func TestApplySkillGuideEmpty(t *testing.T) {
	// 没有技能、或技能没填契约时，提示词必须原样透传
	if got := service.ApplySkillGuide(nil, "画一只猫"); got != "画一只猫" {
		t.Fatalf("nil 技能应原样返回，got %q", got)
	}
	if got := service.ApplySkillGuide(&model.Skill{}, "画一只猫"); got != "画一只猫" {
		t.Fatalf("空契约应原样返回，got %q", got)
	}
	// 只有空白也算没填
	s := &model.Skill{Guide: "  \n ", OutputFormat: "\t"}
	if got := service.ApplySkillGuide(s, "画一只猫"); got != "画一只猫" {
		t.Fatalf("纯空白契约应原样返回，got %q", got)
	}
}

func TestApplySkillGuideOrder(t *testing.T) {
	s := &model.Skill{Guide: "先问清集数", OutputFormat: "每集一段"}
	got := service.ApplySkillGuide(s, "写个短剧")
	for _, want := range []string{"【执行规则】", "先问清集数", "【输出格式】", "每集一段", "【用户需求】", "写个短剧"} {
		if !strings.Contains(got, want) {
			t.Fatalf("结果里缺少 %q：\n%s", want, got)
		}
	}
	// 规则必须排在用户需求前面，否则模型会先读到用户内容
	if strings.Index(got, "【执行规则】") > strings.Index(got, "【用户需求】") {
		t.Fatalf("执行规则应排在用户需求之前：\n%s", got)
	}
	if strings.Index(got, "【输出格式】") > strings.Index(got, "【用户需求】") {
		t.Fatalf("输出格式应排在用户需求之前：\n%s", got)
	}
}

func TestApplySkillGuidePartial(t *testing.T) {
	// 只填了执行规则时不应该冒出空的【输出格式】段
	s := &model.Skill{Guide: "先问清集数"}
	got := service.ApplySkillGuide(s, "写个短剧")
	if strings.Contains(got, "【输出格式】") {
		t.Fatalf("没填输出格式就不该出现该段：\n%s", got)
	}
	// 用户需求为空时（比如只想让技能自我介绍）不该留一个空的【用户需求】
	if got := service.ApplySkillGuide(s, "   "); strings.Contains(got, "【用户需求】") {
		t.Fatalf("空提示词不该出现用户需求段：\n%s", got)
	}
}
