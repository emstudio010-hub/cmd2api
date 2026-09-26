package relay

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// openAIChunk 是发回客户端的 OpenAI 流式分片。
//
// 用 struct 保证 id/object/created/model/choices 的字段顺序稳定——
// 虽然 SDK 按名字取值，但保持与官方响应一致便于抓包对比。
type openAIChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens        int            `json:"prompt_tokens"`
	CompletionTokens    int            `json:"completion_tokens"`
	TotalTokens         int            `json:"total_tokens"`
	PromptTokensDetails map[string]int `json:"prompt_tokens_details,omitempty"`
}

// OpenAIStreamEncoder 把上游 NDJSON 事件编码成 OpenAI 兼容的 SSE 帧。
type OpenAIStreamEncoder struct {
	model        string
	completionID string
	created      int64

	sawFinish     bool
	chunkIndex    int
	finishReason  string
	usage         *CCUsage
	toolCallIndex int

	// UpstreamError 非 nil 表示上游在流里报了错，调用方据此决定收尾方式。
	UpstreamError *MappedError

	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

// NewOpenAIStreamEncoder 为一次请求创建编码器。
func NewOpenAIStreamEncoder(model, completionID string, created int64) *OpenAIStreamEncoder {
	return &OpenAIStreamEncoder{
		model:        model,
		completionID: completionID,
		created:      created,
		finishReason: "stop",
	}
}

// Encode 消费一个上游事件，返回需要写给客户端的 SSE 帧。
func (e *OpenAIStreamEncoder) Encode(ev *CCEvent) []string {
	if ev == nil || ev.Type == "" {
		return nil
	}

	switch ev.Type {
	case "text-start", "reasoning-start", "start", "start-step",
		"reasoning-end", "provider-metadata", "tool-input-start",
		"tool-input-delta", "tool-input-end", "tool-error", "text-end":
		// 这些事件没有对客户端可见的内容。
		return nil

	case "text-delta":
		text := ev.Text
		if text == "" {
			text = ev.Delta
		}
		if text == "" {
			return nil
		}
		delta := map[string]any{"content": text}
		if e.chunkIndex == 0 {
			delta["role"] = "assistant"
		}
		e.chunkIndex++
		return []string{e.frame(delta, nil, nil)}

	case "reasoning-delta":
		if ev.Text == "" {
			return nil
		}
		delta := map[string]any{"reasoning_content": ev.Text}
		if e.chunkIndex == 0 {
			delta["role"] = "assistant"
		}
		e.chunkIndex++
		return []string{e.frame(delta, nil, nil)}

	case "tool-call":
		id := ev.ToolCallID
		if id == "" {
			id = fmt.Sprintf("call_%d_%d", e.created, e.toolCallIndex)
		}
		args := "{}"
		if len(ev.Input) > 0 {
			// 上游可能把 input 发成字符串或对象。字符串要原样保留
			// （它本就是 JSON 文本），对象才需要重新序列化。
			var asString string
			if err := json.Unmarshal(ev.Input, &asString); err == nil {
				args = asString
			} else {
				args = string(ev.Input)
			}
		}
		entry := map[string]any{
			"index": e.toolCallIndex,
			"id":    id,
			"type":  "function",
			"function": map[string]any{
				"name":      ev.ToolName,
				"arguments": args,
			},
		}
		delta := map[string]any{"tool_calls": []any{entry}}
		if e.chunkIndex == 0 {
			// 首个分片要显式带上 content: null，与官方行为一致。
			delta["role"] = "assistant"
			delta["content"] = nil
		}
		e.chunkIndex++
		e.toolCallIndex++
		return []string{e.frame(delta, nil, nil)}

	case "finish-step":
		e.sawFinish = true
		if ev.FinishReason != "" {
			e.finishReason = mapFinishReason(ev.FinishReason)
		}
		if ev.Usage != nil {
			e.usage = ev.Usage
			e.absorbUsage(ev.Usage)
		}
		return nil

	case "finish":
		e.sawFinish = true
		if ev.FinishReason != "" {
			e.finishReason = mapFinishReason(ev.FinishReason)
		}
		u := ev.TotalUsage
		if u == nil {
			u = e.usage
		}
		if u == nil {
			u = &CCUsage{}
		}
		normalizeUsage(u)
		e.absorbUsage(u)

		reason := e.finishReason
		usage := &openAIUsage{
			PromptTokens:     u.InputTokens,
			CompletionTokens: u.OutputTokens,
			TotalTokens:      u.InputTokens + u.OutputTokens,
		}
		if u.CachedInputTokens > 0 {
			usage.PromptTokensDetails = map[string]int{"cached_tokens": u.CachedInputTokens}
		}
		return []string{e.frame(map[string]any{}, &reason, usage)}

	case "error":
		// 刻意不发 finish_reason 分片：交给流的自然结束处理。否则后续的
		// finish(tool_calls) 会被「见到第一个 finish_reason 就停」的
		// 客户端 agent 循环忽略掉。
		e.UpstreamError = MapEventError(ev)
		return nil
	}
	return nil
}

// Finish 收尾。返回是否检测到上游流被截断。
//
// 「截断」的口径是上游一个完成信号都没给就断了。上游给过 finish-step 也算数，
// 不能把它误判成截断——那会把本来正常的响应报成 502。
func (e *OpenAIStreamEncoder) Finish() (truncated bool, detail string) {
	if !e.sawFinish {
		return true, "no finish event"
	}
	if e.finishReason == "upstream_error" {
		return true, "provider reported an upstream connection failure"
	}
	return false, ""
}

// DoneFrame 返回 OpenAI 流的结束标记。
func (e *OpenAIStreamEncoder) DoneFrame() string {
	return "data: [DONE]\n\n"
}

func (e *OpenAIStreamEncoder) absorbUsage(u *CCUsage) {
	if u == nil {
		return
	}
	e.InputTokens = u.InputTokens
	e.OutputTokens = u.OutputTokens
	e.CachedInputTokens = u.CachedInputTokens
}

func (e *OpenAIStreamEncoder) frame(delta map[string]any, finishReason *string, usage *openAIUsage) string {
	chunk := openAIChunk{
		ID:      e.completionID,
		Object:  "chat.completion.chunk",
		Created: e.created,
		Model:   e.model,
		Choices: []openAIChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
		Usage:   usage,
	}
	// json.Marshal 对这几个字段不会失败（全是基础类型），忽略错误是安全的；
	// 真失败也只影响这一个分片，不该中断整条流。
	payload, err := json.Marshal(chunk)
	if err != nil {
		return ""
	}
	return "data: " + string(payload) + "\n\n"
}

// BuildOpenAIFullResponse 把非流式请求的完整回答组装成 chat.completion 对象。
func BuildOpenAIFullResponse(model, completionID string, created int64, text, reasoning string, toolCalls []openAIToolCall, finishReason string, usage *CCUsage) map[string]any {
	message := map[string]any{"role": "assistant", "content": text}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		// 有工具调用时 content 按惯例置空。
		if text == "" {
			message["content"] = nil
		}
	}

	if usage == nil {
		usage = &CCUsage{}
	}
	total := usage.InputTokens + usage.OutputTokens

	return map[string]any{
		"id":      completionID,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     usage.InputTokens,
			"completion_tokens": usage.OutputTokens,
			"total_tokens":      total,
		},
	}
}

// openAIToolCall 是非流式响应里的工具调用条目。
type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// collectToolCalls 把流式过程中攒下的 tool-call 事件转成非流式响应用的数组。
func collectToolCalls(events []*CCEvent) []openAIToolCall {
	var out []openAIToolCall
	for i, ev := range events {
		if ev.Type != "tool-call" {
			continue
		}
		id := ev.ToolCallID
		if id == "" {
			id = "call_" + strconv.Itoa(i)
		}
		var call openAIToolCall
		call.ID = id
		call.Type = "function"
		call.Function.Name = ev.ToolName
		if len(ev.Input) > 0 {
			call.Function.Arguments = string(ev.Input)
		} else {
			call.Function.Arguments = "{}"
		}
		out = append(out, call)
	}
	return out
}
