package service

import (
	"strings"
	"testing"
	"time"
)

func TestAssembleModelHealth(t *testing.T) {
	rows := []modelHealthRow{
		{Model: "kling-v1", Total: 10, Failed: 9},
		{Model: "sora-2", Total: 20, Failed: 2},
		{Model: "new-model", Total: 2, Failed: 2},
		{Model: "", Total: 5, Failed: 5},
	}
	errs := []modelHealthErrorRow{
		{Model: "kling-v1", Message: "Image format is invalid", Count: 6},
		{Model: "kling-v1", Message: "网关返回 502", Count: 2},
		{Model: "kling-v1", Message: "超时", Count: 1},
		{Model: "kling-v1", Message: "其它", Count: 1},
		{Model: "ghost", Message: "x", Count: 1},
	}
	out := assembleModelHealth(rows, errs)
	if len(out) != 3 {
		t.Fatalf("空模型名应跳过：%d", len(out))
	}
	k := out["kling-v1"]
	if !k.Warn || *k.FailRate < 0.89 || len(k.TopErrors) != 3 || k.TopErrors[0].Message != "Image format is invalid" {
		t.Fatalf("kling 应预警且失败原因取 top3 按次数排：%+v", k)
	}
	if out["sora-2"].Warn {
		t.Fatal("10% 失败率不该预警")
	}
	if out["new-model"].Warn {
		t.Fatal("样本不足 5 个不该预警")
	}
	if out["sora-2"].TopErrors == nil {
		t.Fatal("没有失败原因也要给空数组，前端不判 null")
	}
}

func TestModelHealthSuggestOffline(t *testing.T) {
	out := assembleModelHealth([]modelHealthRow{
		{Model: "dead", Total: 12, Failed: 12},
		{Model: "few", Total: 9, Failed: 9},
		{Model: "bad", Total: 20, Failed: 17},
	}, nil)
	if !out["dead"].SuggestOffline || out["few"].SuggestOffline || out["bad"].SuggestOffline {
		t.Fatalf("建议下架要样本 ≥10 且失败率 ≥90%%：dead=%v few=%v bad=%v", out["dead"].SuggestOffline, out["few"].SuggestOffline, out["bad"].SuggestOffline)
	}
	if !out["bad"].Warn {
		t.Fatal("85% 仍应预警")
	}
}

func TestPickModelHealthAlerts(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rate := 100.0
	mk := func(name string, offline bool) *ModelHealthStat {
		return &ModelHealthStat{Model: name, Total: 12, Failed: 12, FailRate: &rate, SuggestOffline: offline}
	}
	stats := map[string]*ModelHealthStat{
		"b-new":     mk("b-new", true),
		"a-new":     mk("a-new", true),
		"recent":    mk("recent", true),
		"stale":     mk("stale", true),
		"disabled":  mk("disabled", true),
		"recovered": mk("recovered", false),
	}
	active := map[string]bool{"a-new": true, "b-new": true, "recent": true, "stale": true, "recovered": true}
	notified := map[string]int64{
		"recent":    now.Add(-24 * time.Hour).Unix(),
		"stale":     now.Add(-8 * 24 * time.Hour).Unix(),
		"recovered": now.Add(-24 * time.Hour).Unix(),
	}
	alerts, next := pickModelHealthAlerts(stats, active, notified, now)
	var names []string
	for _, a := range alerts {
		names = append(names, a.Model)
	}
	if strings.Join(names, ",") != "a-new,b-new,stale" {
		t.Fatalf("新坏的 + 冷却已过的要提醒，按名字排，停用的不提醒：%v", names)
	}
	if next["recent"] != notified["recent"] || next["stale"] != now.Unix() || next["a-new"] != now.Unix() {
		t.Fatalf("冷却中保留原时间，提醒过的记当前时间：%v", next)
	}
	if _, ok := next["recovered"]; ok {
		t.Fatal("恢复正常的模型要清掉记录，再坏马上提醒")
	}
	if _, ok := next["disabled"]; ok {
		t.Fatal("停用的模型不记录")
	}
	title, content := modelHealthAlertMessage(alerts)
	if !strings.Contains(title, "3 个模型") || !strings.Contains(content, "a-new 失败 12/12（100%）") {
		t.Fatalf("文案：%s / %s", title, content)
	}
}
