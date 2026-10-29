package tests

import (
	"strings"
	"testing"

	"easytv-api/model"
)

func TestTrackEventNameValid(t *testing.T) {
	ok := []string{"register", "first_video", "page_view", "a1"}
	bad := []string{"", "Register", "page-view", "页面", strings.Repeat("a", 41)}
	for _, n := range ok {
		if !model.TrackEventNameValid(n) {
			t.Errorf("expected valid: %q", n)
		}
	}
	for _, n := range bad {
		if model.TrackEventNameValid(n) {
			t.Errorf("expected invalid: %q", n)
		}
	}
}

func TestAttributionNormalize(t *testing.T) {
	a := model.Attribution{
		Source:   "  bilibili ",
		Campaign: strings.Repeat("c", 100),
		AffCode:  " ab12cd ",
		AnonId:   "anon-1",
	}.Normalize()
	if a.Source != "bilibili" {
		t.Errorf("source not trimmed: %q", a.Source)
	}
	if len(a.Campaign) != 64 {
		t.Errorf("campaign not clipped: %d", len(a.Campaign))
	}
	if a.AffCode != "AB12CD" {
		t.Errorf("aff_code not upper-cased: %q", a.AffCode)
	}
	if a.AnonId != "anon-1" {
		t.Errorf("anon_id changed: %q", a.AnonId)
	}
}

func TestPropsJSON(t *testing.T) {
	if model.PropsJSON(nil) != "" {
		t.Error("empty props should encode to empty string")
	}
	if got := model.PropsJSON(map[string]any{"amount": 39.0}); got != `{"amount":39}` {
		t.Errorf("unexpected json: %s", got)
	}
	if model.PropsJSON(map[string]any{"big": strings.Repeat("x", 3000)}) != "" {
		t.Error("oversized props should be dropped")
	}
}

func TestMaskNickname(t *testing.T) {
	cases := map[string]string{"": "用户", "小": "小*", "小王": "小*", "张三丰": "张**丰", "leida2026": "l**6"}
	for in, want := range cases {
		if got := model.MaskNickname(in); got != want {
			t.Errorf("%q: want %q got %q", in, want, got)
		}
	}
}
