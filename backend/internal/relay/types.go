// Package relay 实现 Command Code 上游协议的客户端与协议翻译。
//
// 整体流水线（与 commandcode-proxy 同构）：
//
//	入站请求（OpenAI / Anthropic）
//	  → 归一成 OpenAI Chat Completions 形状
//	  → buildEnvelope 包成 CC 信封
//	  → POST /alpha/generate（NDJSON 流）
//	  → 按入站协议翻译回 SSE
//
// 之所以中间层选 OpenAI 形状而不是 Anthropic 形状：CC 信封里的 messages
// 本身就更接近 OpenAI 的 role 语义，且 OpenAI 形状表达多模态/工具调用更直接。
package relay

import (
	"encoding/json"
	"regexp"
	"strings"
)

// CCEvent 是上游 NDJSON 流里的一行。注意上游发的不是 SSE，是每行一个 JSON。
type CCEvent struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	Delta        string          `json:"delta"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	Input        json.RawMessage `json:"input"`
	FinishReason string          `json:"finishReason"`
	Usage        *CCUsage        `json:"usage"`
	TotalUsage   *CCUsage        `json:"totalUsage"`
	Error        *CCError        `json:"error"`
	Message      string          `json:"message"`
	Code         string          `json:"code"`
}

// CCError 是上游 error 事件里的错误对象。
//
// StatusCode 和 IsRetryable 是客户端做退避决策的依据，不能丢——
// 丢了就只能一律当成 502，客户端不会按限流退避。
type CCError struct {
	Message     string `json:"message"`
	Code        string `json:"code"`
	StatusCode  int    `json:"statusCode"`
	IsRetryable bool   `json:"isRetryable"`
}

// CCUsage 是上游返回的用量统计。
//
// 注意 InputTokens 是**总数**（含缓存命中），这与 Anthropic 的 input_tokens
// 语义不同，翻译时必须转换，否则下游会把两者当成互补的两部分、相加后翻倍。
type CCUsage struct {
	InputTokens       int               `json:"inputTokens"`
	OutputTokens      int               `json:"outputTokens"`
	CachedInputTokens int               `json:"cachedInputTokens"`
	InputTokenDetails *InputTokenDetail `json:"inputTokenDetails"`
}

// InputTokenDetail 把输入拆成未命中缓存/读缓存/写缓存三部分。
//
// 实测 noCacheTokens + cacheReadTokens == inputTokens，所以优先用 noCacheTokens
// 作为 Anthropic 的 input_tokens，比做减法更可靠。
//
// 字段用值类型而非指针：靠 InputTokenDetails 本身是否为 nil 判断字段是否存在。
// 代价是「结构体存在但 noCacheTokens 被上游省略」这种畸形响应会被当成 0，
// 实际观测中没出现过这种形状。
type InputTokenDetail struct {
	NoCacheTokens    int `json:"noCacheTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

// unresolvedToolName 用于 placeholder。
var finishReasonUpstreamErr = regexp.MustCompile(`^(?:network|connection|upstream)[-_\s]?error$`)

// mapFinishReason 把上游 finishReason 归一化。
//
// 'length' 家族不止 'length' 一个值：max_output_tokens 和
// model_context_window_exceeded 同样是「输出被截断」。把它们折成 stop/end_turn
// 等于把半截回答谎报成完整回答，所以一律映射为 length。
//
// 未知值原样返回（小写），宁可让它露出来，也不要静默变成 stop。
func mapFinishReason(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	if r == "" {
		return "stop"
	}
	switch r {
	case "tool-calls", "tool_calls", "tool_use":
		return "tool_calls"
	case "length", "max_tokens", "max_output_tokens", "model_context_window_exceeded":
		return "length"
	}
	if finishReasonUpstreamErr.MatchString(r) {
		return "upstream_error"
	}
	return r
}

// normalizeUsage 修正上游用量统计里的假计费。
//
// outputTokens 为 0 时把 input/cached 一并清零：上游偶尔会在请求失败时
// 返回「有输入 token、没有输出 token」的残缺统计，照单全收会记出一笔
// 根本没发生的输入费用。
func normalizeUsage(u *CCUsage) {
	if u == nil {
		return
	}
	if u.OutputTokens == 0 {
		u.InputTokens = 0
		u.CachedInputTokens = 0
	}
}

// anthropicInputTokens 把 CC 的 inputTokens（总数）换成 Anthropic 的 input_tokens
// （仅未命中缓存部分）。
//
// noCacheOverride 不为 nil 且非负时优先采用——留这个参数是为了调用方能
// 从别处（例如累计统计）拿到更准的值。
func anthropicInputTokens(u *CCUsage, noCacheOverride *int) int {
	if noCacheOverride != nil && *noCacheOverride >= 0 {
		return *noCacheOverride
	}
	if u == nil {
		return 0
	}
	if u.InputTokenDetails != nil {
		return u.InputTokenDetails.NoCacheTokens
	}
	// 到这里说明上游没带 inputTokenDetails（老版本），退回减法：
	// 只能拿 CachedInputTokens 当读缓存，写缓存无法得知，按 0 处理。
	diff := u.InputTokens - u.CachedInputTokens
	if diff < 0 {
		return 0
	}
	return diff
}

// cacheTokens 取出缓存写入/读取量，供 Anthropic 用量块使用。
func cacheTokens(u *CCUsage) (creation, read int) {
	if u == nil || u.InputTokenDetails == nil {
		return 0, 0
	}
	return u.InputTokenDetails.CacheWriteTokens, u.InputTokenDetails.CacheReadTokens
}
