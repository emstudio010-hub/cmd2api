package relay

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// ccEnvelope 是发往 /alpha/generate 的请求体。
//
// 用 struct 而不是 map：Go 的 encoding/json 按字段声明顺序输出，正好用来
// 对齐 CLI 的键序。commandcode-proxy 特意做了这一步重排，说明键序在上游
// 那边是可观测的，不能交给 map 的随机顺序。
type ccEnvelope struct {
	Config         ccConfig `json:"config"`
	Memory         any      `json:"memory"`
	Taste          any      `json:"taste"`
	Skills         any      `json:"skills"`
	PermissionMode string   `json:"permissionMode"`
	ThreadID       string   `json:"threadId,omitempty"`
	Mode           string   `json:"mode"`
	PromptCache    any      `json:"promptCache,omitempty"`
	Params         ccParams `json:"params"`
}

// ccConfig 伪装成本地 CLI 的项目上下文。
//
// 全部用伪造值：把宿主机的真实 cwd、分支、提交历史交给上游既没必要，
// 也和伪装出来的设备档案自相矛盾。
type ccConfig struct {
	WorkingDir    string `json:"workingDir"`
	Date          string `json:"date"`
	Environment   string `json:"environment"`
	Structure     []any  `json:"structure"`
	IsGitRepo     bool   `json:"isGitRepo"`
	CurrentBranch string `json:"currentBranch"`
	MainBranch    string `json:"mainBranch"`
	GitStatus     string `json:"gitStatus"`
	RecentCommits []any  `json:"recentCommits"`
}

type ccParams struct {
	Model             string      `json:"model"`
	Messages          []ccMessage `json:"messages"`
	MaxTokens         int         `json:"max_tokens"`
	Stream            bool        `json:"stream"`
	System            []ccPart    `json:"system,omitempty"`
	Temperature       *float64    `json:"temperature,omitempty"`
	ReasoningEffort   *string     `json:"reasoning_effort,omitempty"`
	Tools             []ccTool    `json:"tools"`
	ToolChoice        any         `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"`
}

type ccMessage struct {
	Role    string   `json:"role"`
	Content []ccPart `json:"content"`
}

// ccPart 是消息内容块。上游对各种块类型用同一套字段名，靠 type 区分。
//
// Text/MimeType 用指针：空字符串在这里是有意义的取值（例如带 cache_control
// 的空文本块），用 omitempty 会把它整个丢掉，指针才能区分「没这个字段」
// 和「字段就是空串」。
type ccPart struct {
	Type         string        `json:"type"`
	Text         *string       `json:"text,omitempty"`
	Image        string        `json:"image,omitempty"`
	MimeType     string        `json:"mimeType,omitempty"`
	ToolCallID   string        `json:"toolCallId,omitempty"`
	ToolName     string        `json:"toolName,omitempty"`
	Input        any           `json:"input,omitempty"`
	Output       *ccToolResult `json:"output,omitempty"`
	CacheControl any           `json:"cache_control,omitempty"`
}

type ccToolResult struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// ccTool 是工具定义。上游的形态是 Anthropic 风格，没有 type 字段。
type ccTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// toolNameAliases 复刻 CLI 发送前对工具名的重写表。
//
// 上游按这些名字识别内置工具，发原名会让它当成自定义工具、失去内建行为。
var toolNameAliases = map[string]string{
	"bash_output":         "shell_output",
	"task_output":         "shell_output",
	"tool_search":         "search_tools",
	"read_multiple_files": "read_file",
}

func toWireToolName(name string) string {
	if alias, ok := toolNameAliases[name]; ok {
		return alias
	}
	return name
}

// maxOutputTokensCap 是上游允许的 max_tokens 上限。
const maxOutputTokensCap = 200000

// defaultMaxTokens 是客户端未指定时的默认输出上限。
const defaultMaxTokens = 64000

// threadIDPattern 用于判断会话 ID 是否是合法 UUID。
//
// 只有合法 UUID 才能作为 threadId 放进信封，否则整个键省略——
// 这是 CLI 的 toWireThreadId 行为，上游会校验格式。
var threadIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// buildEnvelope 把归一化后的 OpenAI 请求包成 CC 信封。
func buildEnvelope(req *normalizedRequest, mode string, device DeviceProfile, threadID string, emptySystemPlaceholder bool) (*ccEnvelope, error) {
	systemBlocks, chatMessages := splitSystemMessages(req.Messages)

	ccMessages := make([]ccMessage, 0, len(chatMessages))
	// 先建一张 tool_call_id → 工具名 的反查表：上游的 tool 消息要求带 toolName，
	// 而 OpenAI 的 tool 消息只有 tool_call_id，名字只能从前面 assistant 的
	// tool_calls 里找回来。
	toolNames := collectToolNames(chatMessages)

	for _, msg := range chatMessages {
		ccMessages = append(ccMessages, convertMessage(msg, toolNames))
	}

	applyCacheBreakpoint(systemBlocks, ccMessages, req.PromptCacheKey)

	envelope := &ccEnvelope{
		Config: ccConfig{
			WorkingDir:    device.ProjectDir,
			Date:          time.Now().UTC().Format("2006-01-02"),
			Environment:   device.Platform,
			Structure:     []any{},
			IsGitRepo:     false,
			CurrentBranch: "",
			MainBranch:    "",
			GitStatus:     "",
			RecentCommits: []any{},
		},
		// CLI 发的就是 null，不是空对象/空串。
		Memory:         nil,
		Taste:          nil,
		Skills:         nil,
		PermissionMode: "standard",
		Mode:           mode,
		Params: ccParams{
			Model:    req.Model,
			Messages: ccMessages,
			// CC API 永远走流式；这里恒为 true。
			Stream: true,
		},
	}

	// 只有合法 UUID 才放 threadId。
	if threadIDPattern.MatchString(threadID) {
		envelope.ThreadID = threadID
	}

	maxTokens := defaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	if maxTokens > maxOutputTokensCap {
		maxTokens = maxOutputTokensCap
	}
	envelope.Params.MaxTokens = maxTokens

	switch {
	case len(systemBlocks) > 0:
		envelope.Params.System = systemBlocks
	case emptySystemPlaceholder:
		// 不带 system 时上游会注入它自带的约 7.5K token 默认提示词，既产生大量
		// 缓存 token 又污染对话（模型会以为自己在 CC 的可执行目录里）。
		// 发一个空格占位即可绕过。
		space := " "
		envelope.Params.System = []ccPart{{Type: "text", Text: &space}}
	}

	envelope.Params.Temperature = req.Temperature
	envelope.Params.ReasoningEffort = req.ReasoningEffort
	// CLI 总是下发 tools，没有工具时是空数组。空数组与缺键在上游是可区分的，
	// 所以这里也保证非 nil。
	envelope.Params.Tools = convertTools(req.Tools)
	envelope.Params.ToolChoice = convertToolChoice(req.ToolChoice)
	envelope.Params.ParallelToolCalls = req.ParallelToolCalls

	return envelope, nil
}

// splitSystemMessages 把 system/developer 消息抽出来，其余留给对话。
//
// OpenAI 的 system 和 developer 两种 role 都映射成 CC 的 system 块。
func splitSystemMessages(messages []map[string]any) ([]ccPart, []map[string]any) {
	var systemBlocks []ccPart
	chat := make([]map[string]any, 0, len(messages))

	for _, m := range messages {
		role, _ := m["role"].(string)
		if role != "system" && role != "developer" {
			chat = append(chat, m)
			continue
		}
		switch content := m["content"].(type) {
		case string:
			if content != "" {
				systemBlocks = append(systemBlocks, textPart(content))
			}
		case []any:
			for _, raw := range content {
				block, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				text, _ := block["text"].(string)
				if text == "" {
					text, _ = block["content"].(string)
				}
				cacheControl := block["cache_control"]
				// 空文本且没有缓存断点时没有意义，丢掉。
				if text == "" && cacheControl == nil {
					continue
				}
				part := textPart(text)
				part.CacheControl = cacheControl
				systemBlocks = append(systemBlocks, part)
			}
		default:
			if v, ok := m["content"]; ok && v != nil {
				systemBlocks = append(systemBlocks, textPart(toString(v)))
			}
		}
	}

	// 相邻 system 块之间补换行，避免拼接后语义粘连。
	for i := 0; i < len(systemBlocks)-1; i++ {
		if systemBlocks[i].Text != nil {
			merged := *systemBlocks[i].Text + "\n"
			systemBlocks[i].Text = &merged
		}
	}
	return systemBlocks, chat
}

// collectToolNames 从 assistant 消息的 tool_calls 里建 id → 名字的反查表。
func collectToolNames(messages []map[string]any) map[string]string {
	names := make(map[string]string)
	for _, m := range messages {
		if role, _ := m["role"].(string); role != "assistant" {
			continue
		}
		calls, _ := m["tool_calls"].([]any)
		for _, raw := range calls {
			call, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := call["id"].(string)
			if id == "" {
				continue
			}
			fn, _ := call["function"].(map[string]any)
			name, _ := fn["name"].(string)
			names[id] = name
		}
	}
	return names
}

// convertMessage 把一条 OpenAI 消息转成 CC 形态。
func convertMessage(msg map[string]any, toolNames map[string]string) ccMessage {
	role, _ := msg["role"].(string)

	switch role {
	case "user":
		return ccMessage{Role: "user", Content: convertUserContent(msg["content"])}

	case "assistant":
		parts := make([]ccPart, 0, 3)
		// 思考内容必须随历史回传：上游在 thinking 模式下会校验 reasoning 是否
		// 带回来了，丢掉会被直接拒绝。次序也要对齐抓包格式 ——
		// [reasoning, text, tool-call]，reasoning 在最前。
		if reasoning, ok := msg["reasoning_content"].(string); ok && reasoning != "" {
			parts = append(parts, textPartTyped("reasoning", reasoning))
		}
		switch content := msg["content"].(type) {
		case string:
			if content != "" {
				parts = append(parts, textPart(content))
			}
		case []any:
			for _, raw := range content {
				block, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				bt, _ := block["type"].(string)
				switch bt {
				case "text":
					text, _ := block["text"].(string)
					part := textPart(text)
					part.CacheControl = block["cache_control"]
					parts = append(parts, part)
				case "reasoning":
					// 已有 reasoning_content 字段就不重复添加。
					if _, dup := msg["reasoning_content"].(string); dup {
						continue
					}
					text, _ := block["text"].(string)
					parts = append(parts, textPartTyped("reasoning", text))
				}
			}
		}
		if calls, ok := msg["tool_calls"].([]any); ok {
			for _, raw := range calls {
				call, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				fn, _ := call["function"].(map[string]any)
				id, _ := call["id"].(string)
				name, _ := fn["name"].(string)
				parts = append(parts, ccPart{
					Type:       "tool-call",
					ToolCallID: id,
					ToolName:   name,
					Input:      parseArguments(fn["arguments"]),
				})
			}
		}
		return ccMessage{Role: "assistant", Content: parts}

	case "tool":
		id, _ := msg["tool_call_id"].(string)
		name := toolNames[id]
		if name == "" {
			name, _ = msg["name"].(string)
		}
		return ccMessage{
			Role: "tool",
			Content: []ccPart{{
				Type:       "tool-result",
				ToolCallID: id,
				ToolName:   name,
				Output:     &ccToolResult{Type: "text", Value: toWireToolOutput(msg["content"])},
			}},
		}
	}

	// 未知 role 兜底成 user，并保证 content 是数组——否则上游校验会拒绝。
	return ccMessage{Role: "user", Content: []ccPart{textPart(toString(msg["content"]))}}
}

// convertUserContent 处理 user 消息，支持纯文本和多模态数组。
func convertUserContent(content any) []ccPart {
	switch c := content.(type) {
	case string:
		return []ccPart{textPart(c)}
	case []any:
		parts := make([]ccPart, 0, len(c))
		for _, raw := range c {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			pt, _ := part["type"].(string)
			if pt == "image_url" {
				// OpenAI 的 image_url → CC 的 image。
				imageURL, _ := part["image_url"].(map[string]any)
				url, _ := imageURL["url"].(string)
				out := ccPart{Type: "image", Image: url}
				// 从 data URI 里取 mime 类型，上游要求单独给。
				if m := dataURIMime.FindStringSubmatch(url); len(m) == 2 {
					out.MimeType = m[1]
				}
				parts = append(parts, out)
				continue
			}
			text, _ := part["text"].(string)
			out := textPart(text)
			out.CacheControl = part["cache_control"]
			parts = append(parts, out)
		}
		return parts
	case nil:
		return []ccPart{textPart("")}
	default:
		return []ccPart{textPart(toString(c))}
	}
}

// applyCacheBreakpoint 处理缓存断点。
//
// 客户端已经在任意块上打过断点就原样保留；否则若给了 OpenAI 系的
// prompt_cache_key，就把断点落在 system 最后一块——缓存按前缀计算，
// system 正是最前面的那段前缀。
func applyCacheBreakpoint(systemBlocks []ccPart, messages []ccMessage, promptCacheKey string) {
	if promptCacheKey == "" || len(systemBlocks) == 0 {
		return
	}
	for _, b := range systemBlocks {
		if b.CacheControl != nil {
			return
		}
	}
	for _, m := range messages {
		for _, p := range m.Content {
			if p.CacheControl != nil {
				return
			}
		}
	}
	systemBlocks[len(systemBlocks)-1].CacheControl = map[string]any{"type": "ephemeral"}
}

// convertTools 把 OpenAI 工具定义转成 CC 形态。
func convertTools(tools []map[string]any) []ccTool {
	out := make([]ccTool, 0, len(tools))
	for _, t := range tools {
		fn, _ := t["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "" {
			name, _ = t["name"].(string)
		}
		if name == "" {
			continue
		}
		desc, _ := fn["description"].(string)
		if desc == "" {
			desc, _ = t["description"].(string)
		}
		schema := fn["parameters"]
		if schema == nil {
			schema = t["input_schema"]
		}
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, ccTool{
			Name:        toWireToolName(name),
			Description: desc,
			InputSchema: schema,
		})
	}
	return out
}

// convertToolChoice 把 OpenAI 的 tool_choice 转成 CC（Anthropic 风格）形态。
func convertToolChoice(choice any) any {
	switch c := choice.(type) {
	case nil:
		return nil
	case string:
		mapping := map[string]string{"auto": "auto", "none": "none", "required": "any"}
		mapped, ok := mapping[c]
		if !ok {
			mapped = "auto"
		}
		return map[string]any{"type": mapped}
	case map[string]any:
		if c["type"] == "function" {
			fn, _ := c["function"].(map[string]any)
			return map[string]any{"type": "tool", "name": fn["name"]}
		}
		// 已经是 Anthropic 风格就直接透传。
		return c
	}
	return nil
}

// toWireToolOutput 只取文本块，用换行拼接。
func toWireToolOutput(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, raw := range c {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] != "text" {
				continue
			}
			text, _ := block["text"].(string)
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	case nil:
		return ""
	default:
		return toString(c)
	}
}

// parseArguments 把工具调用参数解析成对象。
//
// OpenAI 的 arguments 是 JSON 字符串；解析失败时退回空对象而不是报错，
// 因为上游只要求 input 是个对象，格式坏掉不该让整个请求失败。
func parseArguments(raw any) any {
	s, ok := raw.(string)
	if !ok {
		if raw == nil {
			return map[string]any{}
		}
		return raw
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return map[string]any{}
	}
	return out
}

var dataURIMime = regexp.MustCompile(`^data:([^;,]+)`)

func textPart(text string) ccPart {
	return textPartTyped("text", text)
}

func textPartTyped(kind, text string) ccPart {
	t := text
	return ccPart{Type: kind, Text: &t}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
