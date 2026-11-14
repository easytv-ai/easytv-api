package providers

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"easytv-api/model"
	"easytv-api/service"
)

// inlineRefMaxBytes 内联进请求体的单张参考图上限。超过的要求走对象存储或公网地址，
// 免得一次请求带几十兆 base64 把网关和上游的请求体上限撑爆。
const inlineRefMaxBytes = 10 << 20

// gatewayReachable 网关能不能自己拉到这个地址：只认公网 http(s)。
// 本机、内网网段、docker 服务名（没有点的主机名）一律算拉不到——
// 自部署用本地磁盘存储时素材地址就是这几种。
func gatewayReachable(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".test"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

// localUploadKey 素材是不是存在本机上传目录里；是则返回目录内的相对路径。
// 本地磁盘存储给出的地址有两种：没配 UPLOAD_BASE_URL 时是 /upload/<key>，配了是 <UPLOAD_BASE_URL>/<key>。
func localUploadKey(raw string) (string, bool) {
	key := ""
	switch base := service.UploadBaseUrl; {
	case strings.HasPrefix(raw, "/upload/"):
		key = strings.TrimPrefix(raw, "/upload/")
	case base != "" && strings.HasPrefix(raw, base+"/"):
		key = strings.TrimPrefix(raw, base+"/")
	default:
		return "", false
	}
	if i := strings.IndexAny(key, "?#"); i >= 0 {
		key = key[:i]
	}
	return key, key != ""
}

// gatewayAssetUrl 把素材变成网关收得到的地址：
// 公网地址原样给；网关拉不到的图片读出字节内联成 data URL；
// 音视频体积太大不内联，报错并说明要配什么。
func gatewayAssetUrl(a *model.Asset) (string, error) {
	if gatewayReachable(a.Url) {
		return a.Url, nil
	}
	if a.Type != model.AssetTypeImage {
		return "", errors.New("参考视频 / 音频必须是网关能访问的公网地址：请启用对象存储，或把 UPLOAD_BASE_URL 配成公网可访问的本站地址")
	}
	img, err := readAssetImage(a)
	if err != nil {
		return "", err
	}
	if len(img.Data) > inlineRefMaxBytes {
		return "", fmt.Errorf("素材 %d 超过 %d MB，无法随请求内联：请启用对象存储，或把 UPLOAD_BASE_URL 配成公网可访问的本站地址", a.Id, inlineRefMaxBytes>>20)
	}
	return "data:" + http.DetectContentType(img.Data) + ";base64," + base64.StdEncoding.EncodeToString(img.Data), nil
}
