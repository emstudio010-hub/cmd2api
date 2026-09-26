// Package middleware 提供管理后台鉴权与下游密钥鉴权。
package middleware

import (
	"net/http"
	"strings"

	"cmd2api/internal/auth"
	"cmd2api/internal/domain"

	"github.com/gin-gonic/gin"
)

// ctxKeyAdminClaims 是登录声明在 gin.Context 里的键名。
const ctxKeyAdminClaims = "cmd2api.admin_claims"

// AdminAuth 校验管理后台的 Bearer 令牌，并要求持有者是管理员。
func AdminAuth(issuer *auth.Issuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "缺少登录令牌"})
			return
		}
		claims, err := issuer.Verify(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "登录令牌无效或已过期"})
			return
		}
		if claims.Role != domain.RoleAdmin {
			// 仅管理员模式：非管理员一律拒绝，不存在「普通用户能看自己数据」的路径。
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		c.Set(ctxKeyAdminClaims, claims)
		c.Next()
	}
}

// AdminClaimsFrom 取出当前请求的登录声明。
func AdminClaimsFrom(c *gin.Context) (*auth.Claims, bool) {
	v, ok := c.Get(ctxKeyAdminClaims)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*auth.Claims)
	return claims, ok
}

// bearerToken 从 Authorization 头里取出令牌。
func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if h == "" {
		return ""
	}
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	// 有些客户端（curl 手测）会直接填令牌，容错一下。
	return strings.TrimSpace(h)
}
