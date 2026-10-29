package common

import (
	"encoding/hex"
	"math/rand"
)

// GetRandomString 生成长度为 length 的纯小写随机字母数字串。
func GetRandomString(length int) string {
	if length <= 0 {
		return ""
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return string(b)
}

// toHex 将字节切片编码为 16 进制字符串。
func toHex(data []byte) string {
	return hex.EncodeToString(data)
}
