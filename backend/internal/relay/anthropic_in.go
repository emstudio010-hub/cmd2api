package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件把入站的 Anthropic /v1/messages 请求体翻译成 OpenAI Chat Completions 形状。
//
// 逐项对齐参考实现 convertAnthropicToOpenAI（commandcode-proxy/proxy.mjs），
// 包括那些看起来像「怪癖」的边界行为，例如：
//   - assistant 消息的文本块即使为空也照样进 parts 数组（只在不带断点且只有一块时才折回字符串）；
//   - user 消息里 tool_result 排在文本前面（OpenAI 要求 tool 消息紧跟 assistant 的 tool_calls）；
//   - cache_control 断点原样保留，交给下游 buildEnvelope 下发；
//   - 顶层 system 的块数组只有在拼出的文本非空时才会产出一条 system 消息。
//
// 参考实现不做任何入参校验：类型不对时结果是「忽略这一项」而不是报错。这里保持一致，
// 因此 error 恒为 nil；保留返回值只是为了让调用方使用统一签名。

// ── 入站 body 取值辅助 ──────────────────────────────────
//
// 入站 body 是 encoding/json 解出来的 map[string]any：对象是 map[string]any，
// 数组是 []any，数字是 float64。参考实现里到处是 `x || ''`、`Array.isArray(x)`、
// `if (x)` 这类判断，下面几个函数逐个对应。

// anthropicTruthy 复刻 JS 的真值语义：nil / false / 0 / "" 为假，
// 而空数组、空对象在 JS 里恒为真（这点对 tools、stop_sequences 很重要）。
func anthropicTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	default:
		// []any / map[string]any：JS 里恒为真，哪怕为空
		return true
	}
}

// anthropicMapValue 取对象；不是对象（缺失、null、数组、标量）时返回 nil。
func anthropicMapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// anthropicSliceValue 取数组，对应 Array.isArray(x)。
//
// 返回 nil 表示「不是数组」，而不是「空数组」——调用方要靠这个区分
// msg.content 是字符串还是块数组。
func anthropicSliceValue(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// anthropicString 取字符串字段值，对应 `x || ”`：缺失、null、非字符串一律当空串。
func anthropicString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// anthropicJSONArgs 复刻 `JSON.stringify(input || {})`：缺失与 falsy 值当 {}，
// 其余原样序列化（字符串会被序列化成带引号的 JSON，与 JS 的行为一致）。
func anthropicJSONArgs(v any) string {
	switch t := v.(type) {
	case nil:
		return "{}"
	case bool:
		if !t {
			return "{}"
		}
	case float64:
		if t == 0 {
			return "{}"
		}
	case string:
		if t == "" {
			return "{}"
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// anthropicEffortForBudget 把 thinking.budget_tokens 折成 reasoning_effort
// （LiteLLM 的标准映射）。参考实现里 <2000 与 [2000,5000) 都是 low。
func anthropicEffortForBudget(v any) string {
	budget, ok := v.(float64)
	if !ok {
		// 非数字（含 null）在 JS 里比较结果为 false，一路落到 low
		return "low"
	}
	switch {
	case budget >= 10000:
		return "high"
	case budget >= 5000:
		return "medium"
	default:
		return "low"
	}
}

// ConvertAnthropicToOpenAI 把 Anthropic Messages 请求体翻译成 OpenAI Chat Completions 请求体。
//
// raw 应当是已反序列化的入站 body；nil 视为一个空 body（等价于 JS 的 {}）。
func ConvertAnthropicToOpenAI(raw map[string]any) (map[string]any, error) {
	// 1. 抽取 system（在顶层，不在 messages 里）
	systemPrompt := ""
	var systemBlocks []any
	if sys, ok := raw["system"]; ok && anthropicTruthy(sys) {
		if s, isStr := sys.(string); isStr {
			systemPrompt = s
		} else if arr := anthropicSliceValue(sys); arr != nil {
			// 保留 cache_control：下游 buildEnvelope 需要块数组才能把断点下发
			//（Claude Code 的 params.system 就是块数组）。
			for _, item := range arr {
				b := anthropicMapValue(item)
				if b == nil || anthropicString(b["type"]) != "text" {
					continue
				}
				blk := map[string]any{"type": "text", "text": anthropicString(b["text"])}
				if cc, ok := b["cache_control"]; ok && anthropicTruthy(cc) {
					blk["cache_control"] = cc
				}
				systemBlocks = append(systemBlocks, blk)
			}
			texts := make([]string, 0, len(systemBlocks))
			for _, blk := range systemBlocks {
				texts = append(texts, anthropicString(blk.(map[string]any)["text"]))
			}
			systemPrompt = strings.Join(texts, "\n")
		}
	}

	// 2. 建 tool_use_id → 工具名 映射，并转换 messages
	toolNameFromID := map[string]string{}
	openaiMessages := []any{}

	if systemPrompt != "" {
		// 有块数组时下发块数组（保留断点），否则下发字符串
		content := any(systemPrompt)
		if len(systemBlocks) > 0 {
			content = systemBlocks
		}
		openaiMessages = append(openaiMessages, map[string]any{"role": "system", "content": content})
	}

	// messages 不是数组时参考实现会因 for...of 不可迭代而抛错；这里按「没有消息」处理。
	for _, item := range anthropicSliceValue(raw["messages"]) {
		msg := anthropicMapValue(item)
		if msg == nil {
			continue
		}
		switch anthropicString(msg["role"]) {
		case "assistant":
			openaiMessages = append(openaiMessages, convertAnthropicAssistantMessage(msg, toolNameFromID))
		case "user":
			openaiMessages = append(openaiMessages, convertAnthropicUserMessages(msg, toolNameFromID)...)
		}
	}

	// 3. 组装 OpenAI 请求
	model := anthropicString(raw["model"])
	if model == "" {
		model = "deepseek/deepseek-v4-flash"
	}
	var maxTokens any = 64000
	if v, ok := raw["max_tokens"]; ok && anthropicTruthy(v) {
		maxTokens = v
	}
	stream := false
	if b, ok := raw["stream"].(bool); ok {
		stream = b // 严格等于 true，对应 `stream === true`
	}
	openaiReq := map[string]any{
		"model":      model,
		"messages":   openaiMessages,
		"max_tokens": maxTokens,
		"stream":     stream,
	}

	// 4. 映射 tools
	if tools := anthropicSliceValue(raw["tools"]); len(tools) > 0 {
		converted := make([]any, 0, len(tools))
		for _, item := range tools {
			t := anthropicMapValue(item)
			if t == nil {
				t = map[string]any{}
			}
			var parameters any = map[string]any{"type": "object", "properties": map[string]any{}}
			if v, ok := t["input_schema"]; ok && anthropicTruthy(v) {
				parameters = v
			}
			converted = append(converted, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        anthropicString(t["name"]),
					"description": anthropicString(t["description"]),
					"parameters":  parameters,
				},
			})
		}
		openaiReq["tools"] = converted
	}

	// 5. 映射 tool_choice
	if tcVal, ok := raw["tool_choice"]; ok && anthropicTruthy(tcVal) {
		tc := anthropicMapValue(tcVal)
		var tv any
		var hasType bool
		if tc != nil {
			tv, hasType = tc["type"]
		}
		switch {
		// type 缺席（tc 不是对象时也缺席）当 auto；显式 null 则什么都不设
		case !hasType || tv == "auto":
			openaiReq["tool_choice"] = "auto"
		case tv == "any":
			openaiReq["tool_choice"] = "required"
		case tv == "tool":
			openaiReq["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": anthropicString(tc["name"])},
			}
		case tv == "none":
			openaiReq["tool_choice"] = "none"
		}
	}

	// 6. 可选参数。temperature / top_p 只看「字段在不在」（undefined 才跳过），
	//    所以显式 null 也会照搬过去。
	if v, ok := raw["temperature"]; ok {
		openaiReq["temperature"] = v
	}
	if v, ok := raw["top_p"]; ok {
		openaiReq["top_p"] = v
	}
	if v, ok := raw["stop_sequences"]; ok && anthropicTruthy(v) {
		openaiReq["stop"] = v
	}
	if md := anthropicMapValue(raw["metadata"]); md != nil {
		if v, ok := md["user_id"]; ok && anthropicTruthy(v) {
			openaiReq["user"] = v
		}
	}

	// 7. Anthropic thinking → reasoning_effort
	if tv, ok := raw["thinking"]; ok && anthropicTruthy(tv) {
		if t := anthropicMapValue(tv); t != nil {
			switch anthropicString(t["type"]) {
			case "disabled", "none":
				// 不发 reasoning_effort
			case "adaptive":
				var effort any = "medium"
				if v, ok := t["effort"]; ok && v != nil { // JS 的 ?? 只看 null/undefined
					effort = v
				}
				openaiReq["reasoning_effort"] = effort
			default:
				// 含 type:'enabled' 以及没有 type 但有预算的情形
				if v, ok := t["budget_tokens"]; ok { // JS 的 !== undefined：显式 null 也算
					openaiReq["reasoning_effort"] = anthropicEffortForBudget(v)
				}
			}
		}
	}

	return openaiReq, nil
}

// convertAnthropicAssistantMessage 把一条 assistant 消息压成一条 OpenAI assistant 消息。
//
// 文本既可能走字符串也可能走块数组：只有「多块」或「带缓存断点」时才用块数组，
// 其余情况折回纯字符串，保持线上形状（wire shape）不变。
func convertAnthropicAssistantMessage(msg map[string]any, toolNameFromID map[string]string) map[string]any {
	textContent := ""
	// Anthropic 的 thinking 块承载思考内容，要转成 reasoning_content 回传，
	// 否则上游会因缺少 reasoning 而拒绝。
	thinkingContent := ""
	textParts := []any{}
	textHasCache := false
	toolCalls := []any{}

	blocks := anthropicSliceValue(msg["content"])
	if blocks == nil {
		blocks = []any{map[string]any{"type": "text", "text": anthropicString(msg["content"])}}
	}
	for _, item := range blocks {
		block := anthropicMapValue(item)
		if block == nil {
			continue
		}
		switch anthropicString(block["type"]) {
		case "text":
			text := anthropicString(block["text"])
			textContent += text
			part := map[string]any{"type": "text", "text": text}
			if cc, ok := block["cache_control"]; ok && anthropicTruthy(cc) {
				part["cache_control"] = cc
				textHasCache = true
			}
			textParts = append(textParts, part)
		case "thinking":
			thinkingContent += anthropicString(block["thinking"])
		case "tool_use":
			id := anthropicString(block["id"])
			name := anthropicString(block["name"])
			toolNameFromID[id] = name
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": anthropicJSONArgs(block["input"]),
				},
			})
		}
	}

	assistantMsg := map[string]any{"role": "assistant"}
	if len(textParts) > 1 || textHasCache {
		assistantMsg["content"] = textParts
	} else if textContent != "" {
		assistantMsg["content"] = textContent
	} else {
		// JS 的 `textContent || null`：空文本落成 null，而不是 ""
		assistantMsg["content"] = nil
	}
	if thinkingContent != "" {
		assistantMsg["reasoning_content"] = thinkingContent
	}
	if len(toolCalls) > 0 {
		assistantMsg["tool_calls"] = toolCalls
	}
	return assistantMsg
}

// convertAnthropicUserMessages 转换一条 user 消息，可能产出多条 OpenAI 消息：
// 每个 tool_result 独立成一条 role:"tool" 消息，且排在文本消息之前。
func convertAnthropicUserMessages(msg map[string]any, toolNameFromID map[string]string) []any {
	out := []any{}
	textContent := ""
	// parts 保持原始顺序（text / image_url）
	parts := []any{}
	textHasCache := false
	toolResults := []any{}

	if s, ok := msg["content"].(string); ok {
		textContent = s
	} else if arr := anthropicSliceValue(msg["content"]); arr != nil {
		for _, item := range arr {
			block := anthropicMapValue(item)
			if block == nil {
				continue
			}
			switch anthropicString(block["type"]) {
			case "text":
				text := anthropicString(block["text"])
				textContent += text
				part := map[string]any{"type": "text", "text": text}
				if cc, ok := block["cache_control"]; ok && anthropicTruthy(cc) {
					part["cache_control"] = cc
					textHasCache = true
				}
				parts = append(parts, part)
			case "image":
				// Anthropic 图片块：{type:'image', source:{type:'base64', media_type, data}}
				// 或 source.url 形式
				src := anthropicMapValue(block["source"])
				url := ""
				if src != nil && anthropicString(src["type"]) == "base64" && anthropicString(src["data"]) != "" {
					mimeType := anthropicString(src["media_type"])
					if mimeType == "" {
						mimeType = "image/png"
					}
					url = "data:" + mimeType + ";base64," + anthropicString(src["data"])
				} else if src != nil {
					url = anthropicString(src["url"])
				}
				if url != "" {
					parts = append(parts, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": url},
					})
				}
			case "tool_result":
				toolResults = append(toolResults, block)
			}
		}
	}

	// 参考实现这里有个空 if 分支，注释说明了原因：tool_result 要优先入队 ——
	// OpenAI 语义要求 tool 消息紧跟 assistant 的 tool_calls，同一条 user 消息里的
	// 文本必须排在 tool 结果之后。所以文本先不定稿，等 tool 消息发完再说。

	for _, item := range toolResults {
		tr, _ := item.(map[string]any)
		toolContent := ""
		switch c := tr["content"].(type) {
		case string:
			toolContent = c
		case []any:
			chunks := make([]string, 0, len(c))
			for _, ci := range c {
				chunks = append(chunks, anthropicString(anthropicMapValue(ci)["text"]))
			}
			toolContent = strings.Join(chunks, "\n")
		default:
			// 对应 JS 的 String(tr.content || '')：null/缺失 → ""
			if anthropicTruthy(tr["content"]) {
				toolContent = fmt.Sprint(tr["content"])
			}
		}
		toolMsg := map[string]any{"role": "tool", "content": toolContent}
		// tool_call_id 缺失时 JS 的 undefined 字段会被 JSON.stringify 整个丢掉，
		// 这里也只在字段存在时才写入。
		if v, ok := tr["tool_use_id"]; ok {
			toolMsg["tool_call_id"] = v
		}
		// OpenAI 语义里 tool 消息的 name 是可选的；会话恢复等场景下 tool_use_id
		// 可能找不到对应的 assistant tool_use（历史被客户端裁剪），此时不硬塞空
		// name，避免上游报 "Tool result is missing"。
		if name := toolNameFromID[anthropicString(tr["tool_use_id"])]; name != "" {
			toolMsg["name"] = name
		}
		out = append(out, toolMsg)
	}

	if len(parts) > 0 || textContent != "" {
		// 单块纯文本仍用字符串（线格不变）；多块 / 带断点 / 含图片时用块数组。
		// 注意：content 为字符串时 parts 为空，判空必须同时看 textContent，
		// 否则整条消息会丢。
		singleText := len(parts) <= 1 &&
			(len(parts) == 0 || anthropicString(parts[0].(map[string]any)["type"]) == "text") &&
			!textHasCache
		var content any
		if singleText {
			content = textContent
		} else {
			content = parts
		}
		out = append(out, map[string]any{"role": "user", "content": content})
	}

	return out
}
