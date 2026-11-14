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

func TestCloneSeenWork(t *testing.T) {
	// 真实事件属性：键按字母序，work_id 在最后，后面跟的是右花括号
	real := `{"canvas_id":35,"work_id":7}`
	if !cloneSeenWork([]string{`{"work_id":3}`, real}, 7) {
		t.Fatal("同一作品当天做过，应命中")
	}
	if cloneSeenWork([]string{real}, 70) {
		t.Fatal("作品 70 不该被作品 7 命中")
	}
	if cloneSeenWork([]string{`{"work_id":17}`, `{"canvas_id":7}`, `坏数据`, ``}, 7) {
		t.Fatal("别的作品、别的字段、坏数据都不算")
	}
	if !cloneSeenWork([]string{`{"work_id":"7"}`}, 7) {
		t.Fatal("字符串形式的编号也要认")
	}
	if cloneSeenWork(nil, 7) {
		t.Fatal("当天没有事件")
	}
	if !cloneSeenWork(make([]string, cloneDedupScan), 7) {
		t.Fatal("事件条数到顶按已做过算")
	}
}
