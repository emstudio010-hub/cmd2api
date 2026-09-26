package handler

import (
	"net/http"

	"cmd2api/internal/crypto"

	"github.com/gin-gonic/gin"
)

// Settings 返回当前生效的运行配置。
//
// 全部只读、且**密钥只回显打码形式**：这个接口的用途是让管理员确认
// 「服务现在按什么参数在跑」，不是在线改配置。改配置走环境变量 + 重启，
// 这样配置的来源永远只有一个，不会出现「界面改了但没生效」的困惑。
func (h *Handler) Settings(c *gin.Context) {
	cc := h.cfg.CommandCC

	c.JSON(http.StatusOK, gin.H{
		"upstream": gin.H{
			"base_url":                 cc.BaseURL,
			"project_slug":             cc.ProjectSlug,
			"cli_version":              cc.CLIVersion,
			"fingerprint_salt_set":     cc.FingerprintSalt != "",
			"fingerprint_salt_masked":  maskSecret(cc.FingerprintSalt),
			"empty_system_placeholder": cc.EmptySystemPlaceholder,
			"stream_idle_timeout":      cc.StreamIdleTimeout.String(),
			"nonstream_idle_timeout":   cc.NonStreamIdleTimeout.String(),
			"max_body_mb":              cc.MaxBodyMB,
			"model_cache_ttl":          cc.ModelCacheTTL.String(),
		},
		"auth": gin.H{
			"token_ttl": h.cfg.Auth.TokenTTL.String(),
		},
		"health_check": gin.H{
			"enabled":           h.cfg.Health.Enabled,
			"interval":          h.cfg.Health.Interval.String(),
			"failure_threshold": h.cfg.Health.FailureThreshold,
			"timeout":           h.cfg.Health.Timeout.String(),
		},
	})
}

// AdminListModels 返回上游可用模型列表（管理后台用，需要管理员登录）。
func (h *Handler) AdminListModels(c *gin.Context) {
	if h.relay == nil {
		fail(c, http.StatusServiceUnavailable, "中转服务未启用")
		return
	}
	ctx := c.Request.Context()

	groupID, err := h.firstActiveGroupID(ctx)
	if err != nil {
		h.failInternal(c, "查询分组失败", err)
		return
	}
	if groupID == 0 {
		// 一个分组都没有时没法借账号去拉列表，直接返回空而不是报错——
		// 这在新部署、还没加账号的阶段是正常状态。
		c.JSON(http.StatusOK, gin.H{"items": []any{}, "hint": "还没有分组或账号，无法拉取模型列表"})
		return
	}

	models, err := h.relay.Models(ctx, groupID)
	if err != nil {
		h.logger.Warn("拉取模型列表失败", "err", err)
		fail(c, http.StatusServiceUnavailable, "暂时无法获取模型列表，请确认账号可用")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": models, "total": len(models)})
}

// maskSecret 把密钥打码，只保留首尾各 2 个字符。
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return crypto.MaskKey(s)
}
