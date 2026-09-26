package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenCode 上游是 OpenAI 兼容协议：请求基本直通，不需要信封、指纹或设备伪装。
// 与 Command Code 相比，这一路的实现要简单得多——真正的工作量在下游翻译，
// 也就是 openAIStreamAdapter 那一层。

// openCodeGenerate 向 OpenCode 上游发起一次 chat completion 请求。
//
// stream 决定要不要在请求体里打上 stream:true——上游据此选择返回 SSE 还是
// 一次性 JSON，而我们内部统一按流处理，所以这里恒为 true。
func (c *Client) openCodeGenerate(ctx context.Context, baseURL, apiKey string, body []byte) (*http.Response, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	// OpenAI 兼容服务多数不强校验 UA，但给一个明确的值便于上游侧排查。
	req.Header.Set("User-Agent", "cmd2api")

	return c.http.Do(req)
}

// openCodeProbe 探活一个 OpenCode 账号。
//
// 和 Command Code 的探活思路一致：发一次极小的真实生成请求，而不是只验证
// 密钥合法性——后者发现不了额度耗尽、账号被停用这类问题。
func (c *Client) openCodeProbe(ctx context.Context, baseURL, apiKey string) ProbeResult {
	started := time.Now()

	// 一律用非流式探活：省掉解析流的开销，而且出错时错误体是完整的 JSON。
	payload := map[string]any{
		"model":      probeModel,
		"messages":   []map[string]any{{"role": "user", "content": probePrompt}},
		"max_tokens": 1,
		"stream":     false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusInternalServerError, Type: "api_error",
			Message: "构造探活请求失败: " + err.Error(),
		}}
	}

	resp, err := c.openCodeGenerate(ctx, baseURL, apiKey, body)
	if err != nil {
		if ctx.Err() != nil {
			return ProbeResult{Latency: time.Since(started), Err: &MappedError{
				Status: http.StatusGatewayTimeout, Type: "upstream_error",
				Message: "探活超时",
			}}
		}
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "无法连接 OpenCode 上游: " + err.Error(),
		}}
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if resp.StatusCode != http.StatusOK {
		return ProbeResult{Latency: time.Since(started), Err: mapOpenCodeError(resp.StatusCode, raw)}
	}

	// 状态 200 也要确认真的返回了内容：有的兼容层会在额度耗尽时
	// 返回一个空的成功响应。
	var parsed struct {
		Choices []json.RawMessage `json:"choices"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "上游返回的不是合法 JSON",
		}}
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: parsed.Error.Message,
		}}
	}
	if len(parsed.Choices) == 0 {
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "上游返回成功状态但没有 choices",
		}}
	}

	return ProbeResult{Latency: time.Since(started)}
}

// openCodeModels 拉取 OpenCode 的模型列表。
//
// 与 Command Code 不同，这里不缓存：OpenCode 的模型列表跟着账号的订阅档位走，
// 不同账号看到的可能不一样，缓存一份全局列表反而是错的。
func (c *Client) openCodeModels(ctx context.Context, baseURL, apiKey string) []Model {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		c.logger.Warn("构造 OpenCode 模型列表请求失败", "err", err)
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "cmd2api")

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Warn("拉取 OpenCode 模型列表失败", "err", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("拉取 OpenCode 模型列表返回非 200", "status", resp.StatusCode)
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		c.logger.Warn("读取 OpenCode 模型列表失败", "err", err)
		return nil
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		c.logger.Warn("OpenCode 模型列表格式不符合预期", "err", err)
		return nil
	}

	out := make([]Model, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, Model{ID: m.ID, Name: m.ID})
		}
	}
	return out
}

// mapOpenCodeError 把 OpenAI 兼容上游的错误映射成统一结构。
//
// 复用 Command Code 那套状态码映射表：OpenAI 系的语义（401 认证、429 限流、
// 5xx 上游故障）与 CC 是一致的，没必要维护第二份规则。
func mapOpenCodeError(status int, body []byte) *MappedError {
	mapped := MapHTTPError(status, body)
	mapped.Message = strings.TrimSpace(mapped.Message)
	if mapped.Message == "" {
		mapped.Message = "OpenCode 上游返回错误"
	}
	return mapped
}

// readOpenAISSE 逐行读 OpenAI SSE 流，把每个 data 载荷交给 handle。
//
// 看门狗的做法与 CC 那条流一致：定时关闭 body 打断阻塞中的 Read。
func (c *Client) readOpenAISSE(ctx context.Context, body io.ReadCloser, idle time.Duration, handle func(payload string)) error {
	if idle <= 0 {
		idle = 30 * time.Second
	}
	watchdog := time.AfterFunc(idle, func() { _ = body.Close() })
	defer watchdog.Stop()

	scanner := bufio.NewScanner(body)
	// SSE 单行可能很长（工具调用参数分片、长文本），默认 64KB 不够。
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		watchdog.Reset(idle)
		payload, ok := SplitSSELine(scanner.Text())
		if !ok {
			continue
		}
		handle(payload)
	}
	return scanner.Err()
}
