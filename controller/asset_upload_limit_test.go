package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// recordingStorage 记录有没有文件被写进存储。
type recordingStorage struct{ saved int }

func (s *recordingStorage) Save(string, io.Reader) (string, error) {
	s.saved++
	return "", nil
}
func (s *recordingStorage) Remove(string) error { return nil }

// chunkedBody 只暴露 Read：http.NewRequest 认不出长度，请求按未知长度（ContentLength=-1）发出。
type chunkedBody struct{ r io.Reader }

func (b chunkedBody) Read(p []byte) (int, error) { return b.r.Read(p) }

func oversizedUpload(t *testing.T, fileBytes int) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "big.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte{'x'}, fileBytes)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

// 超限的上传在解析表单前就被拒：不进存储，临时目录里也不留 multipart 临时文件。
func TestUploadAssetRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevMax, prevStorage := service.UploadMaxBytes, service.DefaultStorage
	store := &recordingStorage{}
	service.UploadMaxBytes = 1 << 20 // 1 MB
	service.DefaultStorage = store
	t.Cleanup(func() { service.UploadMaxBytes, service.DefaultStorage = prevMax, prevStorage })

	r := gin.New()
	r.POST("/user/asset/upload", func(c *gin.Context) { c.Set("id", 1) }, UploadAsset)

	// 文件 40 MB：超过上限，也超过 multipart 的 32 MB 内存阈值——不设限时这部分会写进临时盘。
	const fileBytes = 40 << 20

	for _, tc := range []struct {
		name    string
		chunked bool
	}{
		{"带 Content-Length", false},
		{"不带长度（chunked）", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			store.saved = 0

			body, contentType := oversizedUpload(t, fileBytes)
			var reader io.Reader = body
			if tc.chunked {
				reader = chunkedBody{body}
			}
			req := httptest.NewRequest(http.MethodPost, "/user/asset/upload", reader)
			req.Header.Set("Content-Type", contentType)
			if tc.chunked && req.ContentLength != -1 {
				t.Fatalf("用例前提不成立：ContentLength=%d", req.ContentLength)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			var resp struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("响应不是统一格式的 JSON：%s", rec.Body.String())
			}
			if rec.Code != http.StatusOK || resp.Success || !strings.Contains(resp.Message, "文件超过大小限制") || !strings.Contains(resp.Message, "1 MB") {
				t.Fatalf("应按统一格式拒绝并说明上限，得到 code=%d body=%s", rec.Code, rec.Body.String())
			}
			if store.saved != 0 {
				t.Fatalf("超限文件不应进入存储，Save 被调用 %d 次", store.saved)
			}
			entries, err := os.ReadDir(tmp)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("临时目录不应留下文件，发现 %d 个（如 %s）", len(entries), entries[0].Name())
			}
			// 带长度的请求一个字节都不该读；不带长度的最多读到上限附近。
			limit := int(service.UploadMaxBytes) + uploadFormOverhead
			if read := fileBytes - body.Len(); tc.chunked && read > limit+64<<10 {
				t.Fatalf("读取了 %d 字节，超过上限 %d", read, limit)
			} else if !tc.chunked && read > 0 {
				t.Fatalf("Content-Length 已超限时不应读取请求体，读了约 %d 字节", read)
			}
		})
	}
}

// 没超限的请求不受影响：照常走到后面的校验（这里是空文件）。
func TestUploadAssetWithinLimitStillParsed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevMax, prevStorage := service.UploadMaxBytes, service.DefaultStorage
	service.UploadMaxBytes = 1 << 20
	service.DefaultStorage = &recordingStorage{}
	t.Cleanup(func() { service.UploadMaxBytes, service.DefaultStorage = prevMax, prevStorage })

	r := gin.New()
	r.POST("/user/asset/upload", func(c *gin.Context) { c.Set("id", 1) }, UploadAsset)

	body, contentType := oversizedUpload(t, 0)
	req := httptest.NewRequest(http.MethodPost, "/user/asset/upload", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "文件为空") {
		t.Fatalf("限额内的请求应正常解析，得到 %s", rec.Body.String())
	}
}
