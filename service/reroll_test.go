package service

import (
	"reflect"
	"testing"

	"easytv-api/model"
)

func TestNormalizeRerollReasons(t *testing.T) {
	got := NormalizeRerollReasons([]string{"off_prompt", " identity ", "identity", "nope", ""})
	if want := []string{"identity", "off_prompt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := NormalizeRerollReasons(nil); len(got) != 0 {
		t.Fatalf("nil 应得空：%v", got)
	}
}

func TestMarkRerollSkips(t *testing.T) {
	// 续写、文本、没挂节点的任务都不算重抽，也不该去查库（model.DB 为 nil，查了会 panic）
	for _, task := range []*model.GenerationTask{
		{Type: model.TaskTypeVideo, CanvasId: 1, NodeId: "v1", ParentTaskId: 9},
		{Type: model.TaskTypeText, CanvasId: 1, NodeId: "t1"},
		{Type: model.TaskTypeImage, CanvasId: 0, NodeId: "i1"},
		{Type: model.TaskTypeImage, CanvasId: 1},
	} {
		MarkReroll(task, []string{"identity"})
		if task.RerollOf != 0 || task.RerollReason != "" {
			t.Fatalf("不该标重抽：%+v", task)
		}
	}
	// 已知被顶替的任务（重拍）直接记原因
	task := &model.GenerationTask{Type: model.TaskTypeVideo, CanvasId: 1, NodeId: "v1", RerollOf: 7}
	MarkReroll(task, []string{"duration", "bogus", "identity"})
	if task.RerollOf != 7 || task.RerollReason != "identity,duration" {
		t.Fatalf("got %+v", task)
	}
}

func TestSummarizeRerolls(t *testing.T) {
	nodes := []RerollNodeRow{
		{Type: "video", N: 1, Points: 30, LastCost: 30},
		{Type: "video", N: 3, Points: 90, LastCost: 30},
		{Type: "image", N: 2, Points: 6, LastCost: 2},
		{Type: "image", N: 0},
	}
	models := []RerollModelRow{
		{Model: "a", Type: "video", Total: 4, Rerolled: 2},
		{Model: "b", Type: "image", Total: 2, Rerolled: 2},
		{Model: "", Total: 3},
	}
	m := SummarizeRerolls(nodes, models, []string{"identity,duration", "identity", ""})
	if m.Nodes != 3 || m.Succeeded != 6 || m.FirstPassNodes != 1 || m.AvgPerNode != 2 {
		t.Fatalf("节点汇总不对：%+v", m)
	}
	if m.FirstPassRate == nil || *m.FirstPassRate != 33.3 {
		t.Fatalf("一次留用率 %v", m.FirstPassRate)
	}
	if m.SpentPoints != 126 || m.WastedPoints != 64 || *m.WastedRate != 50.8 {
		t.Fatalf("积分 %d / %d / %v", m.SpentPoints, m.WastedPoints, *m.WastedRate)
	}
	if len(m.ByType) != 2 || m.ByType[0].Type != "image" || m.ByType[1].AvgPerNode != 2 || *m.ByType[1].FirstPassRate != 50 {
		t.Fatalf("分类型 %+v", m.ByType)
	}
	if len(m.ByModel) != 2 || m.ByModel[0].Model != "a" || *m.ByModel[0].KeepRate != 50 || *m.ByModel[0].PullsPerKeep != 2 {
		t.Fatalf("分模型 %+v", m.ByModel)
	}
	if m.ByModel[1].PullsPerKeep != nil || *m.ByModel[1].KeepRate != 0 {
		t.Fatalf("全被顶替的模型不给每留用抽数：%+v", m.ByModel[1])
	}
	if m.ReasonTasks != 2 || len(m.Reasons) != 2 || m.Reasons[0].Reason != "identity" || m.Reasons[0].Count != 2 {
		t.Fatalf("原因 %+v", m.Reasons)
	}
}

func TestAssembleModelKeep(t *testing.T) {
	got := assembleModelKeep([]RerollModelRow{
		{Model: "a", Type: "video", Total: 8, Rerolled: 3},
		{Model: "a", Type: "image", Total: 4, Rerolled: 0},
		{Model: "few", Total: 9, Rerolled: 1},
		{Model: "none", Total: 12, Rerolled: 12},
	})
	if len(got) != 1 || got["a"] == nil {
		t.Fatalf("只有样本够且有留用的模型给数：%v", got)
	}
	if a := got["a"]; a.Total != 12 || a.KeepRate != 75 || a.PullsPerKeep != 1.33 {
		t.Fatalf("got %+v", a)
	}
}
