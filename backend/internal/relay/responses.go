package relay

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// writeClientError 按入站协议的错误格式返回。
//
// 两种协议的错误信封不同，客户端 SDK 只认自己那一套：给 Anthropic SDK 发
// OpenAI 的 {"error":{"message":...}} 会被解析失败，用户看到的是一个
// 语焉不详的解析错误而不是真正的原因。
func (s *Service) writeClientError(w http.ResponseWriter, protocol Protocol, status int, errType, message string, retryAfter int) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.Header().Set("Content-Type", "application/json")

	var payload any
	if protocol == ProtocolAnthropic {
		payload = map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    errType,
				"message": message,
			},
		}
	} else {
		payload = map[string]any{
			"error": map[string]any{
				"message": message,
				"type":    errType,
			},
		}
	}

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeStreamError 在流已经开始、无法再改状态码时补一个错误帧。
func (s *Service) writeStreamError(w http.ResponseWriter, protocol Protocol, mapped *MappedError) {
	if protocol == ProtocolAnthropic {
		frame := map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    mapped.Type,
				"message": mapped.Message,
			},
		}
		writeSSEFrame(w, "error", frame)
		return
	}

	frame := map[string]any{
		"error": map[string]any{
			"message": mapped.Message,
			"type":    mapped.Type,
		},
	}
	if b, err := json.Marshal(frame); err == nil {
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeSSEFrame 写一个 Anthropic 风格的事件帧。
func writeSSEFrame(w http.ResponseWriter, event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("event: " + event + "\ndata: " + string(b) + "\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeOpenAIFullResponse 返回非流式的 OpenAI 完整响应。
func (s *Service) writeOpenAIFullResponse(w http.ResponseWriter, model, requestID string, created int64,
	text, reasoning string, toolCalls []openAIToolCall, finishReason string, usage *CCUsage) {

	body := BuildOpenAIFullResponse(model, "chatcmpl-"+requestID, created, text, reasoning, toolCalls, finishReason, usage)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// writeAnthropicFullResponse 返回非流式的 Anthropic Messages 响应。
func (s *Service) writeAnthropicFullResponse(w http.ResponseWriter, model, requestID string,
	text, reasoning string, toolCalls []openAIToolCall, finishReason string, usage *CCUsage) {

	content := make([]map[string]any, 0, len(toolCalls)+2)
	// 思考块必须排在文本块之前，与流式路径的次序保持一致。
	if reasoning != "" {
		content = append(content, map[string]any{
			"type":      "thinking",
			"thinking":  reasoning,
			"signature": FakeThinkingSignature(reasoning),
		})
	}
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, call := range toolCalls {
		var input any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
			input = map[string]any{}
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Function.Name,
			"input": input,
		})
	}

	inputTokens := anthropicInputTokens(usage, nil)
	cacheCreation, cacheRead := cacheTokens(usage)

	body := map[string]any{
		"id":            "msg_" + requestID,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   MapAnthropicStopReason(finishReason),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                inputTokens,
			"output_tokens":               usage.OutputTokens,
			"cache_creation_input_tokens": cacheCreation,
			"cache_read_input_tokens":     cacheRead,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// GenerateRequestID 生成一个请求 ID。
//
// 用时间戳加随机后缀：既能在日志里按时间排序，又能避免同一毫秒内碰撞。
func GenerateRequestID() string {
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + randomHex(4)
}
