// Package auth 负责管理员登录：口令哈希与 JWT 签发校验。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidToken 表示令牌无效或已过期。
var ErrInvalidToken = errors.New("令牌无效或已过期")

// Claims 是管理后台登录令牌携带的信息。
type Claims struct {
	UserID int64  `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// Issuer 负责签发和校验令牌。
type Issuer struct {
	secret []byte
	ttl    time.Duration
}

// NewIssuer 构造 Issuer。
func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

// Issue 为指定用户签发令牌。
func (i *Issuer) Issue(userID int64, email, role string) (string, time.Time, error) {
	expiresAt := time.Now().Add(i.ttl)
	claims := Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(userID),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			// 显式限定算法，避免校验时被换成 none 或非对称算法。
			Issuer: "cmd2api",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发令牌: %w", err)
	}
	return signed, expiresAt, nil
}

// Verify 校验令牌并返回其中的声明。
func (i *Issuer) Verify(raw string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期的签名算法: %v", t.Header["alg"])
		}
		return i.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// HashPassword 用 bcrypt 生成口令哈希。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("生成口令哈希: %w", err)
	}
	return string(b), nil
}

// CheckPassword 校验明文口令是否匹配哈希。
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// GenerateAPIKey 生成一把下游密钥，形如 sk-c2a-<32位十六进制>。
func GenerateAPIKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机密钥: %w", err)
	}
	return "sk-c2a-" + hex.EncodeToString(buf), nil
}
