package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/apikey"
	"cmd2api/internal/domain"

	"github.com/gin-gonic/gin"
)

// ctxKeyAPIKey 是校验通过的下游密钥在 gin.Context 里的键名。
const ctxKeyAPIKey = "cmd2api.api_key"

// APIKeyAuth 校验下游客户端带来的 cmd2api 密钥。
//
// 同时接受三种常见携带方式，因为这些客户端各有各的习惯：
//   - Authorization: Bearer sk-c2a-xxx（OpenAI SDK 默认）
//   - x-api-key: sk-c2a-xxx（Anthropic SDK 默认）
//   - Authorization: sk-c2a-xxx（裸填）
func APIKeyAuth(client *ent.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := clientAPIKey(c)
		if raw == "" {
			// Anthropic 客户端拿到 401 会重试，所以错误体按 Anthropic 的
			// 结构返回，让 Claude Code 能正常显示错误而不是解析失败。
			abortWithError(c, http.StatusUnauthorized, "authentication_error", "缺少 API Key")
			return
		}

		key, err := client.APIKey.Query().
			Where(
				apikey.KeyEQ(raw),
				apikey.DeletedAtIsNil(),
			).
			WithUser().
			WithGroup().
			Only(c.Request.Context())
		if err != nil {
			abortWithError(c, http.StatusUnauthorized, "authentication_error", "API Key 无效")
			return
		}

		if key.Status != domain.StatusActive {
			abortWithError(c, http.StatusForbidden, "permission_error", "API Key 已被禁用")
			return
		}
		if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
			abortWithError(c, http.StatusForbidden, "permission_error", "API Key 已过期")
			return
		}
		if key.Edges.User != nil && key.Edges.User.Status != domain.StatusActive {
			abortWithError(c, http.StatusForbidden, "permission_error", "账号已被禁用")
			return
		}
		if key.Quota > 0 && key.QuotaUsed >= key.Quota {
			abortWithError(c, http.StatusForbidden, "permission_error", "API Key 额度已用尽")
			return
		}
		if !ipAllowed(c.ClientIP(), key.IPWhitelist) {
			abortWithError(c, http.StatusForbidden, "permission_error", "来源 IP 不在白名单内")
			return
		}
		if key.GroupID == nil {
			// 没有绑分组就没法决定去哪个账号池取号，属于配置缺失，
			// 不是客户端的错，所以返回 500 并说清原因。
			abortWithError(c, http.StatusInternalServerError, "api_error", "该 API Key 未绑定分组，请在后台配置")
			return
		}
		if key.Edges.Group != nil && key.Edges.Group.Status != domain.StatusActive {
			abortWithError(c, http.StatusForbidden, "permission_error", "API Key 所属分组已停用")
			return
		}

		c.Set(ctxKeyAPIKey, key)
		c.Next()
	}
}

// APIKeyFrom 取出当前请求的密钥实体。
func APIKeyFrom(c *gin.Context) (*ent.APIKey, bool) {
	v, ok := c.Get(ctxKeyAPIKey)
	if !ok {
		return nil, false
	}
	key, ok := v.(*ent.APIKey)
	return key, ok
}

// clientAPIKey 从请求头里提取下游密钥。
func clientAPIKey(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("x-api-key")); v != "" {
		return v
	}
	h := c.GetHeader("Authorization")
	if h == "" {
		return ""
	}
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(h)
}

// ipAllowed 判断来源 IP 是否命中白名单。白名单为空表示不限制。
//
// 支持单个 IP 和 CIDR 两种写法，另外 ::ffff:1.2.3.4 这类 IPv4-mapped IPv6
// 需要归一化后再比对，否则明明配了 1.2.3.4 却匹配不上。
func ipAllowed(clientIP string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true
	}
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	for _, entry := range whitelist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if cidr.Contains(ip) {
				return true
			}
			continue
		}
		if allowed := net.ParseIP(entry); allowed != nil {
			if a4 := allowed.To4(); a4 != nil {
				allowed = a4
			}
			if allowed.Equal(ip) {
				return true
			}
		}
	}
	return false
}

// abortWithError 按 Anthropic 的错误信封返回，两种协议的客户端都能读懂。
func abortWithError(c *gin.Context, status int, errType, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// TouchAPIKey 更新密钥的最近使用时间。
//
// 用 context.WithoutCancel 是为了让这个写操作在请求被客户端取消后仍能完成——
// 否则用户一按 Ctrl-C，这条使用记录就丢了。
func TouchAPIKey(ctx context.Context, client *ent.Client, keyID int64) {
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, _ = client.APIKey.Update().
		Where(apikey.ID(keyID)).
		SetLastUsedAt(time.Now()).
		Save(bg)
}
