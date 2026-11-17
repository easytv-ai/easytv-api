package controller

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// CanvasLive 画布实时通道（GET /user/canvas/:id/live）。
// 浏览器的 WebSocket 带不上 EasyTV-User 头，所以这条路由不走 UserAuth。
// 升级之后的第一帧必须是 auth，令牌只留在帧里，不进 URL，避免访问日志记下登录凭证。
func CanvasLive(c *gin.Context) {
	canvasId := paramInt(c, "id")
	if canvasId == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	if !headerHasToken(c.GetHeader("Connection"), "upgrade") || !strings.EqualFold(strings.TrimSpace(c.GetHeader("Upgrade")), "websocket") {
		common.ApiErrorMsg(c, "需要 WebSocket")
		return
	}
	if strings.TrimSpace(c.GetHeader("Sec-WebSocket-Version")) != "13" {
		c.Header("Sec-WebSocket-Version", "13")
		common.ApiErrorMsg(c, "不支持的 WebSocket 版本")
		return
	}
	key := strings.TrimSpace(c.GetHeader("Sec-WebSocket-Key"))
	if key == "" || len(key) > 64 || strings.ContainsAny(key, "\r\n") {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	origin := safeLiveOrigin(c.GetHeader("Origin"))
	if origin == "" && strings.TrimSpace(c.GetHeader("Origin")) != "" {
		common.ApiErrorMsg(c, "无效的来源")
		return
	}
	hijacker, ok := c.Writer.(http.Hijacker)
	if !ok || c.Writer.Written() {
		common.ApiErrorMsg(c, "无法建立实时连接")
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		common.SysError("canvas live hijack: " + err.Error())
		return
	}
	defer conn.Close()
	if rw == nil {
		rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	}
	if rw.Reader == nil {
		rw.Reader = bufio.NewReader(conn)
	}
	if rw.Writer == nil {
		rw.Writer = bufio.NewWriter(conn)
	}
	if err := writeLiveUpgrade(rw.Writer, origin, service.WsAcceptKey(key)); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	opcode, payload, err := service.ReadWsFrame(rw.Reader)
	if err != nil || opcode != 1 {
		_ = service.WriteWsClose(rw.Writer, 1008, "unauthorized")
		return
	}
	var auth struct {
		Op    string `json:"op"`
		Token string `json:"token"`
		Uid   int    `json:"uid"`
	}
	if json.Unmarshal(payload, &auth) != nil || auth.Op != "auth" || auth.Uid <= 0 || auth.Token == "" || len(auth.Token) > 512 {
		_ = service.WriteWsClose(rw.Writer, 1008, "unauthorized")
		return
	}
	user, err := model.ValidateAccessToken(auth.Token)
	auth.Token = ""
	if err != nil || user == nil || user.Id != auth.Uid || user.Status != constant.UserStatusEnabled {
		_ = service.WriteWsClose(rw.Writer, 1008, "unauthorized")
		return
	}
	_, perm, err := service.CanvasAccess(user.Id, canvasId)
	if err != nil {
		_ = service.WriteWsClose(rw.Writer, 1008, "forbidden")
		return
	}
	peer, hello := service.CanvasLive().Join(canvasId, service.CanvasLiveUser{
		Id:   user.Id,
		Name: liveDisplayName(user),
	}, service.CanvasWritable(perm))
	if peer == nil {
		_ = service.WriteWsClose(rw.Writer, 1013, "full")
		return
	}
	defer service.CanvasLive().Leave(canvasId, peer)
	go peer.WriteLoop(rw.Writer, conn)
	peer.TrySend(hello)
	readLiveFrames(conn, rw.Reader, canvasId, peer)
}

func readLiveFrames(conn net.Conn, reader *bufio.Reader, canvasId int, peer *service.CanvasLivePeer) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		opcode, payload, err := service.ReadWsFrame(reader)
		if err != nil {
			return
		}
		if opcode == 8 {
			return
		}
		if opcode != 1 {
			continue
		}
		var msg struct {
			Op    string                   `json:"op"`
			X     float64                  `json:"x"`
			Y     float64                  `json:"y"`
			Items []service.CanvasLiveNode `json:"items"`
		}
		if json.Unmarshal(payload, &msg) != nil {
			continue
		}
		switch msg.Op {
		case "ping", "auth":
			continue
		case "cursor":
			if !service.ValidCanvasLivePoint(msg.X, msg.Y) {
				continue
			}
			service.CanvasLive().Relay(canvasId, peer, "cursor", msg.X, msg.Y, nil)
		case "nodes":
			if !service.ValidCanvasLiveNodes(msg.Items) {
				continue
			}
			service.CanvasLive().Relay(canvasId, peer, "nodes", 0, 0, msg.Items)
		}
	}
}

func liveDisplayName(user *model.User) string {
	name := strings.TrimSpace(user.Nickname)
	if name == "" || strings.Contains(name, "@") {
		return "成员"
	}
	if utf8.RuneCountInString(name) > 20 {
		runes := []rune(name)
		return string(runes[:20])
	}
	return name
}

func safeLiveOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 200 || strings.ContainsAny(raw, "\r\n \t") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func headerHasToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func writeLiveUpgrade(w *bufio.Writer, origin, accept string) error {
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Accept: ")
	b.WriteString(accept)
	b.WriteString("\r\n")
	if origin != "" {
		b.WriteString("Access-Control-Allow-Origin: ")
		b.WriteString(origin)
		b.WriteString("\r\n")
		b.WriteString("Access-Control-Allow-Credentials: true\r\n")
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	return w.Flush()
}
