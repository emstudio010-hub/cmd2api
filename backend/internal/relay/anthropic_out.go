package relay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// 本文件把上游 CC 的 NDJSON 事件流编码成 Anthropic Messages SSE。
//
// 逐项对齐参考实现 createAnthropicSseTranslator（commandcode-proxy/proxy.mjs）：
// 块索引怎么递增、thinking 块的 signature 什么时候补、错误事件之后不补
// message_stop、用量怎么从 CC 的「总数」语义换成 Anthropic 的「非缓存」语义。
//
// 与参考实现唯一的接口差异：参考实现是个 async generator，把 reader 循环、看门狗
// 和事件解析都包在一起；这里只做「事件 → SSE 帧」的纯编码，读到什么事件由调用方决定。

// ── 对外数据结构 ────────────────────────────────────

// AnthropicUsage 是要回报给 Anthropic 客户端的用量块。
//
// 注意 InputTokens 只计**非缓存**部分：Anthropic 的语义是
// 总输入 = input_tokens + cache_creation_input_tokens + cache_read_input_tokens。
// CC 的 inputTokens 是总数，直接透出会让下游把两者相加、得到约两倍（参考实现 issue #25）。
type AnthropicUsage struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// AnthropicStreamError 是上游 error 事件记录下来的错误信息。
//
// 只做记录，不做「CC 状态码 → Anthropic 错误类型」的映射 —— 那是调用方（HTTP 层）
// 的职责，它才知道当前响应头有没有发出去、该回状态码还是回流内错误帧。
type AnthropicStreamError struct {
	Message     string
	Code        string
	StatusCode  int
	IsRetryable bool
}

// anthropicStreamState 是编码器的内部状态。
//
// 单独抽出来是为了让锁/状态的归属一眼可见（参考实现里这些全是闭包变量）。
type anthropicStreamState struct {
	// messageStartSent 标记 message_start 是否已发出。参考实现是在读上游之前
	// 无条件 yield 一次，这里改成惰性发送，保证任何路径下它都排在最前面。
	messageStartSent bool

	nextBlockIndex    int
	currentBlockIndex int
	currentBlockType  string
	blockStarted      bool

	// 累加出来的用量。注意 outputTokens 里既包含上游回报的值，也包含本地估算
	// （上游一个 usage 都没给时，按每个 text-delta +1、每个 tool-call +20 估算）。
	inputTokens       int
	outputTokens      int
	cachedInputTokens int
	cacheWriteTokens  int
	// noCacheTokens 为 -1 表示上游没提供该字段，message_delta 走减法兜底
	noCacheTokens int

	stopReason string
	// finishNorm 是过完 mapFinishReason 的 finishReason（即喂给
	// MapAnthropicStopReason 之前的值），用于判定「上游是否正常收尾」。
	finishNorm string
	sawFinish  bool
	hasError   bool

	// currentThinkingText 累积当前打开的 thinking 块的文本，关闭时用它算签名
	currentThinkingText string

	upstreamErr *AnthropicStreamError

	finished bool
}

// AnthropicStreamEncoder 把一个上游请求的 CC 事件流编码成 Anthropic Messages SSE。
//
// 用法：
//
//	enc := NewAnthropicStreamEncoder(model, messageID)
//	for each upstream event {
//	    for _, frame := range enc.Encode(ev) { writeSSE(frame) }
//	}
//	frames, truncated, usage := enc.Finish()
//
// 非并发安全：一条请求一个实例，按上游事件顺序串行调用。
type AnthropicStreamEncoder struct {
	model     string
	messageID string
	state     anthropicStreamState
}

// NewAnthropicStreamEncoder 为一次请求创建编码器。
//
// messageID 应当是调用方生成的 Anthropic 风格消息 id（如 msg_xxxxxxxxxxxx）；
// 参考实现在流开始时就把它写进 message_start。
func NewAnthropicStreamEncoder(model, messageID string) *AnthropicStreamEncoder {
	e := &AnthropicStreamEncoder{model: model, messageID: messageID}
	e.state.noCacheTokens = -1 // -1 == 上游未提供
	return e
}

// Encode 消费一个 CC 事件，返回要写给客户端的 SSE 帧（可能为 nil，表示这个事件没有可见输出）。
//
// 每个返回的字符串都是完整的一帧，形如 "event: <name>\ndata: <json>\n\n"，
// 直接按顺序写入响应体即可。第一个事件（哪怕它本身没有内容）会顺带带上
// message_start。
func (e *AnthropicStreamEncoder) Encode(ev *CCEvent) []string {
	if ev == nil || e.state.finished {
		return nil
	}
	s := &e.state

	out := make([]string, 0, 4)
	if frame := e.messageStartFrame(); frame != "" {
		out = append(out, frame)
	}

	switch ev.Type {
	case "start", "start-step", "text-start", "reasoning-start":
		// 信号事件，没有用户可见内容

	case "reasoning-delta":
		// CC 的 reasoning → Anthropic thinking 块（Claude Code 会当思考展示）
		text := ev.Text
		if text != "" {
			out = append(out, e.startThinkingBlock()...)
			s.currentThinkingText += text
			out = append(out, anthropicSSEFrame("content_block_delta", anthropicStreamBlockDelta{
				Type:  "content_block_delta",
				Index: s.currentBlockIndex,
				Delta: anthropicStreamThinkingDelta{Type: "thinking_delta", Thinking: text},
			}))
		}

	case "text-delta":
		// 这里只认 ev.Text，不认 ev.Delta —— 参考实现的 OpenAI 侧翻译器会退回
		// event.delta，Anthropic 侧不会，两边行为不同，别顺手「修好」它。
		// 另外文本为空也照样开块、照样计数（参考实现没有空值短路）。
		out = append(out, e.startTextBlock()...)
		out = append(out, anthropicSSEFrame("content_block_delta", anthropicStreamBlockDelta{
			Type:  "content_block_delta",
			Index: s.currentBlockIndex,
			Delta: anthropicStreamTextDelta{Type: "text_delta", Text: ev.Text},
		}))
		s.outputTokens++

	case "tool-call":
		// 先把打开着的块关掉（文本或 thinking 都会走同一套关闭逻辑）
		out = append(out, e.closeBlock()...)

		id := ev.ToolCallID
		if id == "" {
			id = "toolu_" + anthropicRandomHex(12)
		}
		name := ev.ToolName
		input := anthropicToolCallArgs(ev.Input)

		// 工具块不走 startBlock：它自成一块，并且不影响「当前打开的块」状态，
		// 之后的 text-delta 会另开一个新文本块（与参考实现一致）。
		tcIndex := s.nextBlockIndex
		s.nextBlockIndex++
		out = append(out, anthropicSSEFrame("content_block_start", anthropicStreamBlockStart{
			Type:  "content_block_start",
			Index: tcIndex,
			ContentBlock: anthropicStreamToolUseBlock{
				Type:  "tool_use",
				ID:    id,
				Name:  name,
				Input: map[string]any{},
			},
		}))
		out = append(out, anthropicSSEFrame("content_block_delta", anthropicStreamBlockDelta{
			Type:  "content_block_delta",
			Index: tcIndex,
			Delta: anthropicStreamInputJSONDelta{Type: "input_json_delta", PartialJSON: input},
		}))
		out = append(out, anthropicSSEFrame("content_block_stop", anthropicStreamBlockStop{
			Type:  "content_block_stop",
			Index: tcIndex,
		}))
		s.outputTokens += 20

	case "finish-step", "finish":
		// 上游的 finishReason 可能是连字符形式（'tool-calls'），必须先过
		// mapFinishReason 规范化，否则会掉进 MapAnthropicStopReason 的 default
		// 变成 end_turn —— 实测踩到过「工具调用成功但 stop_reason 报 end_turn」。
		s.sawFinish = true
		if ev.FinishReason != "" {
			s.finishNorm = mapFinishReason(ev.FinishReason)
			s.stopReason = MapAnthropicStopReason(s.finishNorm)
		}
		u := ev.TotalUsage
		if u == nil {
			u = ev.Usage
		}
		if u != nil {
			normalizeUsage(u) // 与参考实现一致：就地清零假计费，调用方之后看到的也是修正过的值
			// 参考实现用的是 `u.inputTokens ?? inputTokens`（字段缺失时保留旧值）。
			// CCUsage 的字段是值类型，解析之后就分不清「缺失」和 0，只能整体覆盖；
			// 这里按包内既有约定（见 types.go 的注释）走覆盖，只在 inputTokenDetails
			// 存在与否上保留指针语义。
			s.inputTokens = u.InputTokens
			s.outputTokens = u.OutputTokens
			s.cachedInputTokens = u.CachedInputTokens
			if u.InputTokenDetails != nil {
				s.cacheWriteTokens = u.InputTokenDetails.CacheWriteTokens
				s.noCacheTokens = u.InputTokenDetails.NoCacheTokens
			}
		}

	case "error":
		// 参考实现会在这里发一个 `event: error` 帧。错误信封的映射（CC 状态码 →
		// Anthropic 错误类型、retry_after）属于调用方职责，所以这里只记录：
		// 调用方看到 UpstreamError() != nil 就用 SendAnthropicError 或流内错误帧终止，
		// 绝不能当成正常完成。
		s.hasError = true
		e.recordError(ev)

	case "reasoning-end", "provider-metadata", "tool-input-start", "tool-input-delta",
		"tool-input-end", "tool-error", "text-end":
		// 静默，没有用户可见内容

	default:
		// 未知事件类型：参考实现只打一条 warn 日志，不产出任何内容
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// Finish 在上游流结束时收尾，返回最后的帧、上游是否异常收尾、以及累计用量。
//
// 返回值语义：
//   - frames 通常是收尾用的 content_block_stop / message_delta / message_stop；
//     唯一例外是「上游正常收尾但输出 token 为 0」—— 那一支按参考实现改发一个
//     rate_limit_error 错误帧（不再发 message_delta/message_stop），避免下游异常计费。
//   - truncated 表示上游这一轮没有正常收尾，对应参考实现 incompleteUpstreamDetail
//     的两条判据：整条流没出现过 finish/finish-step，或 finishReason 归一化后是
//     upstream_error（provider 报网络/连接失败）。两种情形参考实现都按可重试的
//     502 上游错误处理，错误信封由调用方生成。若上游给过 error 事件，调用方应优先
//     上报 UpstreamError()，再看 truncated。
//   - usage 是无论走哪个分支都会返回的用量快照，方便调用方记账。
//
// 重复调用只会返回空帧。
func (e *AnthropicStreamEncoder) Finish() (frames []string, truncated bool, usage AnthropicUsage) {
	s := &e.state
	if s.finished {
		return nil, false, e.usageSnapshot()
	}
	s.finished = true

	// 上游一个事件都没来就断了：message_start 得补上，否则客户端会收到空响应
	if frame := e.messageStartFrame(); frame != "" {
		frames = append(frames, frame)
	}
	usage = e.usageSnapshot()

	if s.hasError {
		// 与参考实现一致：出现 error 事件后不再收尾 —— 不补 content_block_stop，
		// 也不发 message_delta/message_stop（错误已经终止了这一轮）。
		return frames, !s.sawFinish, usage
	}

	frames = append(frames, e.closeBlock()...)

	// 上游没有正常走完 finish：绝不能补一个 end_turn 就 message_stop，
	// 那等于把截断谎报成完整回答。
	if !s.sawFinish || s.finishNorm == "upstream_error" {
		return frames, true, usage
	}

	if s.outputTokens == 0 {
		// 输出 token 为 0 记为错误（参考实现原样）：发 error 帧、不发 message_stop
		frames = append(frames, anthropicSSEFrame("error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "rate_limit_error",
				"message": "Empty response from upstream (zero output tokens)",
			},
			"retry_after": 10,
		}))
		return frames, false, usage
	}

	stopReason := s.stopReason
	if stopReason == "" {
		stopReason = "end_turn"
	}
	frames = append(frames, anthropicSSEFrame("message_delta", anthropicStreamMessageDelta{
		Type:  "message_delta",
		Delta: anthropicStreamMessageDeltaBody{StopReason: stopReason},
		Usage: anthropicStreamUsage{
			OutputTokens:             s.outputTokens,
			CacheReadInputTokens:     s.cachedInputTokens,
			CacheCreationInputTokens: s.cacheWriteTokens,
			InputTokens:              usage.InputTokens,
		},
	}))
	frames = append(frames, anthropicSSEFrame("message_stop", anthropicStreamMessageStop{Type: "message_stop"}))

	return frames, false, usage
}

// UpstreamError 返回流里出现过的 error 事件（没有则为 nil）。
//
// 记录下来的字段已经做过「event.error?.message || event.message || 'Unknown CC error'」
// 的兜底，调用方可以直接拿去组 Anthropic 错误信封。
func (e *AnthropicStreamEncoder) UpstreamError() *AnthropicStreamError {
	return e.state.upstreamErr
}

// Usage 返回当前累计的用量快照（Anthropic 语义）。流中途也可以调用。
func (e *AnthropicStreamEncoder) Usage() AnthropicUsage {
	return e.usageSnapshot()
}

// ── 内部：块管理 ────────────────────────────────────

// messageStartFrame 返回 message_start 帧；已经发过则返回空串。
func (e *AnthropicStreamEncoder) messageStartFrame() string {
	if e.state.messageStartSent {
		return ""
	}
	e.state.messageStartSent = true
	return anthropicSSEFrame("message_start", anthropicStreamMessageStart{
		Type: "message_start",
		Message: anthropicStreamMessageStartBody{
			ID:      e.messageID,
			Type:    "message",
			Role:    "assistant",
			Content: []any{}, // 必须是 []，nil slice 会被序列化成 null
			Model:   e.model,
			Usage:   anthropicStreamMessageStartUsage{InputTokens: 0, OutputTokens: 0},
		},
	})
}

// closeBlock 关闭当前打开的块（如果有），返回需要写的帧。
//
// thinking 块在关闭前要补一个 signature_delta（Anthropic 标准做法），
// 签名由整块文本派生。
func (e *AnthropicStreamEncoder) closeBlock() []string {
	s := &e.state
	if !s.blockStarted {
		return nil
	}
	idx := s.currentBlockIndex
	blockType := s.currentBlockType
	var out []string
	if blockType == "thinking" {
		out = append(out, anthropicSSEFrame("content_block_delta", anthropicStreamBlockDelta{
			Type:  "content_block_delta",
			Index: idx,
			Delta: anthropicStreamSignatureDelta{
				Type:      "signature_delta",
				Signature: FakeThinkingSignature(s.currentThinkingText),
			},
		}))
		s.currentThinkingText = ""
	}
	s.blockStarted = false
	s.currentBlockType = ""
	out = append(out, anthropicSSEFrame("content_block_stop", anthropicStreamBlockStop{
		Type:  "content_block_stop",
		Index: idx,
	}))
	return out
}

// startBlock 打开一个指定类型的块：类型不同（或当前没有块）时先关掉旧块再开新块，
// 类型相同时什么都不做（同类型的连续 delta 复用同一个块）。
func (e *AnthropicStreamEncoder) startBlock(blockType string, contentBlock any) []string {
	s := &e.state
	if s.blockStarted && s.currentBlockType == blockType {
		return nil
	}
	out := e.closeBlock()
	s.currentBlockIndex = s.nextBlockIndex
	s.nextBlockIndex++
	s.currentBlockType = blockType
	s.blockStarted = true
	return append(out, anthropicSSEFrame("content_block_start", anthropicStreamBlockStart{
		Type:         "content_block_start",
		Index:        s.currentBlockIndex,
		ContentBlock: contentBlock,
	}))
}

// startTextBlock 开一个空文本块。注意 text 字段必须存在（哪怕是空串），
// 所以这里的块结构体不能给 text 加 omitempty。
func (e *AnthropicStreamEncoder) startTextBlock() []string {
	return e.startBlock("text", anthropicStreamTextBlock{Type: "text", Text: ""})
}

// startThinkingBlock 开一个空 thinking 块。
//
// 参考实现在 content_block_start 里不带 signature（Claude 官方流式响应也是
// 在结束时用 signature_delta 补），签名统一在 closeBlock 里发。
func (e *AnthropicStreamEncoder) startThinkingBlock() []string {
	return e.startBlock("thinking", anthropicStreamThinkingBlock{Type: "thinking", Thinking: ""})
}

// ── 内部：用量与错误 ────────────────────────────────

// usageSnapshot 把累计值换算成 Anthropic 语义的用量。
func (e *AnthropicStreamEncoder) usageSnapshot() AnthropicUsage {
	s := &e.state
	// Anthropic 的 input_tokens 只计非缓存部分。上游给了 noCacheTokens 就直接用；
	// 否则走减法兜底：参考实现是 max(0, input - cacheRead - cacheWrite)，而包里的
	// anthropicInputTokens 在缺少 inputTokenDetails 时只减 CachedInputTokens，
	// 所以把 cacheWrite 折进 CachedInputTokens 才能得到同一个值。
	u := &CCUsage{InputTokens: s.inputTokens, CachedInputTokens: s.cachedInputTokens}
	var override *int
	if s.noCacheTokens >= 0 {
		nc := s.noCacheTokens
		override = &nc
	} else {
		u.CachedInputTokens += s.cacheWriteTokens
	}
	return AnthropicUsage{
		InputTokens:              anthropicInputTokens(u, override),
		OutputTokens:             s.outputTokens,
		CacheCreationInputTokens: s.cacheWriteTokens,
		CacheReadInputTokens:     s.cachedInputTokens,
	}
}

// recordError 把 error 事件里的错误信息记下来，字段兜底顺序与参考实现的
// mapCcEventError 一致。
func (e *AnthropicStreamEncoder) recordError(ev *CCEvent) {
	info := &AnthropicStreamError{}
	if ev.Error != nil {
		info.Message = ev.Error.Message
		info.Code = ev.Error.Code
		info.StatusCode = ev.Error.StatusCode
		info.IsRetryable = ev.Error.IsRetryable
	}
	if info.Message == "" {
		info.Message = ev.Message
	}
	if info.Message == "" {
		info.Message = "Unknown CC error"
	}
	if info.Code == "" {
		info.Code = ev.Code
	}
	e.state.upstreamErr = info
}

// ── 内部：序列化辅助 ────────────────────────────────

// anthropicSSEFrame 拼一帧 Anthropic 命名的 SSE。
func anthropicSSEFrame(event string, payload any) string {
	return "event: " + event + "\ndata: " + anthropicJSONString(payload) + "\n\n"
}

// anthropicJSONString 序列化 SSE 的 data 部分。
//
// 关掉 HTML 转义：默认的 json.Marshal 会把 < > & 转成 < 之类，
// 虽然合法性没问题，但排障时很难读——文本内容里出现尖括号是常事。
func anthropicJSONString(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// 这里的 payload 全是本文件构造的可序列化值，出错只可能是编程错误
		return "{}"
	}
	// Encoder.Encode 会补一个换行，帧里不需要它
	return strings.TrimSuffix(buf.String(), "\n")
}

// anthropicToolCallArgs 复刻 `typeof event.input === 'string' ? event.input :
// JSON.stringify(event.input || {})`。
//
// 上游既可能给对象也可能给「已经序列化好的字符串」，字符串要原样透出（不能再包一层引号）。
func anthropicToolCallArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// 上游给的不是合法 JSON：当作空参数，别让坏数据把整条流带崩
		return "{}"
	}
	if s, ok := v.(string); ok {
		return s
	}
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
	}
	return anthropicJSONString(v)
}

// anthropicRandomHex 生成 n 个十六进制字符的随机串。
//
// 对应参考实现里 randomUUID().slice(0, 12)：只在「上游没给 toolCallId」时兜底。
func anthropicRandomHex(n int) string {
	const alphabet = "0123456789abcdef"
	out := make([]byte, n)
	if _, err := rand.Read(out); err != nil {
		// crypto/rand 基本不会失败；失败时退回确定值，绝不让 id 生成把请求打挂
		for i := range out {
			out[i] = '0'
		}
		return string(out)
	}
	for i := range out {
		out[i] = alphabet[out[i]&0x0f]
	}
	return string(out)
}

// ── 导出给非流式路径复用的两个参考实现函数 ──────────────

// FakeThinkingSignature 生成 Claude 格式的假 thinking 签名。
//
// Anthropic 会密码学校验 thinking 签名，第三方代理签不出合法的；但 Claude Code
// 的浅层校验只要求 base64 以 'E'（单层）/'R'（双层）开头、载荷首字节为 0x12，
// 这里满足该条件，于是 CC 能正常展示 thinking。载荷由 thinking 文本派生，
// 让每个块的签名各不相同（更贴近规范，也避免同签名带来的怪问题）。
func FakeThinkingSignature(thinkingText string) string {
	seed := thinkingText
	if seed == "" {
		seed = "dsh-proxy-thinking"
	}
	sum := sha256.Sum256([]byte(seed))
	// 参考实现取 digest 的前 64 字节，但 sha256 只有 32 字节，subarray 实际就是整个摘要
	raw := make([]byte, 0, len(sum)+2)
	raw = append(raw, 0x12, byte(len(sum)))
	raw = append(raw, sum[:]...)
	return base64.StdEncoding.EncodeToString(raw)
}

// MapAnthropicStopReason 把归一化后的 finishReason（mapFinishReason 的输出）
// 映射成 Anthropic 的 stop_reason。
func MapAnthropicStopReason(finishReason string) string {
	switch finishReason {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "stop":
		return "end_turn"
	// Anthropic 的原生枚举要原样透出：pause_turn 表示「这一轮被暂停，后面还有内容」，
	// 折成 end_turn 会让下游把半截回答当成写完了（CLI 靠自动续写吸收，代理不自动续写，
	// 就必须如实上报）。
	case "pause_turn":
		return "pause_turn"
	case "refusal":
		return "refusal"
	default:
		// 参考实现的 default 是 end_turn，不是透传：stop_reason 是封闭枚举，
		// 把 upstream_error 这类内部值透出去会得到下游无法识别的 stop_reason。
		return "end_turn"
	}
}

// SendAnthropicError 按 Anthropic 的错误信封回一个 JSON 错误响应。
//
// retryAfter > 0 时同时写入 body 的 retry_after 和 Retry-After 响应头
// （Go 没有 undefined，用 0 表达「不带退避提示」）。
func SendAnthropicError(w http.ResponseWriter, status int, errType, message string, retryAfter int) {
	body := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	}
	if retryAfter > 0 {
		body["retry_after"] = retryAfter
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload, err := json.Marshal(body); err == nil {
		_, _ = w.Write(payload)
	}
}

// ── SSE 帧的形状 ────────────────────────────────────
//
// 这些结构体只为固定字段顺序（方便和参考实现对照），字段名一律下划线风格。
// 需要发空串的字段（text / thinking / empty content）绝对不能加 omitempty，
// 否则客户端会少拿到必需的字段。

type anthropicStreamMessageStart struct {
	Type    string                          `json:"type"`
	Message anthropicStreamMessageStartBody `json:"message"`
}

type anthropicStreamMessageStartBody struct {
	ID      string                           `json:"id"`
	Type    string                           `json:"type"`
	Role    string                           `json:"role"`
	Content []any                            `json:"content"`
	Model   string                           `json:"model"`
	Usage   anthropicStreamMessageStartUsage `json:"usage"`
}

type anthropicStreamMessageStartUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicStreamBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

type anthropicStreamBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

type anthropicStreamBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type anthropicStreamMessageDelta struct {
	Type  string                          `json:"type"`
	Delta anthropicStreamMessageDeltaBody `json:"delta"`
	Usage anthropicStreamUsage            `json:"usage"`
}

type anthropicStreamMessageDeltaBody struct {
	StopReason string `json:"stop_reason"`
}

type anthropicStreamMessageStop struct {
	Type string `json:"type"`
}

// anthropicStreamUsage 是 message_delta 里的用量块。
//
// input_tokens 只计非缓存部分；顺序与参考实现一致，便于逐字节对照。
type anthropicStreamUsage struct {
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	InputTokens              int `json:"input_tokens"`
}

type anthropicStreamTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicStreamThinkingBlock struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type anthropicStreamToolUseBlock struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type anthropicStreamTextDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicStreamThinkingDelta struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type anthropicStreamSignatureDelta struct {
	Type      string `json:"type"`
	Signature string `json:"signature"`
}

type anthropicStreamInputJSONDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}
