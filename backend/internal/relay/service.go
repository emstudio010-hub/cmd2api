package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"cmd2api/internal/config"
	"cmd2api/internal/domain"
	"cmd2api/internal/scheduler"
)

// Protocol 标识入站请求说的是哪种协议。
type Protocol string

const (
	// ProtocolOpenAI 是 OpenAI Chat Completions（/v1/chat/completions）。
	ProtocolOpenAI Protocol = "openai"
	// ProtocolAnthropic 是 Anthropic Messages（/v1/messages）。
	ProtocolAnthropic Protocol = "anthropic"
)

// UsageRecord 是一次请求的用量记录。
//
// relay 不直接写数据库，而是通过 UsageRecorder 回调出去：
// 这样 relay 只依赖「记一笔账」这个抽象，不必知道表结构，
// 也便于测试时塞一个假实现。
type UsageRecord struct {
	UserID    int64
	APIKeyID  int64
	AccountID int64
	GroupID   *int64

	RequestID     string
	Model         string
	UpstreamModel string

	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int

	TotalCost      float64
	RateMultiplier float64

	Stream       bool
	DurationMs   int
	FirstTokenMs *int
	StatusCode   int
	ErrorMessage string
	UserAgent    string
	IPAddress    string
}

// UsageRecorder 落库一次用量。
type UsageRecorder interface {
	Record(ctx context.Context, rec UsageRecord)
}

// RelayRequest 是一次中转请求的输入。
type RelayRequest struct {
	Protocol Protocol
	// Body 是原始请求体（已读全）。
	Body []byte

	APIKeyID int64
	UserID   int64
	GroupID  int64

	RequestID string
	UserAgent string
	ClientIP  string
	// SessionHints 是请求头里带的会话标识，按优先级排列。
	SessionHints []string

	// RateMultiplier 是分组倍率，用于结算。
	RateMultiplier float64
}

// Service 编排一次中转请求的完整流程。
type Service struct {
	client   *Client
	sched    *scheduler.Scheduler
	recorder UsageRecorder
	cfg      *config.Config
	logger   *slog.Logger
}

// NewService 构造 Service。
func NewService(client *Client, sched *scheduler.Scheduler, recorder UsageRecorder, cfg *config.Config, logger *slog.Logger) *Service {
	return &Service{
		client:   client,
		sched:    sched,
		recorder: recorder,
		cfg:      cfg,
		logger:   logger,
	}
}

// Models 返回可用于该分组的模型列表。
//
// 模型列表与账号无关（同一个上游服务），但调用需要一把有效凭证，
// 所以借分组内任一账号用一下——不占用它的并发名额。
func (s *Service) Models(ctx context.Context, groupID int64) ([]Model, error) {
	target, err := s.sched.AnyTarget(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if target.Platform == domain.PlatformOpenCode {
		return s.client.openCodeModels(ctx, target.BaseURL, target.APIKey), nil
	}
	return s.client.Models(ctx, target.APIKey), nil
}

// MaxAccountAttempts 是一次请求最多尝试几个上游账号。
//
// 故障转移要有限度：一个格式错误的请求发给 10 个账号都只会被拒 10 次，
// 还会把 10 个账号的连续失败计数全推高一格。
const maxAccountAttempts = 3

// Relay 处理一次中转请求。
func (s *Service) Relay(ctx context.Context, w http.ResponseWriter, req *RelayRequest) {
	started := time.Now()

	normalized, err := s.parse(req)
	if err != nil {
		s.writeClientError(w, req.Protocol, http.StatusBadRequest, "invalid_request_error", err.Error(), 0)
		return
	}
	// 客户端自带的会话标识优先于自动分配。
	if len(req.SessionHints) > 0 {
		normalized.SessionHints = append(req.SessionHints, normalized.SessionHints...)
	}

	var (
		// firstErr 是**第一个**账号报的错，最终回报给客户端的就是它。
		//
		// 不用最后一次的错误：备选账号通常是兜底用的杂牌账号，它们的报错
		// 不如主账号的有信息量。实测踩过这个坑——主账号明确报了
		// "MODEL_NOT_IN_PLAN: ... available in Pro and above plans"，
		// 结果被后面兜底账号的一句「认证失败」盖掉，客户端完全看不出真实原因。
		firstErr *MappedError
		// tried 记录本次请求已经用过的账号，重试时排除掉。
		// 不排除的话会在同一个坏账号上反复失败——同一个请求发给同一个账号，
		// 结果不会变，只是把错误路径从亚秒级拖到几十秒。
		tried []int64
	)

	for attempt := 0; attempt < maxAccountAttempts; attempt++ {
		lease, err := s.sched.Acquire(ctx, req.GroupID, tried...)
		if err != nil {
			if errors.Is(err, scheduler.ErrNoAccountAvailable) {
				// 已经试过账号但还是失败，说明池子被试完了——这时该回报
				// 那次真实的上游错误，而不是「没有可用账号」这种误导性说法。
				if firstErr != nil {
					break
				}
				s.writeClientError(w, req.Protocol, http.StatusServiceUnavailable,
					"api_error", "分组内没有可用账号，请检查账号状态或稍后重试", 15)
				return
			}
			s.logger.Error("调度账号失败", "err", err, "group_id", req.GroupID)
			s.writeClientError(w, req.Protocol, http.StatusInternalServerError,
				"api_error", "调度上游账号失败", 0)
			return
		}

		tried = append(tried, lease.Account.ID)
		done, mappedErr := s.attempt(ctx, w, req, normalized, lease, started)
		lease.Release()

		if done {
			return
		}

		if firstErr == nil {
			firstErr = mappedErr
		}
		if mappedErr != nil {
			// 每一次失败都记下来：最终只回报第一个错误，但排障时需要看到全貌。
			s.logger.Warn("上游账号失败，换下一个账号重试",
				"request_id", req.RequestID, "account_id", lease.Account.ID,
				"account_name", lease.Account.Name,
				"attempt", attempt+1, "status", mappedErr.Status,
				"code", mappedErr.Code, "message", mappedErr.Message)

			if !mappedErr.Retryable() {
				break
			}
		}
	}

	if firstErr == nil {
		firstErr = &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "上游请求失败", ReportedStatus: http.StatusBadGateway,
		}
	}
	s.writeClientError(w, req.Protocol, firstErr.Status, firstErr.Type, firstErr.Message, firstErr.RetryAfter)
}

// attempt 按账号所属平台分发到对应的协议处理。
//
// 第二个返回值在 done=false 时才有意义，表示「这个错误值得换一个账号重试」。
func (s *Service) attempt(
	ctx context.Context,
	w http.ResponseWriter,
	req *RelayRequest,
	normalized *normalizedRequest,
	lease *scheduler.Lease,
	started time.Time,
) (done bool, mappedErr *MappedError) {
	if lease.Platform == domain.PlatformOpenCode {
		return s.attemptOpenCode(ctx, w, req, normalized, lease, started)
	}
	return s.attemptCommandCode(ctx, w, req, normalized, lease, started)
}

// attemptOpenCode 走 OpenAI 兼容上游。
//
// 与 Command Code 那条路的区别只在「怎么发」和「怎么读」：请求不需要信封，
// 响应是标准 SSE 而不是 NDJSON。读出事件之后的翻译、用量统计、错误处理
// 全部复用同一条下游链路。
func (s *Service) attemptOpenCode(
	ctx context.Context,
	w http.ResponseWriter,
	req *RelayRequest,
	normalized *normalizedRequest,
	lease *scheduler.Lease,
	started time.Time,
) (done bool, mappedErr *MappedError) {
	baseURL := lease.BaseURL
	if baseURL == "" {
		baseURL = domain.DefaultOpenCodeBaseURL(lease.AccountMode)
	}

	body, err := normalized.toOpenAIBody()
	if err != nil {
		s.logger.Error("构造 OpenCode 请求失败", "err", err, "request_id", req.RequestID)
		s.writeClientError(w, req.Protocol, http.StatusInternalServerError, "api_error", "构造上游请求失败", 0)
		return true, nil
	}

	resp, err := s.client.openCodeGenerate(ctx, baseURL, lease.Credentials.APIKey, body)
	if err != nil {
		if ctx.Err() != nil {
			return true, nil
		}
		wrapped := fmt.Errorf("请求 OpenCode 上游失败: %w", err)
		s.logger.Warn("请求 OpenCode 失败",
			"err", err, "request_id", req.RequestID, "account_id", lease.Account.ID)
		s.sched.MarkFailure(ctx, lease.Account.ID, wrapped)
		s.record(req, lease.Account.ID, started, 0, 0, 0, req.RateMultiplier,
			http.StatusBadGateway, err.Error())
		return false, &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "无法连接 OpenCode 上游", ReportedStatus: http.StatusBadGateway,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		mapped := mapOpenCodeError(resp.StatusCode, raw)
		s.applyAccountPenalty(ctx, lease.Account.ID, mapped)
		s.record(req, lease.Account.ID, started, 0, 0, 0, req.RateMultiplier, mapped.Status, mapped.Message)
		if mapped.Retryable() {
			return false, mapped
		}
		s.writeClientError(w, req.Protocol, mapped.Status, mapped.Type, mapped.Message, mapped.RetryAfter)
		return true, mapped
	}

	s.sched.MarkUsed(ctx, lease.Account.ID)

	// 把 SSE 读成 CC 事件流，之后就走与 Command Code 完全相同的下游链路。
	adapter := newOpenAIStreamAdapter()
	read := func(handle func(*CCEvent)) error {
		err := s.client.readOpenAISSE(ctx, resp.Body, s.cfg.CommandCC.StreamIdleTimeout, adapter.emitTo(handle))

		// 读结束就一定要把适配器里攒着的东西吐出来，**不能因为读出错就跳过**。
		//
		// 踩过的坑：上游发完内容后不关连接（SSE 的长连接本来就不该被客户端
		// 当成"读完即结束"），读操作要等空闲看门狗超时才返回错误。原来写成
		// `if err != nil { return err }` 直接跳过了 flush，于是 finish 事件和
		// 整条用量统计一起丢了——客户端看到的是"流莫名其妙结束、没有 token 数"。
		//
		// 但也不能无条件补 finish：上游一个完成信号都没给就断了，本来就该
		// 判定为截断，补一个 finish 会把截断粉饰成正常结束。
		if adapter.SawCompletion() {
			for _, ev := range adapter.Flush() {
				handle(ev)
			}
		}
		return err
	}

	return s.streamEvents(ctx, w, req, normalized, lease.Account.ID, started, read)
}

// attemptCommandCode 走 Command Code 自有协议。
func (s *Service) attemptCommandCode(
	ctx context.Context,
	w http.ResponseWriter,
	req *RelayRequest,
	normalized *normalizedRequest,
	lease *scheduler.Lease,
	started time.Time,
) (done bool, mappedErr *MappedError) {
	apiKey := lease.Credentials.APIKey
	device := DeviceProfileFor("")

	// threadId 取本次会话标识；不是合法 UUID 时信封里会自动省略。
	threadID := ""
	if len(normalized.SessionHints) > 0 {
		threadID = normalized.SessionHints[0]
	} else {
		threadID = s.client.sessionFor(apiKey)
	}

	envelope, err := buildEnvelope(normalized, "agent", device, threadID, s.cfg.CommandCC.EmptySystemPlaceholder)
	if err != nil {
		s.logger.Error("构造上游请求失败", "err", err, "request_id", req.RequestID)
		s.record(req, lease.Account.ID, started, 0, 0, 0, req.RateMultiplier,
			http.StatusInternalServerError, err.Error())
		s.writeClientError(w, req.Protocol, http.StatusInternalServerError, "api_error", "构造上游请求失败", 0)
		return true, nil
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		s.logger.Error("序列化上游请求失败", "err", err, "request_id", req.RequestID)
		s.writeClientError(w, req.Protocol, http.StatusInternalServerError, "api_error", "序列化上游请求失败", 0)
		return true, nil
	}

	// 指纹/生命周期预请求。已节流到 8 小时一次，绝大多数请求会直接返回。
	// 失败也不阻塞生成——它只是辅助信号。
	s.client.EnsureInitialized(ctx, apiKey)

	resp, err := s.client.Generate(ctx, apiKey, normalized.SessionHints, payload)
	if err != nil {
		// 客户端主动断开不算账号的错，别把失败计到账号头上。
		if ctx.Err() != nil {
			return true, nil
		}
		wrapped := fmt.Errorf("请求上游失败: %w", err)
		s.logger.Warn("请求上游失败", "err", err, "request_id", req.RequestID, "account_id", lease.Account.ID)
		s.sched.MarkFailure(ctx, lease.Account.ID, wrapped)
		s.record(req, lease.Account.ID, started, 0, 0, 0, req.RateMultiplier,
			http.StatusBadGateway, err.Error())
		return false, &MappedError{
			Status: http.StatusBadGateway, Type: "upstream_error",
			Message: "无法连接上游服务", ReportedStatus: http.StatusBadGateway,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		mapped := MapHTTPError(resp.StatusCode, body)

		s.applyAccountPenalty(ctx, lease.Account.ID, mapped)
		s.record(req, lease.Account.ID, started, 0, 0, 0, req.RateMultiplier, mapped.Status, mapped.Message)

		if mapped.Retryable() {
			return false, mapped
		}
		s.writeClientError(w, req.Protocol, mapped.Status, mapped.Type, mapped.Message, mapped.RetryAfter)
		return true, mapped
	}

	s.sched.MarkUsed(ctx, lease.Account.ID)

	read := func(handle func(*CCEvent)) error {
		return s.readNDJSON(ctx, resp.Body, s.cfg.CommandCC.StreamIdleTimeout, handle)
	}
	return s.streamEvents(ctx, w, req, normalized, lease.Account.ID, started, read)
}

// applyAccountPenalty 按错误类型给账号记账。
func (s *Service) applyAccountPenalty(ctx context.Context, accountID int64, mapped *MappedError) {
	switch {
	case mapped.IsRateLimit():
		// 上游没给 reset 时间时由 scheduler 兜底 5 分钟。
		s.sched.MarkRateLimited(ctx, accountID, nil)
	default:
		// 其余一律按失败累计，连续几次后自动禁用。
		//
		// 这里原来把 IsAuthFailure 单列成一个 case，但两个分支的代码一模一样
		// ——认证失败和别的失败走的是同一条路（都是累计到阈值才禁用）。留着
		// 那个 case 会让人以为认证失败有特殊处理，查问题时往错的方向找。
		s.sched.MarkFailure(ctx, accountID, errors.New(mapped.Message))
	}
}

// streamEvents 是下游共用的处理链路：把上游事件流翻译成客户端要的协议，
// 顺带做用量统计、截断检测和错误收尾。
//
// 上游协议的差异被隔离在 read 里——无论读的是 Command Code 的 NDJSON
// 还是 OpenCode 的 SSE，这里之后的逻辑完全一致。
func (s *Service) streamEvents(
	ctx context.Context,
	w http.ResponseWriter,
	req *RelayRequest,
	normalized *normalizedRequest,
	accountID int64,
	started time.Time,
	read func(handle func(*CCEvent)) error,
) (bool, *MappedError) {
	streaming := normalized.Stream
	model := normalized.Model
	created := time.Now().Unix()
	requestID := req.RequestID

	if streaming {
		// 状态头和 Content-Type 必须在写第一帧之前落定，之后再也改不了。
		//
		// Content-Type 必须是 text/event-stream：客户端 SDK 靠它决定按 SSE
		// 解析还是按普通文本读。之前这里漏了，返回的是 Go 默认的 text/plain，
		// 严格的客户端会直接拒绝解析。
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// 让 nginx 别缓冲这一路。DEPLOY.md 里也让人在反代上关 buffering，
		// 但那个配置容易被漏掉，这个响应头是第二道保险。
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}

	var (
		openaiEnc    *OpenAIStreamEncoder
		anthropicEnc *AnthropicStreamEncoder
	)
	if streaming {
		if req.Protocol == ProtocolAnthropic {
			anthropicEnc = NewAnthropicStreamEncoder(model, "msg_"+requestID)
		} else {
			openaiEnc = NewOpenAIStreamEncoder(model, "chatcmpl-"+requestID, created)
		}
	}

	// 非流式请求也要把回答攒起来，因为 CC 上游永远返回流。
	var (
		fullText      strings.Builder
		fullReasoning strings.Builder
		events        []*CCEvent
		firstTokenAt  *time.Time
	)

	var writeErr error
	writeFrame := func(frame string) {
		if writeErr != nil || frame == "" {
			return
		}
		if _, err := io.WriteString(w, frame); err != nil {
			writeErr = err
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	readErr := read(func(ev *CCEvent) {
		if firstTokenAt == nil && hasVisibleContent(ev) {
			now := time.Now()
			firstTokenAt = &now
		}
		events = append(events, ev)

		switch ev.Type {
		case "text-delta":
			fullText.WriteString(pickText(ev))
		case "reasoning-delta":
			fullReasoning.WriteString(ev.Text)
		}

		if !streaming {
			return
		}
		if anthropicEnc != nil {
			for _, frame := range anthropicEnc.Encode(ev) {
				writeFrame(frame)
			}
			return
		}
		for _, frame := range openaiEnc.Encode(ev) {
			writeFrame(frame)
		}
	})

	// 上游流里报了错：把错误交给客户端，而不是假装回答完了。
	var upstreamErr *MappedError
	if openaiEnc != nil {
		upstreamErr = openaiEnc.UpstreamError
	}
	if anthropicEnc != nil {
		upstreamErr = mapAnthropicStreamError(anthropicEnc.UpstreamError())
	}
	if upstreamErr != nil {
		s.applyAccountPenalty(ctx, accountID, upstreamErr)
		s.recordWithUsage(req, accountID, started, openaiEnc, anthropicEnc, firstTokenAt,
			upstreamErr.Status, upstreamErr.Message)

		if !streaming {
			s.writeClientError(w, req.Protocol, upstreamErr.Status, upstreamErr.Type, upstreamErr.Message, upstreamErr.RetryAfter)
			return true, upstreamErr
		}
		// 已经写出分片了就没法改状态码，只能按协议格式补一个错误帧。
		s.writeStreamError(w, req.Protocol, upstreamErr)
		return true, upstreamErr
	}

	if readErr != nil {
		if ctx.Err() != nil {
			// 客户端断开：不计费、不惩罚账号。
			return true, nil
		}
		// 读流中途失败。如果已经写出内容，只能中断；否则还能重试别的账号。
		if !streaming && fullText.Len() == 0 {
			s.sched.MarkFailure(ctx, accountID, readErr)
			s.record(req, accountID, started, 0, 0, 0, req.RateMultiplier,
				http.StatusBadGateway, readErr.Error())
			return false, &MappedError{
				Status: http.StatusBadGateway, Type: "upstream_error",
				Message: "读取上游响应失败", ReportedStatus: http.StatusBadGateway,
			}
		}
		s.logger.Warn("读取上游流中断", "err", readErr, "request_id", requestID, "account_id", accountID)
	}

	// 检查流是不是被截断了。截断的回答必须让客户端知道，否则会被
	// 当成完整回答写进用户的代码里。
	var truncated bool
	var detail string
	if streaming {
		if anthropicEnc != nil {
			frames, trunc, _ := anthropicEnc.Finish()
			for _, frame := range frames {
				writeFrame(frame)
			}
			truncated, detail = trunc, ""
		} else {
			truncated, detail = openaiEnc.Finish()
		}
	} else {
		if anthropicEnc != nil {
			_, truncated, _ = anthropicEnc.Finish()
		} else if openaiEnc != nil {
			truncated, detail = openaiEnc.Finish()
		}
	}

	// 非流式：攒完再一次性返回完整响应。
	if !streaming {
		finishReason, usage := finalOutcome(events)
		if truncated {
			msg := "上游流未正常结束（" + detail + "），响应可能被截断"
			s.record(req, accountID, started, usage.InputTokens, usage.OutputTokens,
				usage.CachedInputTokens, req.RateMultiplier, http.StatusBadGateway, msg)
			s.writeClientError(w, req.Protocol, http.StatusBadGateway, "upstream_error", msg, 10)
			return true, nil
		}
		s.record(req, accountID, started, usage.InputTokens, usage.OutputTokens,
			usage.CachedInputTokens, req.RateMultiplier, http.StatusOK, "")

		if req.Protocol == ProtocolAnthropic {
			s.writeAnthropicFullResponse(w, model, requestID, fullText.String(), fullReasoning.String(),
				collectToolCalls(events), finishReason, usage)
		} else {
			s.writeOpenAIFullResponse(w, model, requestID, created, fullText.String(), fullReasoning.String(),
				collectToolCalls(events), finishReason, usage)
		}
		return true, nil
	}

	if truncated {
		s.logger.Warn("上游流被截断", "detail", detail, "request_id", requestID)
	}
	if openaiEnc != nil {
		writeFrame(openaiEnc.DoneFrame())
	}
	s.recordWithUsage(req, accountID, started, openaiEnc, anthropicEnc, firstTokenAt, http.StatusOK, "")

	if writeErr != nil && ctx.Err() == nil {
		s.logger.Debug("向客户端写响应失败（通常是客户端已断开）", "err", writeErr, "request_id", requestID)
	}
	return true, nil
}

// readNDJSON 逐行读上游的 NDJSON 流。
//
// 上游发的不是 SSE 而是「每行一个 JSON」，所以按行切。
// 空闲看门狗用定时关闭 body 的方式实现：Read 阻塞时只能靠外部打断，
// 定时器关掉 body 会让 Read 立刻返回错误。
func (s *Service) readNDJSON(ctx context.Context, body io.ReadCloser, idle time.Duration, handle func(*CCEvent)) error {
	if idle <= 0 {
		idle = 30 * time.Second
	}
	watchdog := time.AfterFunc(idle, func() { _ = body.Close() })
	defer watchdog.Stop()

	scanner := bufio.NewScanner(body)
	// 单个 JSON 事件可能很大（工具调用参数、长文本增量），默认 64KB 不够。
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		watchdog.Reset(idle)
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "[DONE]" || strings.HasPrefix(line, ":") {
			continue
		}
		var ev CCEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// 解析不了的行直接跳过：上游偶尔会插入心跳或非 JSON 行，
			// 为一行垃圾中断整条流不划算。
			s.logger.Debug("跳过无法解析的上游行", "line", truncate(line, 200))
			continue
		}
		handle(&ev)
	}
	return scanner.Err()
}

// parse 按协议解析请求体。
func (s *Service) parse(req *RelayRequest) (*normalizedRequest, error) {
	if req.Protocol == ProtocolAnthropic {
		return ParseAnthropicMessagesRequest(req.Body)
	}
	return ParseOpenAIChatRequest(req.Body)
}

func pickText(ev *CCEvent) string {
	if ev.Text != "" {
		return ev.Text
	}
	return ev.Delta
}

// hasVisibleContent 判断这个事件是否算「看到了第一个 token」。
func hasVisibleContent(ev *CCEvent) bool {
	switch ev.Type {
	case "text-delta", "reasoning-delta", "tool-call":
		return true
	}
	return false
}

// finalOutcome 从攒下的所有事件里推出结束原因和用量。
func finalOutcome(events []*CCEvent) (string, *CCUsage) {
	finishReason := "stop"
	usage := &CCUsage{}
	for _, ev := range events {
		switch ev.Type {
		case "finish-step":
			if ev.FinishReason != "" {
				finishReason = mapFinishReason(ev.FinishReason)
			}
			if ev.Usage != nil {
				usage = ev.Usage
			}
		case "finish":
			if ev.FinishReason != "" {
				finishReason = mapFinishReason(ev.FinishReason)
			}
			if ev.TotalUsage != nil {
				usage = ev.TotalUsage
			}
		}
	}
	normalizeUsage(usage)
	return finishReason, usage
}

// record 记一笔用量（不带首 token 时间）。
func (s *Service) record(req *RelayRequest, accountID int64, started time.Time,
	inputTokens, outputTokens, cachedTokens int, multiplier float64,
	statusCode int, errMsg string) {
	s.recordWithUsage(req, accountID, started, nil, nil, nil, statusCode, errMsg)
}

// recordWithUsage 记一笔用量，优先取编码器里累计的统计。
func (s *Service) recordWithUsage(req *RelayRequest, accountID int64, started time.Time,
	openaiEnc *OpenAIStreamEncoder, anthropicEnc *AnthropicStreamEncoder,
	firstTokenAt *time.Time, statusCode int, errMsg string) {
	if s.recorder == nil {
		return
	}

	rec := UsageRecord{
		UserID:         req.UserID,
		APIKeyID:       req.APIKeyID,
		AccountID:      accountID,
		RequestID:      req.RequestID,
		Model:          "",
		Stream:         true,
		DurationMs:     int(time.Since(started).Milliseconds()),
		StatusCode:     statusCode,
		ErrorMessage:   errMsg,
		UserAgent:      req.UserAgent,
		IPAddress:      req.ClientIP,
		RateMultiplier: req.RateMultiplier,
	}
	groupID := req.GroupID
	rec.GroupID = &groupID

	if firstTokenAt != nil {
		ms := int(firstTokenAt.Sub(started).Milliseconds())
		rec.FirstTokenMs = &ms
	}

	switch {
	case openaiEnc != nil:
		rec.InputTokens = openaiEnc.InputTokens
		rec.OutputTokens = openaiEnc.OutputTokens
		rec.CacheReadTokens = openaiEnc.CachedInputTokens
		rec.Model = openaiEnc.model
	case anthropicEnc != nil:
		u := anthropicEnc.Usage()
		rec.InputTokens = u.InputTokens
		rec.OutputTokens = u.OutputTokens
		rec.CacheReadTokens = u.CacheReadInputTokens
		rec.CacheCreationTokens = u.CacheCreationInputTokens
		rec.Model = anthropicEnc.model
	}

	// 用量上报失败不该影响用户请求，记日志即可。
	if err := safeRecord(context.WithoutCancel(context.Background()), s.recorder, rec); err != nil {
		s.logger.Warn("记录用量失败", "err", err, "request_id", req.RequestID)
	}
}

// safeRecord 包一层 recover：recorder 是外部实现，不该因为它的 panic 把请求带崩。
func safeRecord(ctx context.Context, r UsageRecorder, rec UsageRecord) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("recorder panic: %v", v)
		}
	}()
	r.Record(ctx, rec)
	return nil
}

// truncate 按字符（不是字节）截断。
//
// 按字节切会把多字节字符劈成两半，产生非法 UTF-8——上游的报错和流式输出
// 里中文不少，切出来的乱码会一路进日志和界面。
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
