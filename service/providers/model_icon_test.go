package providers

import (
	"testing"
	"time"
)

func TestIconFromDetail(t *testing.T) {
	if got := iconFromDetail(nil); got != "" {
		t.Fatalf("nil detail = %q", got)
	}
	if got := iconFromDetail(&GatewayModelDetail{Icon: " ByteDance.Color "}); got != "ByteDance.Color" {
		t.Fatalf("model icon = %q", got)
	}
	got := iconFromDetail(&GatewayModelDetail{Vendor: &GatewayVendor{Icon: "OpenAI.Color"}})
	if got != "OpenAI.Color" {
		t.Fatalf("vendor icon = %q", got)
	}
	got = iconFromDetail(&GatewayModelDetail{Icon: "Kling.Color", Vendor: &GatewayVendor{Icon: "Other"}})
	if got != "Kling.Color" {
		t.Fatalf("model icon should win, got %q", got)
	}
}

func TestModelFactsUsesCache(t *testing.T) {
	const name = "seedance-2.0-icon-cache"
	modelFactsState.mu.Lock()
	modelFactsState.items[name] = modelFactsEntry{
		facts:   ModelFacts{Icon: "ByteDance.Color", VideoReference: true, VideoReferenceKnown: true},
		expires: time.Now().Add(time.Minute),
	}
	modelFactsState.mu.Unlock()
	t.Cleanup(func() {
		modelFactsState.mu.Lock()
		delete(modelFactsState.items, name)
		modelFactsState.mu.Unlock()
	})

	got := ModelFactsOf([]string{name, name, "  "})
	if got[name].Icon != "ByteDance.Color" {
		t.Fatalf("cached icon = %q", got[name].Icon)
	}
	if !got[name].VideoReference || !got[name].VideoReferenceKnown {
		t.Fatalf("cached video reference = %+v", got[name])
	}
	if _, ok := got[""]; ok {
		t.Fatal("blank name should be skipped")
	}
}

func TestWithVideoReference(t *testing.T) {
	cases := []struct {
		name      string
		in        any
		supported bool
		want      string
	}{
		{"网关能收就补上", []string{"native_audio"}, true, "native_audio,video_reference"},
		{"网关不能收就摘掉", []string{"native_audio", "video_reference"}, false, "native_audio"},
		{"本来就有的不重复", []string{"video_reference"}, true, "video_reference"},
		{"nil 清单", nil, true, "video_reference"},
	}
	for _, c := range cases {
		got := WithVideoReference(c.in, c.supported)
		joined := ""
		for i, v := range got {
			if i > 0 {
				joined += ","
			}
			joined += v
		}
		if joined != c.want {
			t.Errorf("%s：got %q want %q", c.name, joined, c.want)
		}
	}
}
