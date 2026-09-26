package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// probeModelPreference 是挑探活模型时的偏好关键词，越靠前越优先。
//
// 探活每轮每个账号都要跑一次，挑贵的模型纯属烧钱。命不中就退回列表第一个。
var probeModelPreference = []string{"flash", "mini", "lite", "haiku", "small", "m2.5", "m2.7"}

// pickProbeModel 从该部署实际支持的模型列表里挑一个探活用模型。
//
// 绝不能写死模型名。踩过的坑：照搬 Command Code 的 deepseek/deepseek-v4-flash
// 去探 OpenCode，上游直接回 "Model deepseek/deepseek-v4-flash is not supported"，
// 于是**每个 OpenCode 账号**都会被判成不可用，连续失败几次后被自动禁用。
// 而 Go 档（minimax-m3 / kimi-k3 / glm-5.2…）和 Zen 档（claude-* / gpt-*）
// 的模型集合本来就完全不同，还会随上游调整——写死必然过期。
// apiKey 必须带上：探活要问的是「**这个账号**能用哪些模型」。
// 真实 OpenCode 的 /models 恰好是公开端点，不带密钥也能拿到列表，
// 所以这个疏漏一开始没暴露；但换个要求鉴权的上游就会直接 401，
// 探活会以「拿不到模型列表」失败，看起来像网络问题，实则是自己没带凭证。
func (c *Client) pickProbeModel(ctx context.Context, baseURL, apiKey string) string {
	models := c.openCodeModels(ctx, baseURL, apiKey)
	if len(models) == 0 {
		return ""
	}
	for _, want := range probeModelPreference {
		for _, m := range models {
			if strings.Contains(strings.ToLower(m.ID), want) {
				return m.ID
			}
		}
	}
	return models[0].ID
}

// openCodeProbe 探活一个 OpenCode 账号。
//
// 和 Command Code 的探活思路一致：发一次极小的真实生成请求，而不是只验证
// 密钥合法性——后者发现不了额度耗尽、账号被停用这类问题。
func (c *Client) openCodeProbe(ctx context.Context, baseURL, apiKey string) ProbeResult {
	started := time.Now()

	// /models 是公开端点（不需要鉴权），所以它拿不到只说明网络有问题，
	// 而不是账号有问题——这种情况下探活无法得出结论。
	model := c.pickProbeModel(ctx, baseURL, apiKey)
	if model == "" {
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "拿不到 OpenCode 的模型列表，无法完成探活（检查网络或代理配置）",
		}}
	}

	// 一律用非流式探活：省掉解析流的开销，而且出错时错误体是完整的 JSON。
	payload := map[string]any{
		"model":      model,
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
		mapped := mapOpenCodeError(resp.StatusCode, raw)
		// 把探活用的模型名带上：模型不可用与密钥无效的上游措辞可能很像，
		// 不写清楚的话排障时无从判断到底哪一边有问题。
		mapped.Message = fmt.Sprintf("%s（探活模型：%s）", mapped.Message, model)
		return ProbeResult{Latency: time.Since(started), Err: mapped}
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
	// /models 是公开端点，没有密钥时不要发一个空的 Bearer 头——
	// 某些网关会把格式不合法的 Authorization 直接判为 401。
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
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
