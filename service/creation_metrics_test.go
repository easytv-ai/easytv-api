package service

import (
	"testing"
	"time"

	"easytv-api/model"
)

func TestRate(t *testing.T) {
	if Rate(1, 0) != nil {
		t.Fatal("zero denominator should be nil")
	}
	if v := Rate(2, 3); v == nil || *v != 66.7 {
		t.Fatalf("rate = %v", v)
	}
}

func TestSummarizeRetries(t *testing.T) {
	rows := []NodeTaskCount{
		{Type: "image", N: 1}, {Type: "image", N: 1}, {Type: "image", N: 2},
		{Type: "video", N: 3}, {Type: "video", N: 5}, {Type: "video", N: 9},
		{Type: "audio", N: 0},
	}
	s := SummarizeRetries(rows)
	if s.Nodes != 6 || s.Tasks != 21 || s.RetriedNodes != 4 || s.AvgPerNode != 3.5 {
		t.Fatalf("summary = %+v", s)
	}
	want := map[string]int64{"1": 2, "2": 1, "3": 1, "4-5": 1, "6+": 1}
	for _, b := range s.Buckets {
		if want[b.Label] != b.Nodes {
			t.Fatalf("bucket %s = %d", b.Label, b.Nodes)
		}
	}
	if len(s.ByType) != 2 || s.ByType[0].Type != "image" || s.ByType[0].AvgPerNode != 1.33 || s.ByType[1].AvgPerNode != 5.67 {
		t.Fatalf("by type = %+v", s.ByType)
	}
}

func TestSummarizeFirstCompose(t *testing.T) {
	s := SummarizeFirstCompose([]FirstComposeRow{
		{UserId: 1, FirstStatus: model.ComposeStatusDone, EverDone: 1},
		{UserId: 2, FirstStatus: model.ComposeStatusFailed, EverDone: 1},
		{UserId: 3, FirstStatus: model.ComposeStatusFailed, EverDone: 0},
		{UserId: 4, FirstStatus: model.ComposeStatusRunning, EverDone: 0},
	})
	if s.Users != 4 || s.Succeeded != 1 || s.Failed != 2 || s.EventuallyDone != 2 {
		t.Fatalf("first compose = %+v", s)
	}
}

func TestSummarizeRefMissing(t *testing.T) {
	s := SummarizeRefMissing([]RefMissingRow{
		{UserId: 1, Props: `{"source":"canvas_unlinked","count":3}`},
		{UserId: 1, Props: `{"source":"canvas_unlinked","count":1}`},
		{UserId: 2, Props: `{"source":"subject","count":2}`},
		{UserId: 2, Props: `{"source":"compose_clip","count":2}`},
		{UserId: 3, Props: `bad json`},
	})
	if s.Events != 5 || s.Items != 9 || s.Users != 3 || s.TaskEvents != 4 {
		t.Fatalf("summary = %+v", s)
	}
	if s.BySource[0].Source != "canvas_unlinked" || s.BySource[0].Events != 2 || s.BySource[0].Items != 4 || s.BySource[0].Users != 1 {
		t.Fatalf("by source = %+v", s.BySource)
	}
}

func TestFindStuckTasks(t *testing.T) {
	now := int64(10_000)
	tasks := []*model.GenerationTask{
		{Id: 1, Status: "running", CreatedAt: now - 3600, StartedAt: now - 3000, ProgressAt: now - 60},   // 刚有进展
		{Id: 2, Status: "running", CreatedAt: now - 3600, StartedAt: now - 3000, ProgressAt: now - 1200}, // 20 分钟没动
		{Id: 3, Status: "pending", CreatedAt: now - 700},                                                 // 排队 11 分钟
		{Id: 4, Status: "running", CreatedAt: now - 9000, StartedAt: now - 5400},                         // 老数据没有 progress_at
	}
	out := FindStuckTasks(tasks, now, 10)
	if len(out) != 3 || out[0].Id != 4 || out[0].IdleMinutes != 90 || out[1].Id != 2 || out[2].Id != 3 {
		t.Fatalf("stuck = %+v", out)
	}
}

func TestTopFailingModels(t *testing.T) {
	out := TopFailingModels([]ModelFailStat{
		{Model: "a", Total: 100, Failed: 5},
		{Model: "b", Total: 10, Failed: 5},
		{Model: "c", Total: 10, Failed: 0},
		{Model: "d", Total: 8, Failed: 7},
	}, 2)
	if len(out) != 2 || out[0].Model != "d" || out[1].Model != "b" || *out[1].FailRate != 50 {
		t.Fatalf("top = %+v", out)
	}
}

func TestBuildCreationDays(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	from := time.Date(2026, 9, 12, 0, 0, 0, 0, loc).Unix()
	key := func(d int) int64 { return (time.Date(2026, 9, d, 0, 0, 0, 0, loc).Unix() + 8*3600) / 86400 }
	days := BuildCreationDays(from, 3, loc, 8*3600,
		[]DayCount{{Day: key(12), Total: 5, Failed: 1}, {Day: key(14), Total: 2}},
		[]DayCount{{Day: key(13), Total: 3, Done: 2, Failed: 1}})
	if len(days) != 3 || days[0].Day != "09-12" || days[0].Tasks != 5 || days[0].TasksFailed != 1 ||
		days[1].Compose != 3 || days[1].ComposeDone != 2 || days[2].Day != "09-14" || days[2].Tasks != 2 {
		t.Fatalf("days = %+v", days)
	}
}
