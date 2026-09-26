package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"cmd2api/internal/middleware"
	"cmd2api/internal/relay"

	"github.com/gin-gonic/gin"
)

// sessionHintHeaders 是客户端可能用来标记会话的请求头。
//
// Claude Code 用 x-claude-code-session-id，OpenAI 系客户端用 session_id，
// 都收下来传给上游，让「同一会话保持同一 session」这件事成立。
var sessionHintHeaders = []string{
	"x-session-id",
	"x-claude-code-session-id",
	"session_id",
}

// ChatCompletions 处理 OpenAI Chat Completions 请求。
func (h *Handler) ChatCompletions(c *gin.Context) {
	h.relayRequest(c, relay.ProtocolOpenAI)
}

// Messages 处理 Anthropic Messages 请求，供 Claude Code 等客户端接入。
func (h *Handler) Messages(c *gin.Context) {
	h.relayRequest(c, relay.ProtocolAnthropic)
}

// relayRequest 是两种协议共用的中转入口。
func (h *Handler) relayRequest(c *gin.Context, protocol relay.Protocol) {
	key, ok := middleware.APIKeyFrom(c)
	if !ok {
		// 走到这里说明中间件没挂上，属于服务端配置问题。
		fail(c, http.StatusUnauthorized, "缺少 API Key")
		return
	}
	if h.relay == nil {
		fail(c, http.StatusServiceUnavailable, "中转服务未启用")
		return
	}

	// 限制请求体大小：超限时 MaxBytesReader 会让读取直接失败，
	// 而不是把几百 MB 读进内存再判断。
	maxBytes := h.cfg.CommandCC.MaxBodyMB * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = 100 * 1024 * 1024
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(c, http.StatusRequestEntityTooLarge, "请求体超过大小限制")
			return
		}
		fail(c, http.StatusBadRequest, "读取请求体失败")
		return
	}
	if len(body) == 0 {
		fail(c, http.StatusBadRequest, "请求体为空")
		return
	}

	var groupID int64
	if key.GroupID != nil {
		groupID = *key.GroupID
	}

	// 分组倍率：没绑定分组实体时按 1 计算。
	multiplier := 1.0
	if key.Edges.Group != nil && key.Edges.Group.RateMultiplier > 0 {
		multiplier = key.Edges.Group.RateMultiplier
	}

	req := &relay.RelayRequest{
		Protocol:       protocol,
		Body:           body,
		APIKeyID:       key.ID,
		UserID:         key.UserID,
		GroupID:        groupID,
		RequestID:      relay.GenerateRequestID(),
		UserAgent:      truncateHeader(c.GetHeader("User-Agent"), 512),
		ClientIP:       c.ClientIP(),
		SessionHints:   collectSessionHints(c),
		RateMultiplier: multiplier,
	}

	// 使用记录异步写，失败也不影响本次请求。
	middleware.TouchAPIKey(c.Request.Context(), h.client, key.ID)

	h.relay.Relay(c.Request.Context(), c.Writer, req)
}

// collectSessionHints 收集客户端带来的会话标识。
func collectSessionHints(c *gin.Context) []string {
	var hints []string
	for _, name := range sessionHintHeaders {
		if v := strings.TrimSpace(c.GetHeader(name)); v != "" {
			hints = append(hints, v)
		}
	}
	return hints
}

// ListModels 返回可用模型列表，供客户端拉取。
func (h *Handler) ListModels(c *gin.Context) {
	if h.relay == nil {
		fail(c, http.StatusServiceUnavailable, "中转服务未启用")
		return
	}
	key, ok := middleware.APIKeyFrom(c)
	if !ok {
		fail(c, http.StatusUnauthorized, "缺少 API Key")
		return
	}

	var groupID int64
	if key.GroupID != nil {
		groupID = *key.GroupID
	}
	models, err := h.relay.Models(c.Request.Context(), groupID)
	if err != nil {
		h.logger.Warn("获取模型列表失败", "err", err, "api_key_id", key.ID)
		fail(c, http.StatusServiceUnavailable, "暂时无法获取模型列表")
		return
	}

	// 返回 OpenAI 的 list 结构：走 /v1/models 的绝大多数是 OpenAI 系客户端，
	// Anthropic SDK 基本不调这个端点。
	data := make([]gin.H, 0, len(models))
	for _, m := range models {
		data = append(data, gin.H{
			"id":       m.ID,
			"object":   "model",
			"created":  0,
			"owned_by": "commandcode",
		})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

func truncateHeader(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
