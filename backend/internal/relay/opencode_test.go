package relay

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cmd2api/internal/config"
)

// collect 把适配器喂进去的行全部跑完，返回下游会收到的事件序列。
func collect(a *openAIStreamAdapter, lines ...string) []*CCEvent {
	var out []*CCEvent
	for _, line := range lines {
		out = append(out, a.ParseLine(line)...)
	}
	return append(out, a.Flush()...)
}

// TestSplitSSELine 验证 SSE 行的解析。
func TestSplitSSELine(t *testing.T) {
	cases := []struct {
		line    string
		payload string
		ok      bool
	}{
		{`data: {"a":1}`, `{"a":1}`, true},
		{"data: [DONE]", "[DONE]", true},
		{"data:{}", "{}", true},
		// 带 \r 的行（有些实现用 CRLF）要能正确处理。
		{"data: {\"a\":1}\r", `{"a":1}`, true},
		// 空行、注释、event/id/retry 行都不是数据行。
		{"", "", false},
		{": keepalive", "", false},
		{"event: message", "", false},
		{"id: 42", "", false},
		{"retry: 1000", "", false},
	}
	for _, tc := range cases {
		payload, ok := SplitSSELine(tc.line)
		if ok != tc.ok {
			t.Errorf("SplitSSELine(%q) ok = %v，期望 %v", tc.line, ok, tc.ok)
			continue
		}
		if ok && payload != tc.payload {
			t.Errorf("SplitSSELine(%q) = %q，期望 %q", tc.line, payload, tc.payload)
		}
	}
}

// TestAdapterTextStream 验证文本增量被转成 text-delta。
func TestAdapterTextStream(t *testing.T) {
	a := newOpenAIStreamAdapter()
	events := collect(a,
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)

	var texts []string
	for _, ev := range events {
		if ev.Type == "text-delta" {
			texts = append(texts, ev.Text)
		}
	}
	if strings.Join(texts, "") != "Hello" {
		t.Errorf("文本增量拼接应为 Hello，得到 %q", strings.Join(texts, ""))
	}

	// 最后一个事件应当是 finish，且带上 stop。
	last := events[len(events)-1]
	if last.Type != "finish" {
		t.Fatalf("最后一个事件应为 finish，得到 %s", last.Type)
	}
	if last.FinishReason != "stop" {
		t.Errorf("finish_reason 应为 stop，得到 %q", last.FinishReason)
	}
}

// TestAdapterReasoningContent 验证推理内容走 reasoning-delta 而不是 text-delta。
//
// 混进正文会让思考过程被当成回答显示给用户。
func TestAdapterReasoningContent(t *testing.T) {
	a := newOpenAIStreamAdapter()
	events := collect(a,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"让我想想"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"答案"}}]}`,
	)

	if len(events) < 2 {
		t.Fatalf("应至少产生 2 个事件，得到 %d", len(events))
	}
	if events[0].Type != "reasoning-delta" || events[0].Text != "让我想想" {
		t.Errorf("第一个事件应为 reasoning-delta，得到 %+v", events[0])
	}
	if events[1].Type != "text-delta" || events[1].Text != "答案" {
		t.Errorf("第二个事件应为 text-delta，得到 %+v", events[1])
	}
}

// TestAdapterToolCallFragments 验证被拆成多片的工具调用参数能拼齐。
//
// OpenAI 把一次工具调用的参数拆成多个分片陆续发来，攒不齐就会得到
// 残缺的 JSON，下游工具调用直接失败。
func TestAdapterToolCallFragments(t *testing.T) {
	a := newOpenAIStreamAdapter()
	events := collect(a,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.txt\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	var calls []*CCEvent
	for _, ev := range events {
		if ev.Type == "tool-call" {
			calls = append(calls, ev)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("应产生 1 个 tool-call，得到 %d", len(calls))
	}
	if calls[0].ToolCallID != "call_1" {
		t.Errorf("工具调用 id 应为 call_1，得到 %q", calls[0].ToolCallID)
	}
	if calls[0].ToolName != "read_file" {
		t.Errorf("工具名应为 read_file，得到 %q", calls[0].ToolName)
	}
	if got := string(calls[0].Input); got != `{"path":"a.txt"}` {
		t.Errorf("参数应拼接完整，得到 %q", got)
	}
}

// TestAdapterParallelToolCalls 验证多个工具调用按 index 正确切分。
func TestAdapterParallelToolCalls(t *testing.T) {
	a := newOpenAIStreamAdapter()
	events := collect(a,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"tool_a","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"tool_b","arguments":"{\"x\":1}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	var names []string
	for _, ev := range events {
		if ev.Type == "tool-call" {
			names = append(names, ev.ToolName)
		}
	}
	if len(names) != 2 || names[0] != "tool_a" || names[1] != "tool_b" {
		t.Errorf("应按顺序得到两个工具调用，得到 %v", names)
	}
}

// TestAdapterUsageFromFinalChunk 验证最后一片里的用量被取到。
//
// 拿不到用量，用量日志就全是 0，统计页等于废掉。
func TestAdapterUsageFromFinalChunk(t *testing.T) {
	a := newOpenAIStreamAdapter()
	events := collect(a,
		`{"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":40}}}`,
	)

	last := events[len(events)-1]
	if last.Type != "finish" {
		t.Fatalf("最后一个事件应为 finish，得到 %s", last.Type)
	}
	if last.TotalUsage == nil {
		t.Fatal("finish 事件应带上用量")
	}
	if last.TotalUsage.InputTokens != 120 || last.TotalUsage.OutputTokens != 8 {
		t.Errorf("用量不对: %+v", last.TotalUsage)
	}
	if last.TotalUsage.CachedInputTokens != 40 {
		t.Errorf("缓存命中 token 应为 40，得到 %d", last.TotalUsage.CachedInputTokens)
	}
}

// TestAdapterInlineError 验证上游把错误放在 200 的流里时能被识别。
func TestAdapterInlineError(t *testing.T) {
	a := newOpenAIStreamAdapter()
	events := collect(a,
		`{"error":{"message":"quota exceeded","type":"insufficient_quota","code":"USAGE_EXCEEDED"}}`,
	)

	if len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("应产生 1 个 error 事件，得到 %+v", events)
	}
	if events[0].Error == nil || events[0].Error.Message != "quota exceeded" {
		t.Errorf("错误信息没取到: %+v", events[0].Error)
	}
	if events[0].Error.Code != "USAGE_EXCEEDED" {
		t.Errorf("错误码应为 USAGE_EXCEEDED，得到 %q", events[0].Error.Code)
	}
}

// TestAdapterSkipsGarbage 验证非 JSON 行被跳过而不是让整条流失败。
func TestAdapterSkipsGarbage(t *testing.T) {
	a := newOpenAIStreamAdapter()
	events := collect(a,
		``,
		`not json at all`,
		`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	)

	var sawText bool
	for _, ev := range events {
		if ev.Type == "text-delta" && ev.Text == "ok" {
			sawText = true
		}
	}
	if !sawText {
		t.Error("垃圾行之后的正常内容应当仍被处理")
	}
}

// TestAdapterFeedsAnthropicEncoder 验证适配器与 Anthropic 编码器接得上。
//
// 这是整个复用设计的关键：OpenCode 的 SSE 经适配后，应当能直接喂给
// 为 Command Code 写的 Anthropic 编码器，产出的帧形如 Anthropic 协议。
func TestAdapterFeedsAnthropicEncoder(t *testing.T) {
	a := newOpenAIStreamAdapter()
	enc := NewAnthropicStreamEncoder("gpt-5.4", "msg_test")

	var frames []string
	feed := func(events []*CCEvent) {
		for _, ev := range events {
			frames = append(frames, enc.Encode(ev)...)
		}
	}

	feed(a.ParseLine(`{"choices":[{"index":0,"delta":{"content":"Hi"}}]}`))
	feed(a.ParseLine(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
	feed(a.Flush())

	tail, truncated, usage := enc.Finish()
	frames = append(frames, tail...)

	if truncated {
		t.Error("正常结束的流不该判定为截断")
	}
	joined := strings.Join(frames, "")
	if !strings.Contains(joined, "message_start") {
		t.Error("应产出 message_start 帧")
	}
	if !strings.Contains(joined, "content_block_delta") {
		t.Error("应产出 content_block_delta 帧")
	}
	if !strings.Contains(joined, "message_stop") {
		t.Error("应产出 message_stop 帧")
	}
	if usage.OutputTokens != 2 {
		t.Errorf("用量应透传：期望输出 2，得到 %d", usage.OutputTokens)
	}
}

// TestToOpenAIBody 验证归一化请求能渲染回 OpenAI 形状。
func TestToOpenAIBody(t *testing.T) {
	maxTokens := 512
	temp := 0.5
	req := &normalizedRequest{
		Model:          "gpt-5.4",
		Messages:       []map[string]any{{"role": "user", "content": "hi"}},
		MaxTokens:      &maxTokens,
		Temperature:    &temp,
		Tools:          []map[string]any{{"type": "function", "function": map[string]any{"name": "t"}}},
		PromptCacheKey: "cache-key",
	}

	raw, err := req.toOpenAIBody()
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("渲染结果不是合法 JSON: %v", err)
	}

	if body["model"] != "gpt-5.4" {
		t.Errorf("model 不对: %v", body["model"])
	}
	// 内部一律按流处理，发给 OpenCode 也必须是流式。
	if body["stream"] != true {
		t.Error("stream 应为 true")
	}
	if body["max_tokens"] != float64(512) {
		t.Errorf("max_tokens 不对: %v", body["max_tokens"])
	}
	if body["prompt_cache_key"] != "cache-key" {
		t.Errorf("prompt_cache_key 应透传: %v", body["prompt_cache_key"])
	}
	// 不要求用量的话拿不到 token 统计。
	so, ok := body["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Error("应带上 stream_options.include_usage 以获取用量")
	}
	if _, ok := body["tools"]; !ok {
		t.Error("tools 应被保留")
	}
}

// TestMapOpenCodeError 验证 OpenCode 的错误映射与 CC 侧一致。
//
// 两个平台共用同一张状态码映射表，保证下游看到的错误语义统一。
func TestMapOpenCodeError(t *testing.T) {
	body := []byte(`{"error":{"message":"invalid api key","type":"authentication_error"}}`)
	got := mapOpenCodeError(401, body)
	if got.Status != 401 {
		t.Errorf("401 应映射为 401，得到 %d", got.Status)
	}
	if !strings.Contains(got.Message, "invalid api key") {
		t.Errorf("应透出上游错误信息，得到 %q", got.Message)
	}

	// 429 要带上退避提示，客户端才知道该等而不是重试。
	rateLimited := mapOpenCodeError(429, nil)
	if rateLimited.Status != 429 || rateLimited.RetryAfter == 0 {
		t.Errorf("429 应带 retry_after，得到 %+v", rateLimited)
	}
}

// TestProbeModelComesFromPlatformList 是实测踩坑后补的回归测试。
//
// 真实事故：探活写死了 Command Code 的模型名（deepseek/deepseek-v4-flash），
// 拿去探 OpenCode 时上游回 "Model deepseek/deepseek-v4-flash is not supported"。
// 后果不是"探活失败"这么简单——每个 OpenCode 账号都会被判成不可用，
// 连续失败达阈值后被健康检查**自动禁用**，而账号其实完全正常。
//
// 所以这里验证：探活模型必须来自该部署实际返回的模型列表。
func TestProbeModelComesFromPlatformList(t *testing.T) {
	// 模拟 OpenCode Go 的模型列表——注意它**没有**任何 Command Code 的模型名。
	const goModels = `{"object":"list","data":[
		{"id":"minimax-m3"},{"id":"minimax-m2.7"},{"id":"kimi-k3"},
		{"id":"glm-5.2"},{"id":"longcat-2.0"}]}`

	var requestedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(goModels))
			return
		}
		// 生成端点：记下客户端用了哪个模型
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		requestedModel = body.Model

		// 复刻真实上游的行为：模型不在列表里就报错。
		if body.Model != "minimax-m3" && body.Model != "minimax-m2.7" && body.Model != "kimi-k3" &&
			body.Model != "glm-5.2" && body.Model != "longcat-2.0" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Model ` + body.Model + ` is not supported"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	client := NewClient(config.CommandCodeConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. 挑出来的模型必须来自列表
	model := client.pickProbeModel(context.Background(), srv.URL, "k")
	if model == "" {
		t.Fatal("应当能从列表里挑出探活模型")
	}
	valid := map[string]bool{"minimax-m3": true, "minimax-m2.7": true, "kimi-k3": true, "glm-5.2": true, "longcat-2.0": true}
	if !valid[model] {
		t.Errorf("挑出的模型 %q 不在该部署支持的列表里", model)
	}
	// 明确防止回归：绝不能挑出 Command Code 的模型名。
	if model == commandCodeProbeModel {
		t.Errorf("探活用了 Command Code 的模型名 %q，这正是当初导致账号被误禁用的原因", model)
	}
	// 偏好便宜模型：列表里有 m2.7，应当优先于列表第一个 m3（同样命中偏好时取先出现的）。
	if model != "minimax-m2.7" {
		t.Logf("提示：挑中的是 %q；偏好顺序里 flash/mini/lite/m2.7 应优先命中", model)
	}

	// 2. 真跑一次探活，确认它不因为"模型不支持"而失败
	result := client.openCodeProbe(context.Background(), srv.URL, "some-key")
	if result.Err != nil {
		t.Fatalf("探活不应失败，却报了: %s", result.Err.Message)
	}
	if requestedModel == commandCodeProbeModel {
		t.Errorf("实际请求用的模型是 %q —— 回归了！", requestedModel)
	}
	t.Logf("探活实际使用的模型: %s", requestedModel)
}

// TestProbeModelEmptyListFails 验证拿不到模型列表时明确报错而不是瞎猜。
func TestProbeModelEmptyListFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := NewClient(config.CommandCodeConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if model := client.pickProbeModel(context.Background(), srv.URL, "k"); model != "" {
		t.Errorf("列表拿不到时应返回空，得到 %q", model)
	}

	result := client.openCodeProbe(context.Background(), srv.URL, "k")
	if result.Err == nil {
		t.Fatal("拿不到模型列表时探活应报错（说明网络有问题），而不是假装成功")
	}
	if !strings.Contains(result.Err.Message, "模型列表") {
		t.Errorf("错误信息应说明是模型列表的问题，得到: %s", result.Err.Message)
	}
}

// TestAdapterFlushOnReadError 是实测踩坑后补的回归测试。
//
// 真实事故：上游发完内容后连接没有立刻关闭（SSE 长连接的正常形态），
// 读操作阻塞到空闲看门狗超时才返回错误，而服务层当时写成
// `if err != nil { return err }` 直接跳过了 Flush，
// 结果 finish 事件和整条用量统计一起丢失——客户端看到"回答完了但没有 token 数"。
//
// 这里锁定两条规则：
//  1. 上游给过完成信号 → 读出错也要 flush，finish 必须补上
//  2. 上游什么都没给就断 → 不能补，否则截断会被粉饰成正常结束
func TestAdapterFlushOnReadError(t *testing.T) {
	// 情况一：收到了 finish_reason 和 usage，属于"上游已完成"
	completed := newOpenAIStreamAdapter()
	completed.ParseLine(`{"choices":[{"index":0,"delta":{"content":"hi"}}]}`)
	completed.ParseLine(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)

	if !completed.SawCompletion() {
		t.Fatal("收到 finish_reason 和 usage 后应判定为上游已完成")
	}
	events := completed.Flush()
	if len(events) != 1 || events[0].Type != "finish" {
		t.Fatalf("应补出一个 finish 事件，得到 %+v", events)
	}
	if events[0].TotalUsage == nil || events[0].TotalUsage.InputTokens != 10 {
		t.Errorf("finish 事件应带上用量，得到 %+v", events[0].TotalUsage)
	}

	// 情况二：只收到内容就断了，属于截断
	truncated := newOpenAIStreamAdapter()
	truncated.ParseLine(`{"choices":[{"index":0,"delta":{"content":"half"}}]}`)
	if truncated.SawCompletion() {
		t.Error("只收到内容、没有任何完成信号时不该判定为已完成——那会把截断粉饰成正常结束")
	}

	// [DONE] 本身也算完成信号
	doneOnly := newOpenAIStreamAdapter()
	doneOnly.ParseLine("[DONE]")
	if !doneOnly.SawCompletion() {
		t.Error("[DONE] 是明确的完成信号，应当被认作已完成")
	}
}
