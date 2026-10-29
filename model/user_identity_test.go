package model

import "testing"

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

func TestUserIdentityTags(t *testing.T) {
	if got := UserIdentityTags("enterprise"); len(got) != 1 || got[0] != IdentityEnterprise {
		t.Fatalf("tags: %v", got)
	}
	if got := UserIdentityTags(""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}
