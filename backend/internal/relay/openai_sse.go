package relay

import (
	"encoding/json"
	"strings"
)

// openAIStreamAdapter 把 OpenAI 兼容上游的 SSE 流适配成 CC 事件流。
//
// 这样做的价值：OpenCode 上游说的是 OpenAI 协议、Command Code 上游说的是自有
// NDJSON 协议，但**下游**的翻译器（OpenAIStreamEncoder / AnthropicStreamEncoder）
// 只需要认一种中间事件格式。适配一次，两种上游就复用同一套已经测过的编码器，
// 不必为「OpenCode → Anthropic」再写一条独立的翻译链。
//
// 代价是经过一次格式往返，OpenAI 侧一些本项目不关心的字段（logprobs 等）会丢。
// 对本项目的用途来说这是可接受的取舍。
type openAIStreamAdapter struct {
	// current 是正在累积的工具调用。OpenAI 把一次工具调用的参数拆成多个分片
	// 陆续发来，必须攒齐了才能拼出完整参数。
	current *pendingToolCall
	// finished 收集已经结束的工具调用，按出现顺序保留。
	finished []*pendingToolCall

	finishReason string
	usage        *CCUsage
	sawDone      bool
}

type pendingToolCall struct {
	Index int
	ID    string
	Name  string
	args  strings.Builder
}

// newOpenAIStreamAdapter 构造适配器。
func newOpenAIStreamAdapter() *openAIStreamAdapter {
	return &openAIStreamAdapter{}
}

// openAIStreamChunk 是 OpenAI 流式响应的一个分片。
type openAIStreamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// ParseLine 解析一行 SSE，返回应向下游转发的事件。
//
// 传入的应当是去掉 "data: " 前缀后的内容（由调用方负责剥离）。
func (a *openAIStreamAdapter) ParseLine(payload string) []*CCEvent {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil
	}
	if payload == "[DONE]" {
		a.sawDone = true
		return nil
	}

	var chunk openAIStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		// 上游偶尔会插入非 JSON 的心跳行，跳过而不是中断整条流。
		return nil
	}

	// 有的兼容实现会把错误直接放在 200 的流里。
	if chunk.Error != nil {
		return []*CCEvent{{
			Type:    "error",
			Message: chunk.Error.Message,
			Error: &CCError{
				Message: chunk.Error.Message,
				Code:    errorCodeString(chunk.Error.Code),
			},
		}}
	}

	// 用量通常只在最后一片里出现（需要上游开启 include_usage）。
	if chunk.Usage != nil {
		a.usage = &CCUsage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
		}
		if chunk.Usage.PromptTokensDetails != nil {
			a.usage.CachedInputTokens = chunk.Usage.PromptTokensDetails.CachedTokens
		}
	}

	var out []*CCEvent
	for _, choice := range chunk.Choices {
		if choice.Delta.ReasoningContent != "" {
			out = append(out, &CCEvent{Type: "reasoning-delta", Text: choice.Delta.ReasoningContent})
		}
		if choice.Delta.Content != "" {
			out = append(out, &CCEvent{Type: "text-delta", Text: choice.Delta.Content})
		}

		for _, tc := range choice.Delta.ToolCalls {
			// 出现新的 index 意味着上一个工具调用已经发完了，先把它吐出去。
			// 这样工具调用能尽早到达下游，而不是全攒到最后一起发。
			if a.current != nil && tc.Index != a.current.Index {
				if ev := a.flushCurrent(); ev != nil {
					out = append(out, ev)
				}
			}
			if a.current == nil {
				a.current = &pendingToolCall{Index: tc.Index}
			}
			if tc.ID != "" {
				a.current.ID = tc.ID
			}
			if tc.Function.Name != "" {
				a.current.Name = tc.Function.Name
			}
			a.current.args.WriteString(tc.Function.Arguments)
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			a.finishReason = *choice.FinishReason
		}
	}

	return out
}

// Flush 在流结束时调用，吐出最后一个工具调用和 finish 事件。
func (a *openAIStreamAdapter) Flush() []*CCEvent {
	var out []*CCEvent
	if a.current != nil {
		if ev := a.flushCurrent(); ev != nil {
			out = append(out, ev)
		}
	}
	if a.finishReason != "" || a.usage != nil {
		ev := &CCEvent{Type: "finish", FinishReason: a.finishReason}
		if a.usage != nil {
			ev.TotalUsage = a.usage
		}
		out = append(out, ev)
	}
	return out
}

// flushCurrent 把正在累积的工具调用转成一个完整事件。
func (a *openAIStreamAdapter) flushCurrent() *CCEvent {
	tc := a.current
	a.current = nil
	if tc == nil {
		return nil
	}

	args := strings.TrimSpace(tc.args.String())
	if args == "" {
		args = "{}"
	}
	// 参数可能不是合法 JSON（上游被截断等），交给下游编码器兜底成 {}，
	// 这里不预先丢弃——否则会连工具名一起丢掉。
	ev := &CCEvent{
		Type:       "tool-call",
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Input:      json.RawMessage(args),
	}
	a.finished = append(a.finished, tc)
	return ev
}

// errorCodeString 把可能是字符串或数字的 code 归一成字符串。
func errorCodeString(code any) string {
	switch v := code.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// SplitSSELine 从一行 SSE 里取出 data 内容。
//
// 返回 ok=false 表示这行不是数据行（空行、注释、event: 行等），应当跳过。
func SplitSSELine(line string) (payload string, ok bool) {
	line = strings.TrimRight(line, "\r")
	if line == "" || strings.HasPrefix(line, ":") {
		return "", false
	}
	after, found := strings.CutPrefix(line, "data:")
	if !found {
		// event: / id: / retry: 这些字段本项目不用——OpenAI 协议靠 JSON 里的
		// 字段区分事件类型，不靠 SSE 的 event 名。
		return "", false
	}
	return strings.TrimSpace(after), true
}
