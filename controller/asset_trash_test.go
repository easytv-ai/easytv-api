package controller

import "testing"

func TestNormalizeAssetIds(t *testing.T) {
	got := normalizeAssetIds([]int{3, 0, 3, -1, 8, 8, 1})
	want := []int{3, 8, 1}
	if len(got) != len(want) {
		t.Fatalf("len %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if len(normalizeAssetIds(nil)) != 0 {
		t.Fatal("nil ids should be empty")
	}
}
