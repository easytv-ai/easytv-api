package service

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// OSSStorage 阿里云 OSS 存储：直接按 OSS 签名 V1 协议 PUT/DELETE 对象，不依赖 SDK。
//
// 环境变量：
//   - OSS_BUCKET            桶名（必填，设置后启用 OSS，否则走本地磁盘）
//   - OSS_ENDPOINT          地域 endpoint，如 oss-cn-hangzhou.aliyuncs.com
//   - OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET
//   - OSS_BASE_URL          对外访问前缀（CDN / 自定义域名），默认 https://<bucket>.<endpoint>
//   - OSS_PREFIX            对象前缀，默认 easytv/
//   - OSS_PUBLIC_READ       上传时带 x-oss-object-acl: public-read（默认 true；桶已公共读时可设 false）
type OSSStorage struct {
	Bucket     string
	Endpoint   string
	KeyId      string
	KeySecret  string
	BaseUrl    string
	Prefix     string
	PublicRead bool
	client     *http.Client
	// 测试注入：覆盖对象 API 地址（默认 https://bucket.endpoint）
	apiBase string
}

// NewOSSStorageFromEnv 按环境变量构造；未配置桶返回 nil。
func NewOSSStorageFromEnv(getenv func(string, string) string, getbool func(string, bool) bool) *OSSStorage {
	bucket := strings.TrimSpace(getenv("OSS_BUCKET", ""))
	if bucket == "" {
		return nil
	}
	endpoint := strings.TrimSpace(getenv("OSS_ENDPOINT", "oss-cn-hangzhou.aliyuncs.com"))
	s := &OSSStorage{
		Bucket:     bucket,
		Endpoint:   endpoint,
		KeyId:      strings.TrimSpace(getenv("OSS_ACCESS_KEY_ID", "")),
		KeySecret:  strings.TrimSpace(getenv("OSS_ACCESS_KEY_SECRET", "")),
		BaseUrl:    strings.TrimRight(strings.TrimSpace(getenv("OSS_BASE_URL", "")), "/"),
		Prefix:     strings.Trim(strings.TrimSpace(getenv("OSS_PREFIX", "easytv")), "/"),
		PublicRead: getbool("OSS_PUBLIC_READ", true),
		client:     &http.Client{Timeout: 10 * time.Minute},
	}
	if s.BaseUrl == "" {
		s.BaseUrl = fmt.Sprintf("https://%s.%s", bucket, endpoint)
	}
	return s
}

// WithAPIBase 覆盖对象 API 地址（测试用）。
func (s *OSSStorage) WithAPIBase(base string) *OSSStorage {
	s.apiBase = strings.TrimRight(base, "/")
	return s
}

func (s *OSSStorage) objectKey(key string) string {
	if s.Prefix == "" {
		return key
	}
	return s.Prefix + "/" + key
}

func (s *OSSStorage) objectURL(key string) string {
	if s.apiBase != "" {
		return s.apiBase + "/" + s.objectKey(key)
	}
	return fmt.Sprintf("https://%s.%s/%s", s.Bucket, s.Endpoint, s.objectKey(key))
}

// Save 上传对象；为了带 Content-Length，先落临时文件。
func (s *OSSStorage) Save(key string, r io.Reader) (string, error) {
	if !isSafeKey(key) {
		return "", errors.New("invalid storage key")
	}
	if s.KeyId == "" || s.KeySecret == "" {
		return "", errors.New("OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET 未配置")
	}
	tmp, err := os.CreateTemp("", "easytv-oss-*")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	size, err := io.Copy(tmp, r)
	if err != nil {
		return "", err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	contentType := mime.TypeByExtension(strings.ToLower(path.Ext(key)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req, err := http.NewRequest(http.MethodPut, s.objectURL(key), tmp)
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	ossHeaders := map[string]string{}
	if s.PublicRead {
		ossHeaders["x-oss-object-acl"] = "public-read"
	}
	s.sign(req, ossHeaders, key)
	res, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oss put failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return "", fmt.Errorf("oss put %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return s.BaseUrl + "/" + s.objectKey(key), nil
}

// Remove 删除对象（不存在视为成功）。
func (s *OSSStorage) Remove(key string) error {
	if !isSafeKey(key) {
		return errors.New("invalid storage key")
	}
	req, err := http.NewRequest(http.MethodDelete, s.objectURL(key), nil)
	if err != nil {
		return err
	}
	s.sign(req, nil, key)
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("oss delete %d", res.StatusCode)
	}
	return nil
}

// sign 按 OSS 签名 V1：Authorization: OSS <KeyId>:<base64(hmac-sha1(secret, StringToSign))>
// StringToSign = VERB\nContent-MD5\nContent-Type\nDate\nCanonicalizedOSSHeaders + CanonicalizedResource
func (s *OSSStorage) sign(req *http.Request, ossHeaders map[string]string, key string) {
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	canonicalHeaders := ""
	// x-oss-* 头需按字典序排列；此处最多一个
	keys := make([]string, 0, len(ossHeaders))
	for k := range ossHeaders {
		keys = append(keys, strings.ToLower(k))
	}
	sortStrings(keys)
	for _, k := range keys {
		v := ossHeaders[k]
		req.Header.Set(k, v)
		canonicalHeaders += k + ":" + v + "\n"
	}
	resource := "/" + s.Bucket + "/" + s.objectKey(key)
	stringToSign := req.Method + "\n" +
		req.Header.Get("Content-MD5") + "\n" +
		req.Header.Get("Content-Type") + "\n" +
		date + "\n" +
		canonicalHeaders + resource
	mac := hmac.New(sha1.New, []byte(s.KeySecret))
	mac.Write([]byte(stringToSign))
	req.Header.Set("Authorization", "OSS "+s.KeyId+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

// StringToSignForTest 暴露签名串给测试核对。
func (s *OSSStorage) StringToSignForTest(method, contentType, date, canonicalHeaders, key string) string {
	return method + "\n\n" + contentType + "\n" + date + "\n" + canonicalHeaders + "/" + s.Bucket + "/" + s.objectKey(key)
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
