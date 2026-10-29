package controller

import "testing"

// TestSnapDuration 素材续写的秒数落到模型档位（API.md 30）。
func TestSnapDuration(t *testing.T) {
	cases := []struct {
		seconds int
		allowed []int
		want    int
	}{
		{5, []int{6, 7, 8, 9, 10}, 6},
		{12, []int{6, 7, 8, 9, 10}, 10},
		{7, []int{4, 8, 12}, 8},
		{6, []int{4, 8, 12}, 8},
		{5, nil, 5},
	}
	for _, c := range cases {
		if got := snapDuration(c.seconds, c.allowed); got != c.want {
			t.Errorf("snapDuration(%d, %v) = %d, want %d", c.seconds, c.allowed, got, c.want)
		}
	}
}
