package common

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// GenerateHMAC 使用全局 CryptoSecret 计算 HMAC-SHA256 签名（16 进制）。
func GenerateHMAC(data string) string {
	h := hmac.New(sha256.New, []byte(CryptoSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

// Password2Hash 使用 bcrypt 生成密码哈希。
func Password2Hash(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hashedPassword), err
}

// ValidatePasswordAndHash 校验明文密码与 bcrypt 哈希是否匹配。
func ValidatePasswordAndHash(password string, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
