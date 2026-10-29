package model

import (
	"errors"
	"strings"
	"testing"
)

// 短剧复审（2026-09-15）回归：D02 stale / D03 超限与引用校验。

func reviewPlan(shots, chars, scenes int) Plan {
	p := Plan{}
	for i := 0; i < chars; i++ {
		p.Characters = append(p.Characters, PlanCharacter{Key: "c" + itoa(i+1), Name: "人物"})
	}
	for i := 0; i < scenes; i++ {
		p.Scenes = append(p.Scenes, PlanScene{Key: "s" + itoa(i+1), Name: "场景"})
	}
	for i := 0; i < shots; i++ {
		p.Shots = append(p.Shots, PlanShot{Prompt: "镜头", Seconds: 5, Characters: []string{"c1"}, Scene: "s1"})
	}
	return p
}

// TestPlanNormalizeRejectsOverLimit D03：13 镜 / 5 人 / 5 场景不再静默截成 12/4/4，而是明确报错且原稿不动。
func TestPlanNormalizeRejectsOverLimit(t *testing.T) {
	cases := []struct {
		name                  string
		shots, chars, scenes  int
		wantLimit, wantActual string
	}{
		{"shots", 13, 4, 4, "镜头最多 12 个", "当前 13 个"},
		{"characters", 12, 5, 4, "人物最多 4 个", "当前 5 个"},
		{"scenes", 12, 4, 5, "场景最多 4 个", "当前 5 个"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := reviewPlan(c.shots, c.chars, c.scenes)
			err := p.Normalize(65)
			if err == nil {
				t.Fatalf("超限应报错，实际 shots=%d chars=%d scenes=%d", len(p.Shots), len(p.Characters), len(p.Scenes))
			}
			if !errors.Is(err, ErrPlanLimit) || !strings.Contains(err.Error(), c.wantLimit) || !strings.Contains(err.Error(), c.wantActual) {
				t.Fatalf("err = %v，应含上限与实际数量", err)
			}
			if len(p.Shots) != c.shots || len(p.Characters) != c.chars || len(p.Scenes) != c.scenes {
				t.Fatalf("原稿被改动：shots=%d chars=%d scenes=%d", len(p.Shots), len(p.Characters), len(p.Scenes))
			}
		})
	}
	// 刚好到上限仍可用
	p := reviewPlan(ScreenplayMaxShots, ScreenplayMaxCharacters, ScreenplayMaxScenes)
	if err := p.Normalize(60); err != nil {
		t.Fatalf("12/4/4 应合法: %v", err)
	}
}

// TestPlanNormalizeRejectsMissingRefs D03：镜头引用了不存在的人物 / 场景要指出是哪一镜引用了什么。
func TestPlanNormalizeRejectsMissingRefs(t *testing.T) {
	p := reviewPlan(3, 2, 2)
	p.Shots[1].Characters = []string{"c1", "c5"}
	err := p.Normalize(30)
	if !errors.Is(err, ErrPlanRef) || !strings.Contains(err.Error(), "第 2 镜") || !strings.Contains(err.Error(), "人物 c5") {
		t.Fatalf("err = %v", err)
	}
	p = reviewPlan(3, 2, 2)
	p.Shots[2].Scene = "s9"
	err = p.Normalize(30)
	if !errors.Is(err, ErrPlanRef) || !strings.Contains(err.Error(), "第 3 镜") || !strings.Contains(err.Error(), "场景 s9") {
		t.Fatalf("err = %v", err)
	}
	// 场景留空（未指定）合法；人物里的空串被清掉
	p = reviewPlan(1, 1, 1)
	p.Shots[0].Scene = ""
	p.Shots[0].Characters = []string{"", "c1"}
	if err := p.Normalize(30); err != nil || len(p.Shots[0].Characters) != 1 {
		t.Fatalf("err = %v chars=%v", err, p.Shots[0].Characters)
	}
}

// TestScreenplayStale D02：已建画布且 revision != built_revision 才是 stale。
func TestScreenplayStale(t *testing.T) {
	sp := &Screenplay{Status: ScreenplayStatusBuilt, CanvasId: 3, Revision: 2, BuiltRevision: 1, Plan: "{}"}
	sp.FillView()
	if !sp.Stale {
		t.Fatal("改稿后应 stale")
	}
	sp.BuiltRevision = 2
	sp.FillView()
	if sp.Stale {
		t.Fatal("同步后不应 stale")
	}
	sp = &Screenplay{Status: ScreenplayStatusReady, Revision: 5, Plan: "{}"}
	sp.FillView()
	if sp.Stale {
		t.Fatal("没建画布不算 stale")
	}
}
