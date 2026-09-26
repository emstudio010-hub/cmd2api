package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// MappedError 是映射后的上游错误，同时供两种协议的响应格式使用。
type MappedError struct {
	// Status 是发给客户端的 HTTP 状态码。
	Status int
	// Type 是错误类别（invalid_request_error 等）。
	Type    string
	Message string
	// Code 是上游的机器可读分类（BAD_REQUEST / USAGE_EXCEEDED 等）。
	Code string
	// ReportedStatus 是上游自称的状态码，用于日志与排障。
	ReportedStatus int
	// RetryAfter 非 0 时作为 retry-after 提示下发。
	RetryAfter int
}

// statusMapping 是上游状态码到客户端状态码的映射。
type statusMapping struct {
	Status int
	Type   string
}

var ccStatusMap = map[int]statusMapping{
	400: {http.StatusBadRequest, "invalid_request_error"},
	401: {http.StatusUnauthorized, "authentication_error"},
	// 402（欠费）映射成 429：对客户端来说「额度用尽」和「被限流」的
	// 应对方式一样——退避而不是重试同样的请求。
	402: {http.StatusTooManyRequests, "rate_limit_error"},
	403: {http.StatusUnauthorized, "authentication_error"},
	404: {http.StatusNotFound, "not_found"},
	422: {http.StatusBadRequest, "invalid_request_error"},
	429: {http.StatusTooManyRequests, "rate_limit_error"},
	500: {http.StatusBadGateway, "upstream_error"},
	502: {http.StatusBadGateway, "upstream_error"},
	503: {http.StatusServiceUnavailable, "temporarily_unavailable"},
}

const defaultRetryAfter = 30

// MapHTTPError 映射一次非 2xx 的上游 HTTP 响应。
func MapHTTPError(status int, body []byte) *MappedError {
	mapped, ok := ccStatusMap[status]
	if !ok {
		mapped = statusMapping{http.StatusBadGateway, "upstream_error"}
	}

	message := fmt.Sprintf("Command Code API 返回错误 (%d)", status)
	code := ""

	var parsed struct {
		Success bool `json:"success"`
		Error   struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Error.Message != "" {
			message = parsed.Error.Message
		} else if parsed.Message != "" {
			message = parsed.Message
		}
		if parsed.Error.Code != "" {
			code = parsed.Error.Code
		} else {
			code = parsed.Code
		}
	} else if len(body) > 0 {
		// 不是 JSON，就把原文截一段当消息，便于排障。
		truncated := body
		if len(truncated) > 200 {
			truncated = truncated[:200]
		}
		message = string(truncated)
	}

	out := &MappedError{
		Status:         mapped.Status,
		Type:           mapped.Type,
		Message:        message,
		Code:           code,
		ReportedStatus: status,
	}
	// 状态码层面的兜底先铺上，再由 code 覆盖：映射成限流的错误一律要给
	// 退避提示，否则客户端不知道该等，只会立刻重试。
	if out.Status == http.StatusTooManyRequests {
		out.RetryAfter = defaultRetryAfter
	}
	refineByCode(out, code, message)
	return out
}

// refineByCode 用上游的机器可读 error code 修正映射结果。
//
// 必要性来自实测：模型不在套餐内时，上游返回的是 HTTP 403 / code=FORBIDDEN，
// 消息为 "MODEL_NOT_IN_PLAN: Claude Sonnet 4.6 available in Pro and above plans"。
// 如果只按状态码映射，403 会被折成 authentication_error——客户端看到
// 「认证失败」只会以为是密钥写错了，而真实原因是「这个模型你的套餐用不了」，
// 两件事该做的处理完全不同。
//
// 注意这里只修**对外呈现**，不改变 Retryable() 的判定：换一个账号重试
// 仍然是有意义的，因为别的账号可能套餐更高、有这个模型。真正该停止重试的
// 是「请求本身格式不对」那类错误。
//
// 有 code 时以 code 为准，同时对消息前缀做兜底匹配——上游并非每个错误都带 code。
func refineByCode(out *MappedError, code, message string) {
	upperCode := strings.ToUpper(code)
	upperMsg := strings.ToUpper(message)

	switch {
	case upperCode == "MODEL_NOT_IN_PLAN" || strings.HasPrefix(upperMsg, "MODEL_NOT_IN_PLAN"):
		out.Status = http.StatusBadRequest
		out.Type = "invalid_request_error"
		out.RetryAfter = 0
		// 这条消息本身是可操作的（写明了哪些套餐可用），原样透出。
	case upperCode == "USAGE_EXCEEDED" || upperCode == "QUOTA_EXCEEDED":
		// 额度用尽：退避没意义，等多久都一样，直到额度恢复。
		out.Status = http.StatusTooManyRequests
		out.Type = "rate_limit_error"
		out.RetryAfter = defaultRetryAfter
	case upperCode == "RATE_LIMITED" || upperCode == "RATE_LIMIT_EXCEEDED":
		out.Status = http.StatusTooManyRequests
		out.Type = "rate_limit_error"
		out.RetryAfter = defaultRetryAfter
	case upperCode == "BAD_REQUEST" || upperCode == "INVALID_REQUEST":
		out.Status = http.StatusBadRequest
		out.Type = "invalid_request_error"
		out.RetryAfter = 0
	}
}

// statusPrefix 匹配上游把状态码塞在错误消息前缀里的写法，例如 "<429> ..."。
var statusPrefix = regexp.MustCompile(`^<(\d{3})>`)

// MapEventError 映射流内 error 事件。
//
// 上游的 error 事件除了 message 还可能自带 statusCode——这正是客户端判断
// 「该退避重试还是该报错」的依据。只看 message 会把这些信号抹平成 502，
// 于是客户端不再按限流退避，监控也会把它错误归类成后端故障。
func MapEventError(ev *CCEvent) *MappedError {
	message := "Command Code 上游返回未知错误"
	code := ""
	reported := 0

	if ev != nil {
		if ev.Error != nil {
			if ev.Error.Message != "" {
				message = ev.Error.Message
			}
			code = ev.Error.Code
			if ev.Error.StatusCode != 0 {
				reported = ev.Error.StatusCode
			}
		}
		if message == "Command Code 上游返回未知错误" && ev.Message != "" {
			message = ev.Message
		}
		if code == "" {
			code = ev.Code
		}
	}

	// message 里的 "<NNN>" 前缀优先于 statusCode 字段。
	if m := statusPrefix.FindStringSubmatch(message); len(m) == 2 {
		var n int
		if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil {
			reported = n
		}
	}

	if reported == 0 {
		reported = http.StatusBadGateway
	}
	mapped, ok := ccStatusMap[reported]
	if !ok {
		mapped = statusMapping{http.StatusBadGateway, "upstream_error"}
	}

	out := &MappedError{
		Status:         mapped.Status,
		Type:           mapped.Type,
		Message:        message,
		Code:           code,
		ReportedStatus: reported,
	}
	if out.Status == http.StatusTooManyRequests {
		out.RetryAfter = defaultRetryAfter
	}
	refineByCode(out, code, message)
	return out
}

// mapAnthropicStreamError 把 Anthropic 侧记录的上游错误接进统一的映射逻辑。
//
// 复用 MapEventError 而不是另写一套：状态码前缀解析、402→429 这类映射规则
// 只该有一份，两边各写一份迟早会漂移。
func mapAnthropicStreamError(e *AnthropicStreamError) *MappedError {
	if e == nil {
		return nil
	}
	return MapEventError(&CCEvent{
		Type:    "error",
		Message: e.Message,
		Code:    e.Code,
		Error: &CCError{
			Message:     e.Message,
			Code:        e.Code,
			StatusCode:  e.StatusCode,
			IsRetryable: e.IsRetryable,
		},
	})
}

// Retryable 表示这个错误是否值得换一个账号重试。
//
// 认证类错误换账号有意义（可能只是这个号被停了）；请求格式类错误换账号
// 没意义，同一个请求发给谁都会被拒。
func (e *MappedError) Retryable() bool {
	switch e.ReportedStatus {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired,
		http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	}
	return e.Status == http.StatusBadGateway || e.Status == http.StatusServiceUnavailable
}

// IsRateLimit 表示这是限流类错误，需要给账号打上限流标记。
func (e *MappedError) IsRateLimit() bool {
	return e.Status == http.StatusTooManyRequests
}

// IsAuthFailure 表示凭证本身有问题，该账号应被标记为异常。
func (e *MappedError) IsAuthFailure() bool {
	return e.ReportedStatus == http.StatusUnauthorized || e.ReportedStatus == http.StatusForbidden
}
