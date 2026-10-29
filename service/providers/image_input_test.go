package providers

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easytv-api/model"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n0000")

// TestEditImageMultipart images/edits 表单：字段、多图用 image[]、每个文件带真实 Content-Type、蒙版单独字段。
func TestEditImageMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if r.FormValue("model") != "gpt-image-2" || r.FormValue("prompt") != "换成夜景" || r.FormValue("n") != "1" ||
			r.FormValue("size") != "1024x1024" || r.FormValue("quality") != "high" {
			t.Errorf("fields = %v", r.MultipartForm.Value)
		}
		imgs := r.MultipartForm.File["image[]"]
		if len(imgs) != 2 {
			t.Fatalf("image[] = %d, want 2", len(imgs))
		}
		if ct := imgs[0].Header.Get("Content-Type"); ct != "image/png" {
			t.Errorf("image content-type = %q", ct)
		}
		if masks := r.MultipartForm.File["mask"]; len(masks) != 1 {
			t.Errorf("mask files = %d", len(masks))
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGVsbG8="}]}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)
	_, b64, err := p.EditImage("gpt-image-2", "换成夜景", "1024x1024", "high",
		[]ImageFile{{Name: "a.png", Data: pngHeader}, {Name: "b.png", Data: pngHeader}},
		&ImageFile{Name: "mask.png", Data: pngHeader})
	if err != nil || b64 != "aGVsbG8=" {
		t.Fatalf("edit: b64=%q err=%v", b64, err)
	}
}

// TestGenerateImageWithUrls url 方式：单张参考图给字符串，多张给数组。
func TestGenerateImageWithUrls(t *testing.T) {
	var got []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		got = append(got, req["image"])
		if _, ok := req["quality"]; ok {
			t.Errorf("url 方式不该带 quality: %v", req)
		}
		_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn.example.com/out.png"}]}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)
	if _, _, err := p.generateImage("seedream-4.5", "p", "", "", []string{"https://cdn/a.png"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.generateImage("seedream-4.5", "p", "", "", []string{"https://cdn/a.png", "https://cdn/b.png"}); err != nil {
		t.Fatal(err)
	}
	if got[0] != "https://cdn/a.png" {
		t.Errorf("single image = %v", got[0])
	}
	if arr, ok := got[1].([]any); !ok || len(arr) != 2 {
		t.Errorf("multi image = %v", got[1])
	}
}

func TestInferImageInput(t *testing.T) {
	cases := map[string]string{
		"gpt-image-2":                 model.ImageInputEdits,
		"gpt-image-1-mini":            model.ImageInputEdits,
		"chatgpt-image-latest":        model.ImageInputEdits,
		"seedream-4.5":                model.ImageInputUrl,
		"qwen-image-edit":             model.ImageInputUrl,
		"gemini-3.1-flash-image":      model.ImageInputUrl,
		"gemini-3-pro-image":          model.ImageInputUrl,
		"gemini-3.1-flash-lite-image": model.ImageInputUrl,
		"gemini-2.5-flash":            model.ImageInputNone,
		"bytedance/seedream-5.0-lite": model.ImageInputUrl,
		"dall-e-3":                    model.ImageInputNone,
		"midjourney":                  model.ImageInputNone,
	}
	for name, want := range cases {
		if got := model.InferImageInput(name); got != want {
			t.Errorf("InferImageInput(%q) = %q, want %q", name, got, want)
		}
	}
	p := &model.ModelPrice{Model: "dall-e-3", ImageInput: model.ImageInputUrl}
	if p.EffectiveImageInput() != model.ImageInputUrl {
		t.Error("声明值应优先于推断")
	}
}

func TestImageQuality(t *testing.T) {
	if q := imageQuality(model.ImageInputEdits, taskParams{"quality": "std"}); q != "medium" {
		t.Errorf("std → %q", q)
	}
	if q := imageQuality(model.ImageInputUrl, taskParams{"quality": "high"}); q != "" {
		t.Errorf("非 OpenAI 模型不该带 quality: %q", q)
	}
}

// TestOpenAIMaskFromMark 画布标记（涂抹处不透明）→ OpenAI 蒙版（要改的地方透明）。
func TestOpenAIMaskFromMark(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255}) // 标记
	src.SetNRGBA(1, 0, color.NRGBA{})                               // 未标记
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	out, err := OpenAIMaskFromMark(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Errorf("标记处应透明, alpha=%d", a)
	}
	if _, _, _, a := img.At(1, 0).RGBA(); a == 0 {
		t.Error("未标记处应不透明")
	}
	if _, err := OpenAIMaskFromMark([]byte("not png")); err == nil || !strings.Contains(err.Error(), "PNG") {
		t.Errorf("非 PNG 应报错: %v", err)
	}
}

// TestImageSizeFor gpt-image 系不能用 DALL·E 3 的 1792 尺寸；2K 清晰度给其他模型 2K 像素、给 gpt-image 高画质。
func TestImageSizeFor(t *testing.T) {
	cases := []struct{ model, ratio, clarity, want string }{
		{"gpt-image-2", "16:9", "", "1536x1024"},
		{"openai/gpt-image-1", "9:16", "2K", "1024x1536"},
		{"gpt-image-2", "1:1", "", "1024x1024"},
		{"dall-e-3", "16:9", "", "1792x1024"},
		{"seedream-4", "16:9", "2K", "2560x1440"},
		{"seedream-4", "1:1", "2K", "2048x2048"},
		{"seedream-4", "16:9", "1K", "1792x1024"},
	}
	for _, c := range cases {
		if got := imageSizeFor(c.model, c.ratio, c.clarity); got != c.want {
			t.Errorf("imageSizeFor(%s,%s,%s) = %s, want %s", c.model, c.ratio, c.clarity, got, c.want)
		}
	}
	if q := imageQuality("edits", taskParams{"clarity": "2K"}); q != "high" {
		t.Errorf("2K quality = %q", q)
	}
	if q := imageQuality("edits", taskParams{"clarity": "1K"}); q != "" {
		t.Errorf("1K quality = %q", q)
	}
}

// TestChatWithImages 看图对话：content 为 text + image_url 数组，公网地址原样透传。
func TestChatWithImages(t *testing.T) {
	var req map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" 一只猫 "}}]}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)
	text, err := p.ChatWithImages("gpt-4o-mini", "描述图片", []*model.Asset{{Id: 1, Url: "https://cdn.example.com/a.png"}})
	if err != nil || text != "一只猫" {
		t.Fatalf("chat: text=%q err=%v", text, err)
	}
	msgs, _ := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", req["messages"])
	}
	content, _ := msgs[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %v", content)
	}
	img, _ := content[1].(map[string]any)
	if img["type"] != "image_url" || img["image_url"].(map[string]any)["url"] != "https://cdn.example.com/a.png" {
		t.Errorf("image part = %v", img)
	}
}
