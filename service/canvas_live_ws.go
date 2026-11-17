package service

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// 一条实时消息的上限。光标和一批节点坐标都远小于此。
const canvasLiveMaxFrame = 32 << 10

func wsAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func writeWsFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	if len(payload) > canvasLiveMaxFrame {
		return errors.New("frame too large")
	}
	header := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n <= 65535:
		header = append(header, 126, byte(n>>8), byte(n))
	default:
		return errors.New("frame too large")
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func writeWsText(w *bufio.Writer, payload []byte) error {
	return writeWsFrame(w, 1, payload)
}

// WsAcceptKey / ReadWsFrame / WriteWsClose 给 HTTP 升级用。测试仍走未导出的同名实现。
func WsAcceptKey(key string) string { return wsAcceptKey(key) }

func ReadWsFrame(r *bufio.Reader) (byte, []byte, error) { return readWsFrame(r) }

func WriteWsClose(w *bufio.Writer, code int, reason string) error {
	if len(reason) > 80 {
		reason = reason[:80]
	}
	payload := []byte{byte(code >> 8), byte(code & 0xff)}
	payload = append(payload, reason...)
	return writeWsFrame(w, 8, payload)
}

// WriteLoop 是这条连接唯一的写者。
func (p *CanvasLivePeer) WriteLoop(w *bufio.Writer, conn net.Conn) {
	for {
		select {
		case <-p.done:
			return
		case msg := <-p.send:
			if err := writeWsText(w, msg); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

// readWsFrame 读客户端帧。浏览器发来的帧必须带 mask。
func readWsFrame(r *bufio.Reader) (opcode byte, payload []byte, err error) {
	head := make([]byte, 2)
	if _, err = io.ReadFull(r, head); err != nil {
		return 0, nil, err
	}
	if head[0]&0x80 == 0 {
		return 0, nil, errors.New("fragmented frame")
	}
	opcode = head[0] & 0x0f
	if head[1]&0x80 == 0 {
		return 0, nil, errors.New("unmasked client frame")
	}
	length := int(head[1] & 0x7f)
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext))
	case 127:
		return 0, nil, errors.New("frame too large")
	}
	if length > canvasLiveMaxFrame {
		return 0, nil, errors.New("frame too large")
	}
	mask := make([]byte, 4)
	if _, err = io.ReadFull(r, mask); err != nil {
		return 0, nil, err
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return opcode, payload, nil
}
