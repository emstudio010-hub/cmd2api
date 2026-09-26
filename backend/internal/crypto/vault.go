// Package crypto 负责上游账号凭证的落库加密。
//
// Command Code 的密钥等同于账号本身——拿到就能消耗额度。数据库、备份、
// 误导出的 dump 都可能泄露，所以凭证不以明文入库，用 AES-256-GCM 加密，
// 密钥只存在于环境变量里。GCM 自带完整性校验，密文被改动会解密失败而不是
// 悄悄返回一段垃圾。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidCiphertext 表示密文格式不对或已被篡改。
var ErrInvalidCiphertext = errors.New("密文无效或已被篡改")

// Vault 用固定密钥做对称加解密。
type Vault struct {
	aead cipher.AEAD
}

// NewVault 用 32 字节密钥构造 Vault。
func NewVault(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256 需要 32 字节密钥，得到 %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM: %w", err)
	}
	return &Vault{aead: aead}, nil
}

// Encrypt 加密明文，返回 base64(nonce || ciphertext)。
func (v *Vault) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce: %w", err)
	}
	// Seal 把密文追加到 nonce 后面，省掉一次额外的拼接。
	sealed := v.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 的输出。
func (v *Vault) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	ns := v.aead.NonceSize()
	if len(raw) < ns {
		return "", ErrInvalidCiphertext
	}
	plaintext, err := v.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		// 不把底层错误透出去，避免给攻击者区分「格式错」和「校验失败」的线索。
		return "", ErrInvalidCiphertext
	}
	return string(plaintext), nil
}

// MaskKey 把密钥打码成 user_ab…cd 的样子，用于列表展示。
//
// 只保留前缀和后 4 位：足够管理员认出是哪一把，又不至于泄露。
func MaskKey(key string) string {
	if key == "" {
		return ""
	}
	const keepTail = 4
	if len(key) <= keepTail+4 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-keepTail:]
}
