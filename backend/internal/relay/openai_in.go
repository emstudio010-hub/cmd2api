package relay

import (
	"encoding/json"
	"errors"
	"fmt"
)

// normalizedRequest 是归一化后的请求。无论客户端说 OpenAI 还是 Anthropic，
// 走到信封构造这一步都是这个形状。
//
// 用 map 装 messages/tools 而不是强类型 struct：客户端可能带任意扩展字段，
// 强类型化会静默丢字段，而透传是这里的正确行为。
type normalizedRequest struct {
	Model             string
	Messages          []map[string]any
	MaxTokens         *int
	Temperature       *float64
	Tools             []map[string]any
	Stream            bool
	ReasoningEffort   *string
	ToolChoice        any
	ParallelToolCalls *bool
	PromptCacheKey    string

	// SessionHints 是客户端自带的会话标识候选，按优先级排列。
	SessionHints []string
}

// errEmptyMessages 表示请求里没有可用的对话消息。
var errEmptyMessages = errors.New("messages 不能为空")

// toOpenAIBody 把归一化请求渲染回 OpenAI Chat Completions 形状。
//
// 给 OpenCode 这类 OpenAI 兼容上游用：它们要的就是 OpenAI 格式，
// 不需要经过 Command Code 的信封包装。这样入站无论是哪种协议，
// 到 OpenCode 这一路都会先被归一成 OpenAI 形状再发出去。
func (r *normalizedRequest) toOpenAIBody() ([]byte, error) {
	body := map[string]any{
		"model":    r.Model,
		"messages": r.Messages,
		// 内部一律按流处理，所以向 OpenCode 也始终要流式。
		"stream": true,
		// 必须显式要求上游在最后一片里带用量，否则 token 统计拿不到，
		// 用量日志会全是 0。非 OpenAI 官方实现可能忽略这个字段，但没有副作用。
		"stream_options": map[string]any{"include_usage": true},
	}
	if r.MaxTokens != nil {
		body["max_tokens"] = *r.MaxTokens
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if len(r.Tools) > 0 {
		body["tools"] = r.Tools
	}
	if r.ToolChoice != nil {
		body["tool_choice"] = r.ToolChoice
	}
	if r.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *r.ParallelToolCalls
	}
	if r.ReasoningEffort != nil {
		body["reasoning_effort"] = *r.ReasoningEffort
	}
	if r.PromptCacheKey != "" {
		body["prompt_cache_key"] = r.PromptCacheKey
	}
	return json.Marshal(body)
}

// ParseOpenAIChatRequest 解析 OpenAI Chat Completions 请求。
func ParseOpenAIChatRequest(raw []byte) (*normalizedRequest, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("解析请求体: %w", err)
	}
	return normalizeOpenAIShape(body)
}

// ParseAnthropicMessagesRequest 解析 Anthropic Messages 请求。
//
// 先转成 OpenAI 形状再走同一条归一化路径——这样两种协议在后续所有环节
// 共享同一套代码，不必维护两条平行的流水线。
func ParseAnthropicMessagesRequest(raw []byte) (*normalizedRequest, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("解析请求体: %w", err)
	}
	converted, err := ConvertAnthropicToOpenAI(body)
	if err != nil {
		return nil, fmt.Errorf("转换 Anthropic 请求: %w", err)
	}
	return normalizeOpenAIShape(converted)
}

// normalizeOpenAIShape 把 OpenAI 形状的 body 读进 normalizedRequest。
func normalizeOpenAIShape(body map[string]any) (*normalizedRequest, error) {
	req := &normalizedRequest{
		PromptCacheKey: stringField(body, "prompt_cache_key"),
	}

	req.Model = stringField(body, "model")
	if req.Model == "" {
		return nil, errors.New("缺少 model 字段")
	}

	req.Messages = messageList(body["messages"])
	if len(req.Messages) == 0 {
		return nil, errEmptyMessages
	}

	req.MaxTokens = intField(body, "max_tokens", "max_completion_tokens")
	req.Temperature = floatField(body, "temperature")
	req.ReasoningEffort = stringFieldPtr(body, "reasoning_effort")
	req.ToolChoice = body["tool_choice"]
	req.ParallelToolCalls = boolFieldPtr(body, "parallel_tool_calls")
	req.Tools = toolList(body["tools"])

	// 客户端没显式给 stream 时按非流式处理；但到底怎么回由 service 决定，
	// CC 上游永远是流式的。
	req.Stream, _ = body["stream"].(bool)

	req.SessionHints = sessionHints(body)
	return req, nil
}

// sessionHints 收集客户端自带的会话标识。
//
// 沿用同一个会话 ID 是正常 CLI 的行为；cmd2api 擅自重新分配反而会制造出
// 「每次请求都是新会话」的异常模式。
func sessionHints(body map[string]any) []string {
	var hints []string
	for _, key := range []string{"session_id", "x-session-id", "x-claude-code-session-id"} {
		if v := stringField(body, key); v != "" {
			hints = append(hints, v)
		}
	}
	if v := stringField(body, "prompt_cache_key"); v != "" {
		hints = append(hints, v)
	}
	// metadata.user_id 是 Anthropic SDK 常用的会话标记位置。
	if md, ok := body["metadata"].(map[string]any); ok {
		if v := stringField(md, "user_id"); v != "" {
			hints = append(hints, v)
		}
	}
	return hints
}

// ---- 宽松的类型读取 ----
//
// JSON 里数字可能是 float64 也可能是 json.Number，还可能是字符串；
// 客户端五花八门，这里统一容错，不做严格校验。

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func stringFieldPtr(m map[string]any, key string) *string {
	if v, ok := m[key].(string); ok && v != "" {
		return &v
	}
	return nil
}

func floatField(m map[string]any, key string) *float64 {
	switch v := m[key].(type) {
	case float64:
		return &v
	case int:
		f := float64(v)
		return &f
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return &f
		}
	}
	return nil
}

func boolFieldPtr(m map[string]any, key string) *bool {
	if v, ok := m[key].(bool); ok {
		return &v
	}
	return nil
}

func intField(m map[string]any, keys ...string) *int {
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			n := int(v)
			return &n
		case int:
			n := v
			return &n
		case json.Number:
			if n, err := v.Int64(); err == nil {
				i := int(n)
				return &i
			}
		}
	}
	return nil
}

func messageList(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func toolList(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
