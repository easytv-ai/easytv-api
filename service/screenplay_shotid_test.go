package service

import (
	"encoding/json"
	"testing"

	"easytv-api/model"
)

const shotIdPlanJSON = `{"title":"三镜","style":"","aspect":"16:9",
"characters":[{"key":"c1","name":"甲","look":"红衣"}],
"scenes":[{"key":"s1","name":"屋内","look":"暖光"}],
"shots":[{"no":1,"scene":"s1","characters":["c1"],"prompt":"开门","line":"来了","seconds":4},
{"no":2,"scene":"s1","characters":["c1"],"prompt":"坐下","line":"坐","seconds":4},
{"no":3,"scene":"s1","characters":["c1"],"prompt":"喝茶","line":"好茶","seconds":4}]}`

// TestPlanShotIds Normalize 给每镜稳定 id，已有的保留，重复 / 超长的重发。
func TestPlanShotIds(t *testing.T) {
	plan, err := ParsePlanText(shotIdPlanJSON)
	if err != nil || plan.Normalize(12) != nil {
		t.Fatalf("plan: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range plan.Shots {
		if len(s.Id) != 11 || s.Id[:3] != "sh_" || ids[s.Id] {
			t.Fatalf("id 不合法或重复：%q", s.Id)
		}
		ids[s.Id] = true
	}
	keep := plan.Shots[0].Id
	plan.Shots[1].Id = keep // 重复
	plan.Shots[2].Id = "x123456789012345678901234567890123"
	if err := plan.Normalize(12); err != nil {
		t.Fatal(err)
	}
	if plan.Shots[0].Id != keep || plan.Shots[1].Id == keep || len(plan.Shots[2].Id) != 11 {
		t.Fatalf("ids = %v", []string{plan.Shots[0].Id, plan.Shots[1].Id, plan.Shots[2].Id})
	}
}

// TestSyncPlanGraphStableIds 删中间一镜：第 3 镜按 id 认领原节点并改号为 2，内容 / 产物不动；原第 2 镜节点列为 removed，不被顶掉覆盖。
func TestSyncPlanGraphStableIds(t *testing.T) {
	plan, err := ParsePlanText(shotIdPlanJSON)
	if err != nil || plan.Normalize(12) != nil {
		t.Fatalf("plan: %v", err)
	}
	m := BuildModels{Image: "img", Video: "vid", Audio: "aud"}
	graph := BuildPlanGraph(plan, m)
	byId, _ := graphNodesById(t, graph)
	if byId["video-3"]["data"].(map[string]any)["sp"].(map[string]any)["shot_id"] != plan.Shots[2].Id {
		t.Fatal("建画布时应写入 shot_id")
	}
	byId["video-3"]["data"].(map[string]any)["assetId"] = 33
	byId["video-2"]["data"].(map[string]any)["assetId"] = 22
	var g map[string]any
	_ = json.Unmarshal([]byte(graph), &g)
	nodes := []any{}
	for _, id := range []string{"text-1", "image-1", "image-2", "video-1", "audio-1", "image-3", "video-2", "audio-2", "image-4", "video-3", "audio-3"} {
		nodes = append(nodes, byId[id])
	}
	g["nodes"] = nodes
	raw, _ := json.Marshal(g)

	edited := *plan
	edited.Shots = []model.PlanShot{plan.Shots[0], plan.Shots[2]}
	edited.Shots[1].Line = "这茶真好"
	if err := edited.Normalize(12); err != nil {
		t.Fatal(err)
	}
	if edited.Shots[1].No != 2 || edited.Shots[1].Id != plan.Shots[2].Id {
		t.Fatalf("重排后 id 应保留：%+v", edited.Shots[1])
	}
	out, res, err := syncPlanGraph(&edited, string(raw), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SyncedShots) != 2 || len(res.AddedShots) != 0 || len(res.RemovedShots) != 1 || res.RemovedShots[0] != 2 {
		t.Fatalf("res = %+v", res)
	}
	byId, _ = graphNodesById(t, out)
	v3 := byId["video-3"]["data"].(map[string]any)
	if v3["line"] != "这茶真好" || toInt(v3["assetId"]) != 33 || v3["label"] != "镜头 2" || toInt(v3["sp"].(map[string]any)["shot"]) != 2 {
		t.Fatalf("原第 3 镜应按 id 认领并改号为 2：%+v", v3)
	}
	v2 := byId["video-2"]["data"].(map[string]any)
	if v2["line"] != "坐" || toInt(v2["assetId"]) != 22 || v2["label"] != "镜头 2" {
		t.Fatalf("被删的第 2 镜节点不应被覆盖：%+v", v2)
	}
	a3 := byId["audio-3"]["data"].(map[string]any)
	if a3["text"] != "这茶真好" || a3["label"] != "配音 2" {
		t.Fatalf("配音应跟着镜头走：%+v", a3)
	}
	if byId["image-4"]["data"].(map[string]any)["label"] != "分镜 2" {
		t.Fatal("分镜标签应改号")
	}
}

// TestSyncPlanGraphLegacyNoIds 老画布（节点没有 shot_id）或客户端丢了 id：一个都对不上时退回按序号匹配，不会整份全删全建。
func TestSyncPlanGraphLegacyNoIds(t *testing.T) {
	plan, err := ParsePlanText(shotIdPlanJSON)
	if err != nil || plan.Normalize(12) != nil {
		t.Fatalf("plan: %v", err)
	}
	m := BuildModels{Image: "img", Video: "vid", Audio: "aud"}
	graph := BuildPlanGraph(plan, m)
	fresh, _ := ParsePlanText(shotIdPlanJSON) // 重新解析 → 全新 id
	fresh.Shots[1].Line = "请坐"
	_ = fresh.Normalize(12)
	out, res, err := syncPlanGraph(fresh, graph, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SyncedShots) != 3 || len(res.AddedShots) != 0 || len(res.RemovedShots) != 0 {
		t.Fatalf("res = %+v", res)
	}
	byId, _ := graphNodesById(t, out)
	if byId["video-2"]["data"].(map[string]any)["line"] != "请坐" {
		t.Fatal("序号模式下第 2 镜应被更新")
	}
	if byId["video-2"]["data"].(map[string]any)["sp"].(map[string]any)["shot_id"] != fresh.Shots[1].Id {
		t.Fatal("同步后节点应改记新的 shot_id")
	}
}
