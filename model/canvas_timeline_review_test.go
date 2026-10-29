package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// 短剧复审（2026-09-15）回归：D04 同一素材多次使用 / D07 BGM 音量 0。

// TestTimelineKeepsSplitTake D04：A / B / A 三条片段归一化后仍是三条，各自有独立 id 且入出点独立。
func TestTimelineKeepsSplitTake(t *testing.T) {
	tl := Timeline{Clips: []TimelineClip{
		{NodeId: "video-1", Enabled: true, In: 0, Out: 1},
		{NodeId: "video-2", Enabled: true, In: 0, Out: 1},
		{NodeId: "video-1", Enabled: true, In: 2, Out: 3},
	}}
	tl.Normalize()
	if len(tl.Clips) != 3 {
		t.Fatalf("A/B/A 保存后只剩 %d 条", len(tl.Clips))
	}
	ids := map[string]bool{}
	for _, c := range tl.Clips {
		if !strings.HasPrefix(c.Id, "c_") || len(c.Id) != 10 {
			t.Fatalf("片段 id 应为 c_ + 8 位随机串: %q", c.Id)
		}
		ids[c.Id] = true
	}
	if len(ids) != 3 {
		t.Fatalf("id 应各不相同: %v", ids)
	}
	if tl.Clips[0].NodeId != "video-1" || tl.Clips[2].NodeId != "video-1" || tl.Clips[0].In != 0 || tl.Clips[2].In != 2 || tl.Clips[2].Out != 3 {
		t.Fatalf("入出点应独立保留: %+v", tl.Clips)
	}
	// 再次归一化（相当于保存 → 刷新 → 再保存）id 不变、条数不变
	before := []string{tl.Clips[0].Id, tl.Clips[1].Id, tl.Clips[2].Id}
	raw, _ := json.Marshal(tl)
	var again Timeline
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	again.Normalize()
	if len(again.Clips) != 3 || again.Clips[0].Id != before[0] || again.Clips[1].Id != before[1] || again.Clips[2].Id != before[2] {
		t.Fatalf("重载后 id 应稳定: %+v", again.Clips)
	}
}

// TestTimelineDedupById D04：去重按片段 id 而不是 node_id；重复 id 的后一条分配新 id 而不是丢掉。
func TestTimelineDedupById(t *testing.T) {
	tl := Timeline{Clips: []TimelineClip{
		{Id: "c_aaaaaaaa", NodeId: "video-1", Enabled: true},
		{Id: "c_aaaaaaaa", NodeId: "video-1", Enabled: true, In: 1},
		{Id: "", NodeId: "", Enabled: true},
	}}
	tl.Normalize()
	if len(tl.Clips) != 2 || tl.Clips[0].Id != "c_aaaaaaaa" || tl.Clips[1].Id == "c_aaaaaaaa" || tl.Clips[1].In != 1 {
		t.Fatalf("clips = %+v", tl.Clips)
	}
}

// TestTimelineBgmVolumeZero D07：字段缺失回落 0.2；明确传 0 保持 0（静音）；正常值照旧钳到 [0,2]。
func TestTimelineBgmVolumeZero(t *testing.T) {
	cases := []struct {
		body string
		want float64
	}{
		{`{"bgm_asset_id":5,"clips":[]}`, 0.2},
		{`{"bgm_asset_id":5,"bgm_volume":0,"clips":[]}`, 0},
		{`{"bgm_asset_id":5,"bgm_volume":0.5,"clips":[]}`, 0.5},
		{`{"bgm_asset_id":5,"bgm_volume":9,"clips":[]}`, 2},
		{`{"bgm_asset_id":5,"bgm_volume":-1,"clips":[]}`, 0.2},
	}
	for _, c := range cases {
		var tl Timeline
		if err := json.Unmarshal([]byte(c.body), &tl); err != nil {
			t.Fatal(err)
		}
		tl.Normalize()
		if tl.BgmVolume != c.want {
			t.Fatalf("%s → bgm_volume=%v, want %v", c.body, tl.BgmVolume, c.want)
		}
	}
	// Go 侧直接构造（比如缺省时间线）显式给 0.2，不受哨兵影响
	tl := Timeline{BgmVolume: 0.2}
	tl.Normalize()
	if tl.BgmVolume != 0.2 {
		t.Fatalf("bgm_volume=%v", tl.BgmVolume)
	}
}
