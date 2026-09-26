package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cmd2api/internal/domain"
	"cmd2api/internal/scheduler"
)

// commandCodeProbeModel 是探活 Command Code 账号用的模型。
//
// 只在 Command Code 这一路写死：它的模型命名（vendor/model）稳定，
// 且这个模型便宜快。OpenCode 那边绝对不能照搬——见 openCodeProbeModel 的说明。
const commandCodeProbeModel = "deepseek/deepseek-v4-flash"

// probePrompt 尽量短，把探活的 token 消耗压到最低。
const probePrompt = "ok"

// probeReadLimit 是探活时最多读多少字节的上游响应。
//
// 给得比较宽松：上游会在 start-step 事件里**回显整个请求体**，
// 带上它自己注入的默认提示词后，这一行可能有几十到上百 KB。
// 上限太小会把预算全耗在这一行上，后面的内容事件就再也读不到了。
const probeReadLimit = 4 * 1024 * 1024

// probeLineLimit 是单行的最大长度，必须大于上面那个整体上限，
// 否则超长行会让 bufio.Scanner 直接返回 ErrTooLong 并停止扫描。
const probeLineLimit = 8 * 1024 * 1024

// ProbeResult 是一次探活的结果。
type ProbeResult struct {
	Latency time.Duration
	// Err 为 nil 表示账号可用。
	Err *MappedError
}

// Probe 用一次极小的生成请求验证账号是否真的能用。
//
// 为什么不用「拉模型列表」当探活：那个端点只验证密钥本身的合法性，
// 账号被停用、额度耗尽、被限流这些问题它都发现不了。要判断「能不能干活」，
// 只能真的发一次生成请求。代价是每个账号每轮消耗几十个 token。
//
// 两种平台各有一套探活实现——它们的上游协议不同，没办法共用一条链路。
func (c *Client) Probe(ctx context.Context, target *scheduler.Target) ProbeResult {
	if target == nil {
		return ProbeResult{Err: &MappedError{
			Status: http.StatusInternalServerError, Type: "api_error",
			Message: "探活目标为空",
		}}
	}
	if target.Platform == domain.PlatformOpenCode {
		baseURL := target.BaseURL
		if baseURL == "" {
			baseURL = domain.DefaultOpenCodeBaseURL(target.AccountMode)
		}
		return c.openCodeProbe(ctx, baseURL, target.APIKey)
	}
	return c.probeCommandCode(ctx, target.APIKey)
}

// probeCommandCode 是 Command Code 的探活实现。
func (c *Client) probeCommandCode(ctx context.Context, apiKey string) ProbeResult {
	started := time.Now()

	one := 1
	req := &normalizedRequest{
		Model:     commandCodeProbeModel,
		Messages:  []map[string]any{{"role": "user", "content": probePrompt}},
		MaxTokens: &one,
	}
	// 探活同样要带 system 占位。不占位的话上游会注入它自带的约 7.5K token
	// 默认提示词——每次探活都白烧一遍 token，代价比生成请求还明显。
	envelope, err := buildEnvelope(req, "agent", c.device, "", true)
	if err != nil {
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusInternalServerError, Type: "api_error",
			Message: "构造探活请求失败: " + err.Error(),
		}}
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusInternalServerError, Type: "api_error",
			Message: "序列化探活请求失败: " + err.Error(),
		}}
	}

	resp, err := c.Generate(ctx, apiKey, nil, payload)
	if err != nil {
		if ctx.Err() != nil {
			return ProbeResult{Latency: time.Since(started), Err: &MappedError{
				Status: http.StatusGatewayTimeout, Type: "upstream_error",
				Message: "探活超时",
			}}
		}
		return ProbeResult{Latency: time.Since(started), Err: &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "无法连接上游: " + err.Error(),
		}}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return ProbeResult{Latency: time.Since(started), Err: MapHTTPError(resp.StatusCode, body)}
	}

	// 状态码 200 还不够：上游可能在流里报错。扫一小段确认没有 error 事件。
	if err := scanForStreamError(resp.Body); err != nil {
		return ProbeResult{Latency: time.Since(started), Err: err}
	}

	// 读到内容就算通过。探活不关心回答质量，只关心账号能不能干活。
	return ProbeResult{Latency: time.Since(started)}
}

// scanForStreamError 检查上游流里有没有 error 事件，以及是否真的产出了内容。
//
// 判定原则：**宁可放过，不可误杀**。把可用账号判成不可用会导致健康检查
// 逐个自动禁用账号，代价远大于漏判一个坏账号——后者在真正的中转路径上
// 会被 MarkFailure 抓到。所以只有「明确看到错误事件」才判失败；
// 「读到上限还没看到完成信号」属于不确定，按通过处理。
func scanForStreamError(body io.Reader) *MappedError {
	scanner := bufio.NewScanner(io.LimitReader(body, probeReadLimit))
	scanner.Buffer(make([]byte, 0, 8192), probeLineLimit)

	sawContent := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev CCEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// 超长回显行被截断、或上游插了非 JSON 的心跳，都不影响判定。
			continue
		}
		switch ev.Type {
		case "error":
			return MapEventError(&ev)
		case "text-delta", "reasoning-delta", "tool-call":
			// 有内容流出来了，账号肯定能用，不必再读。
			return nil
		case "finish", "finish-step":
			// 完成信号：即便一个字都没生成（例如 max_tokens 极小被立刻截断），
			// 也说明上游正常受理了这个请求。
			sawContent = true
		}
		if sawContent {
			return nil
		}
	}

	// 扫描中断。ErrTooLong 说明某一行超出了缓冲区——多半还是那条超长回显，
	// 不该据此判定账号坏了。同理，读到上限而未见完成信号也只是信息不足。
	if !sawContent {
		return &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "上游返回成功状态但没有读到任何生成内容或完成信号",
		}
	}
	return nil
}

// ProbeError 把探活失败整理成一句可存进账号 error_message 的话。
func (r ProbeResult) ErrorMessage() string {
	if r.Err == nil {
		return ""
	}
	parts := []string{r.Err.Message}
	if r.Err.ReportedStatus > 0 {
		parts = append(parts, fmt.Sprintf("（上游状态 %d）", r.Err.ReportedStatus))
	}
	if r.Err.Code != "" {
		parts = append(parts, "code="+r.Err.Code)
	}
	return strings.Join(parts, " ")
}
