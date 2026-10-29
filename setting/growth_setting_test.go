package setting

import "testing"

func TestGrowthTaskKeysOmitBindWeChat(t *testing.T) {
	if len(GrowthTaskKeys) != 4 {
		t.Fatalf("新手任务应为 4 条，得到 %d: %v", len(GrowthTaskKeys), GrowthTaskKeys)
	}
	for _, key := range GrowthTaskKeys {
		if key == GrowthTaskKeyBindWeChat {
			t.Fatal("bind_wechat 不应再出现在任务列表")
		}
	}
}
