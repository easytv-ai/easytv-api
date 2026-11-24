package model

import (
	"strings"
	"testing"
)

func TestNormalizeUserIdentity(t *testing.T) {
	got, ok := NormalizeUserIdentity(" easytv_official ")
	if !ok || got != IdentityOfficial {
		t.Fatalf("official: %q %v", got, ok)
	}
	if got, ok := NormalizeUserIdentity("__none__"); !ok || got != "" {
		t.Fatalf("none: %q %v", got, ok)
	}
	if _, ok := NormalizeUserIdentity("hacker"); ok {
		t.Fatal("invalid identity should fail")
	}
}

func TestSettingAvatar(t *testing.T) {
	if got := SettingAvatar(`{"nickname":"a","avatar":" /upload/a.png "}`); got != "/upload/a.png" {
		t.Fatalf("avatar: %q", got)
	}
	if got := SettingAvatar(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := SettingAvatar("not-json"); got != "" {
		t.Fatalf("invalid: %q", got)
	}
}

func TestFillSettingAvatar(t *testing.T) {
	got, ok := FillSettingAvatar("", " https://wx.example/a.jpg ")
	if !ok || SettingAvatar(got) != "https://wx.example/a.jpg" {
		t.Fatalf("fill empty: %q %v", got, ok)
	}
	got, ok = FillSettingAvatar(`{"lang":"zh"}`, "https://wx.example/a.jpg")
	if !ok || !strings.Contains(got, `"lang":"zh"`) || SettingAvatar(got) != "https://wx.example/a.jpg" {
		t.Fatalf("keep keys: %q %v", got, ok)
	}
	if _, ok := FillSettingAvatar(`{"avatar":"/upload/mine.png"}`, "https://wx.example/a.jpg"); ok {
		t.Fatal("should not overwrite")
	}
	if _, ok := FillSettingAvatar("not-json", "https://wx.example/a.jpg"); ok {
		t.Fatal("invalid setting should stay")
	}
}

func TestUserIdentityTags(t *testing.T) {
	if got := UserIdentityTags("enterprise"); len(got) != 1 || got[0] != IdentityEnterprise {
		t.Fatalf("tags: %v", got)
	}
	if got := UserIdentityTags(""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}
