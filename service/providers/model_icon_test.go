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

func TestModelIconsUsesCache(t *testing.T) {
	const name = "seedance-2.0-icon-cache"
	modelIconState.mu.Lock()
	modelIconState.items[name] = modelIconEntry{icon: "ByteDance.Color", expires: time.Now().Add(time.Minute)}
	modelIconState.mu.Unlock()
	t.Cleanup(func() {
		modelIconState.mu.Lock()
		delete(modelIconState.items, name)
		modelIconState.mu.Unlock()
	})

	got := ModelIcons([]string{name, name, "  "})
	if got[name] != "ByteDance.Color" {
		t.Fatalf("cached icon = %q", got[name])
	}
	if _, ok := got[""]; ok {
		t.Fatal("blank name should be skipped")
	}
}
