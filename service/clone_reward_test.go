package service

import "testing"

func TestRewardWorkCloneSkipsSelfAndInvalid(t *testing.T) {
	RewardWorkClone(0, 8, 7, "x")
	RewardWorkClone(2, 0, 7, "x")
	RewardWorkClone(2, 8, 0, "x")
	RewardWorkClone(8, 8, 7, "自己的作品")
}

func TestCloneRewardPoints(t *testing.T) {
	if got := cloneRewardPoints(10, 0, 100); got != 10 {
		t.Fatalf("未达上限: %d", got)
	}
	if got := cloneRewardPoints(10, 95, 100); got != 5 {
		t.Fatalf("补差额: %d", got)
	}
	if got := cloneRewardPoints(10, 100, 100); got != 0 {
		t.Fatalf("已满: %d", got)
	}
	if got := cloneRewardPoints(0, 0, 100); got != 0 {
		t.Fatalf("关闭: %d", got)
	}
	if got := cloneRewardPoints(10, 0, 0); got != 10 {
		t.Fatalf("无上限: %d", got)
	}
}
