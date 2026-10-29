package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
)

const samplePlan = "```json\n{\"title\":\"深夜修 bug\",\"style\":\"电影感 · 冷色调\",\"aspect\":\"16:9\"," +
	"\"characters\":[{\"key\":\"c1\",\"name\":\"程序员阿杰\",\"look\":\"黑框眼镜，灰色卫衣\"}]," +
	"\"scenes\":[{\"key\":\"s1\",\"name\":\"深夜办公室\",\"look\":\"只有显示器的冷光\"}]," +
	"\"shots\":[{\"no\":1,\"scene\":\"s1\",\"characters\":[\"c1\"],\"prompt\":\"阿杰盯着满屏报错\",\"motion\":\"缓慢推近\",\"line\":\"又是这个 bug\",\"seconds\":5}," +
	"{\"no\":2,\"scene\":\"s1\",\"characters\":[\"c1\"],\"prompt\":\"敲下回车，测试全绿\",\"motion\":\"\",\"line\":\"\",\"seconds\":99}]}\n```"

// TestParsePlanText 容忍代码块包裹；Normalize 修正越界时长并补默认镜头运动。
func TestParsePlanText(t *testing.T) {
	plan, err := service.ParsePlanText(samplePlan)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := plan.Normalize(30); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(plan.Shots) != 2 || plan.Shots[1].Seconds < 3 || plan.Shots[1].Seconds > 10 {
		t.Fatalf("shots normalized wrong: %+v", plan.Shots)
	}
	if plan.TotalSeconds() <= 0 {
		t.Fatal("total seconds should be positive")
	}
	if _, err := service.ParsePlanText("不是 JSON"); err == nil {
		t.Fatal("garbage should fail")
	}
	empty := &model.Plan{}
	if err := empty.Normalize(30); err == nil {
		t.Fatal("plan without shots should fail")
	}
}

// TestBuildPlanGraph 节点 id 必须是 kind-N，分镜图连角色图、视频连分镜图、有台词才有配音节点。
func TestBuildPlanGraph(t *testing.T) {
	plan, _ := service.ParsePlanText(samplePlan)
	_ = plan.Normalize(30)
	raw := service.BuildPlanGraph(plan, service.BuildModels{Image: "img-m", Video: "vid-m", Audio: "aud-m"})
	var g struct {
		Nodes []struct {
			Id   string         `json:"id"`
			Data map[string]any `json:"data"`
		} `json:"nodes"`
		Edges []struct{ Source, Target string } `json:"edges"`
	}
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatalf("graph json: %v", err)
	}
	// 1 文本 + 1 角色 + 2 分镜图 + 2 视频 + 1 配音
	if len(g.Nodes) != 7 {
		t.Fatalf("want 7 nodes got %d", len(g.Nodes))
	}
	kinds := map[string]int{}
	for _, n := range g.Nodes {
		kind, _ := n.Data["kind"].(string)
		kinds[kind]++
		if !strings.HasPrefix(n.Id, kind+"-") {
			t.Fatalf("node id %s must be kind-N", n.Id)
		}
	}
	if kinds["image"] != 3 || kinds["video"] != 2 || kinds["audio"] != 1 || kinds["text"] != 1 {
		t.Fatalf("kinds: %v", kinds)
	}
	hasEdge := func(s, d string) bool {
		for _, e := range g.Edges {
			if e.Source == s && e.Target == d {
				return true
			}
		}
		return false
	}
	if !hasEdge("image-1", "image-2") || !hasEdge("image-2", "video-1") || !hasEdge("video-1", "audio-1") || !hasEdge("text-1", "image-1") {
		t.Fatalf("edges wrong: %+v", g.Edges)
	}
	if len(raw) > model.CanvasGraphMaxBytes {
		t.Fatal("graph too large")
	}
}
