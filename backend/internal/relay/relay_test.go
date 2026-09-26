package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFingerprintDeterministic 是这套协议里最重要的一条不变量。
//
// 指纹代表「这个账号对应的那台设备」。如果同一个 key 每次算出不同指纹，
// 上游看到的就是「同一个账号在不停换机器」——这本身就是最明显的异常信号。
// 所以这里必须逐字段验证稳定性，而不只是验证「两次结果相等」。
func TestFingerprintDeterministic(t *testing.T) {
	const key = "user_test1234567890"

	first := GenerateFingerprint(key, "", "")
	second := GenerateFingerprint(key, "", "")

	if first.Thumbmark != second.Thumbmark {
		t.Fatalf("同一把 key 两次生成的 thumbmark 不一致:\n%s\n%s", first.Thumbmark, second.Thumbmark)
	}
	if len(first.Thumbmark) != 64 {
		t.Errorf("thumbmark 应为 64 位十六进制，实际 %d 位", len(first.Thumbmark))
	}

	// components 里每个派生字段都要稳定。
	if first.Components.MachineIDHash != second.Components.MachineIDHash {
		t.Error("machineIdHash 不稳定")
	}
	if first.Components.HostnameHash != second.Components.HostnameHash {
		t.Error("hostnameHash 不稳定")
	}
	if first.Components.CPUModel != second.Components.CPUModel {
		t.Error("cpuModel 不稳定")
	}
	if len(first.Components.MACHashes) != len(second.Components.MACHashes) {
		t.Error("macHashes 数量不稳定")
	}
	// MAC 排序后上报，顺序也必须稳定。
	for i := range first.Components.MACHashes {
		if first.Components.MACHashes[i] != second.Components.MACHashes[i] {
			t.Errorf("macHashes[%d] 不稳定", i)
		}
	}
}

// TestFingerprintDiffersByKey 确保不同账号拿到不同设备。
//
// 如果所有 key 都映射到同一台设备，那就等于「一堆账号在同一台机器上跑」，
// 比不伪装还糟。
func TestFingerprintDiffersByKey(t *testing.T) {
	seen := make(map[string]string)
	keys := []string{"user_aaa", "user_bbb", "user_ccc", "user_ddd", "user_eee"}
	for _, k := range keys {
		fp := GenerateFingerprint(k, "", "")
		if prev, dup := seen[fp.Thumbmark]; dup {
			t.Errorf("key %s 与 %s 的 thumbmark 相同: %s", k, prev, fp.Thumbmark)
		}
		seen[fp.Thumbmark] = k
	}
}

// TestFingerprintSaltRotatesDevice 验证 salt 能整体换身份。
//
// 这是逃生口：万一上游把某个设备标记了，改 salt 就能让所有账号换设备，
// 而真实账号的 key 是动不了的。
func TestFingerprintSaltRotatesDevice(t *testing.T) {
	const key = "user_test1234567890"
	base := GenerateFingerprint(key, "", "")
	salted := GenerateFingerprint(key, "rotated-salt", "")
	if base.Thumbmark == salted.Thumbmark {
		t.Error("改了 salt 之后 thumbmark 没变，逃生口失效")
	}
}

// TestFingerprintShapeMatchesCLI 校验那些必须逐字对上的形态约束。
func TestFingerprintShapeMatchesCLI(t *testing.T) {
	fp := GenerateFingerprint("user_shape_check", "", "")
	c := fp.Components

	if c.Platform != "win32" || c.Arch != "x64" {
		t.Errorf("平台档案应与内置 profile 自洽，得到 %s-%s", c.Platform, c.Arch)
	}
	if c.Runtime != "cli" {
		t.Errorf("runtime 应为 cli，得到 %s", c.Runtime)
	}
	if c.CollectorVer != 1 {
		t.Errorf("collectorVersion 应为 1，得到 %d", c.CollectorVer)
	}
	if c.IsContainer {
		t.Error("isContainer 应为 false")
	}
	if len(c.MACHashes) < 2 || len(c.MACHashes) > 5 {
		t.Errorf("MAC 数量应在 2~5 之间，得到 %d", len(c.MACHashes))
	}
	// 所有 hash 信号都是 sha256 十六进制。
	for name, h := range map[string]string{
		"machineIdHash": c.MachineIDHash,
		"hostnameHash":  c.HostnameHash,
		"osUserHash":    c.OSUserHash,
		"gitEmailHash":  c.GitEmailHash,
	} {
		if len(h) != 64 {
			t.Errorf("%s 应为 64 位十六进制，实际 %d 位", name, len(h))
		}
	}
}

// TestSlugifyProjectPath 校验 x-project-slug 的取值。
//
// 上游期望 slug 与 workingDir 同源（真机上 slug = slugify(workingDir)）,
// 所以这里不能直接用配置里的 projectSlug。
func TestSlugifyProjectPath(t *testing.T) {
	cases := map[string]string{
		`C:\Users\dev\projects\app`: "c-users-dev-projects-app",
		"/home/dev/app":             "home-dev-app",
		"":                          "root",
		"---":                       "root",
		"a--b":                      "a-b",
		"UPPER Case":                "upper-case",
	}
	for in, want := range cases {
		if got := slugifyProjectPath(in); got != want {
			t.Errorf("slugifyProjectPath(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestMapFinishReason 覆盖几个容易出错的映射。
//
// 重点是 'length' 家族不止一个值：把 max_output_tokens 折成 stop
// 等于把被截断的半截回答谎报成完整回答。
func TestMapFinishReason(t *testing.T) {
	cases := map[string]string{
		"":                              "stop",
		"stop":                          "stop",
		"end_turn":                      "end_turn",
		"tool_use":                      "tool_calls",
		"tool-calls":                    "tool_calls",
		"tool_calls":                    "tool_calls",
		"length":                        "length",
		"max_tokens":                    "length",
		"max_output_tokens":             "length",
		"model_context_window_exceeded": "length",
		"network-error":                 "upstream_error",
		"connection error":              "upstream_error",
		"pause_turn":                    "pause_turn",
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestNormalizeUsageZeroesFalseBilling 验证「有输入无输出」的残缺统计被清零。
func TestNormalizeUsageZeroesFalseBilling(t *testing.T) {
	u := &CCUsage{InputTokens: 5000, OutputTokens: 0, CachedInputTokens: 100}
	normalizeUsage(u)
	if u.InputTokens != 0 || u.CachedInputTokens != 0 {
		t.Errorf("outputTokens 为 0 时应清零输入统计，得到 input=%d cached=%d", u.InputTokens, u.CachedInputTokens)
	}

	// 正常情况不该被改动。
	ok := &CCUsage{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10}
	normalizeUsage(ok)
	if ok.InputTokens != 100 {
		t.Errorf("正常统计被误改：input=%d", ok.InputTokens)
	}
}

// TestAnthropicInputTokensExcludesCache 是计费正确性的关键。
//
// Anthropic 的 input_tokens 不含缓存部分，而 CC 的 inputTokens 是总数。
// 直接透传会让下游把两者当互补部分相加，得到约两倍的输入量。
func TestAnthropicInputTokensExcludesCache(t *testing.T) {
	u := &CCUsage{
		InputTokens:       1000,
		OutputTokens:      10,
		CachedInputTokens: 400,
		InputTokenDetails: &InputTokenDetail{NoCacheTokens: 600, CacheReadTokens: 400},
	}
	if got := anthropicInputTokens(u, nil); got != 600 {
		t.Errorf("应取 noCacheTokens=600，得到 %d", got)
	}

	// 老版本上游没有 details，退回减法。
	legacy := &CCUsage{InputTokens: 1000, OutputTokens: 10, CachedInputTokens: 400}
	if got := anthropicInputTokens(legacy, nil); got != 600 {
		t.Errorf("回退减法应得 600，得到 %d", got)
	}

	// 减法算出负数时夹到 0，不能把负的 token 数发给客户端。
	weird := &CCUsage{InputTokens: 100, OutputTokens: 10, CachedInputTokens: 500}
	if got := anthropicInputTokens(weird, nil); got != 0 {
		t.Errorf("负数应夹到 0，得到 %d", got)
	}
}

// TestBuildEnvelopeShape 校验信封的关键形态。
func TestBuildEnvelopeShape(t *testing.T) {
	req := &normalizedRequest{
		Model: "claude-sonnet-4-6",
		Messages: []map[string]any{
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "hello"},
		},
		Tools:  []map[string]any{},
		Stream: true,
	}

	env, err := buildEnvelope(req, "agent", DeviceProfileFor(""), "", true)
	if err != nil {
		t.Fatalf("buildEnvelope 失败: %v", err)
	}

	if env.Mode != "agent" {
		t.Errorf("mode 应为 agent，得到 %s", env.Mode)
	}
	if env.Config.Environment != "win32" {
		t.Errorf("environment 应与设备档案一致，得到 %s", env.Config.Environment)
	}
	if !env.Params.Stream {
		t.Error("CC API 总是流式，stream 必须为 true")
	}
	if len(env.Params.System) != 1 || env.Params.System[0].Text == nil || *env.Params.System[0].Text != "You are helpful." {
		t.Errorf("system 块抽取有误: %+v", env.Params.System)
	}
	// tools 必须非 nil：上游能区分「空数组」和「缺键」。
	if env.Params.Tools == nil {
		t.Error("tools 应为空数组而不是缺键")
	}
	if len(env.Params.Messages) != 1 {
		t.Fatalf("system 消息不应留在对话里，得到 %d 条", len(env.Params.Messages))
	}
	if env.Params.MaxTokens != defaultMaxTokens {
		t.Errorf("未指定 max_tokens 时应取默认 %d，得到 %d", defaultMaxTokens, env.Params.MaxTokens)
	}
}

// TestBuildEnvelopeEmptySystemPlaceholder 验证空 system 占位开关。
//
// 不带 system 时上游会注入它自带的约 7.5K token 默认提示词，既产生大量
// 缓存 token 又污染对话，所以默认要发一个空格占位绕过。
func TestBuildEnvelopeEmptySystemPlaceholder(t *testing.T) {
	req := &normalizedRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []map[string]any{{"role": "user", "content": "hi"}},
	}

	on, _ := buildEnvelope(req, "agent", DeviceProfileFor(""), "", true)
	if len(on.Params.System) != 1 || on.Params.System[0].Text == nil || *on.Params.System[0].Text != " " {
		t.Errorf("开启占位时应发一个空格，得到 %+v", on.Params.System)
	}

	off, _ := buildEnvelope(req, "agent", DeviceProfileFor(""), "", false)
	if len(off.Params.System) != 0 {
		t.Errorf("关闭占位时不应有 system，得到 %+v", off.Params.System)
	}
}

// TestBuildEnvelopeMaxTokensCap 验证输出上限被夹住。
func TestBuildEnvelopeMaxTokensCap(t *testing.T) {
	huge := 999999
	req := &normalizedRequest{
		Model:     "claude-sonnet-4-6",
		Messages:  []map[string]any{{"role": "user", "content": "hi"}},
		MaxTokens: &huge,
	}
	env, _ := buildEnvelope(req, "agent", DeviceProfileFor(""), "", true)
	if env.Params.MaxTokens != maxOutputTokensCap {
		t.Errorf("max_tokens 应被夹到 %d，得到 %d", maxOutputTokensCap, env.Params.MaxTokens)
	}
}

// TestBuildEnvelopeThreadIDOnlyUUID 验证 threadId 的 UUID 校验。
//
// 不是合法 UUID 时整个键必须省略——上游会校验格式。
func TestBuildEnvelopeThreadIDOnlyUUID(t *testing.T) {
	req := &normalizedRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []map[string]any{{"role": "user", "content": "hi"}},
	}

	valid, _ := buildEnvelope(req, "agent", DeviceProfileFor(""), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", true)
	if valid.ThreadID == "" {
		t.Error("合法 UUID 应写入 threadId")
	}

	invalid, _ := buildEnvelope(req, "agent", DeviceProfileFor(""), "not-a-uuid", true)
	if invalid.ThreadID != "" {
		t.Errorf("非法 UUID 不应写入 threadId，得到 %q", invalid.ThreadID)
	}
}

// TestEnvelopeKeyOrder 验证顶层键序与 CLI 一致。
//
// commandcode-proxy 特意做了键重排，说明上游那边键序是可观测的。
func TestEnvelopeKeyOrder(t *testing.T) {
	req := &normalizedRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []map[string]any{{"role": "user", "content": "hi"}},
	}
	env, _ := buildEnvelope(req, "agent", DeviceProfileFor(""), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", true)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("序列化信封失败: %v", err)
	}

	want := []string{"config", "memory", "taste", "skills", "permissionMode", "threadId", "mode", "params"}
	pos := 0
	for _, key := range want {
		idx := strings.Index(string(raw), `"`+key+`":`)
		if idx < 0 {
			t.Fatalf("信封缺少键 %q", key)
		}
		if idx < pos {
			t.Errorf("键 %q 的位置乱序（%d < %d）", key, idx, pos)
		}
		pos = idx
	}
}

// TestToolNameAliases 验证工具名重写。
func TestToolNameAliases(t *testing.T) {
	cases := map[string]string{
		"bash_output":         "shell_output",
		"task_output":         "shell_output",
		"tool_search":         "search_tools",
		"read_multiple_files": "read_file",
		"read_file":           "read_file",
		"my_custom_tool":      "my_custom_tool",
	}
	for in, want := range cases {
		if got := toWireToolName(in); got != want {
			t.Errorf("toWireToolName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestOpenAIEncoderStream 验证上游事件到 OpenAI 分片的翻译。
func TestOpenAIEncoderStream(t *testing.T) {
	enc := NewOpenAIStreamEncoder("claude-sonnet-4-6", "chatcmpl-test", 1700000000)

	// 无内容事件不应产生任何帧。
	for _, silent := range []string{"start", "text-start", "start-step", "provider-metadata", "text-end"} {
		if frames := enc.Encode(&CCEvent{Type: silent}); len(frames) != 0 {
			t.Errorf("事件 %s 不应产生分片，得到 %d 个", silent, len(frames))
		}
	}

	frames := enc.Encode(&CCEvent{Type: "text-delta", Text: "Hello"})
	if len(frames) != 1 {
		t.Fatalf("text-delta 应产生 1 个分片，得到 %d 个", len(frames))
	}
	if !strings.HasPrefix(frames[0], "data: ") || !strings.HasSuffix(frames[0], "\n\n") {
		t.Errorf("分片格式不对: %q", frames[0])
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(frames[0], "data: "), "\n\n")), &chunk); err != nil {
		t.Fatalf("分片不是合法 JSON: %v", err)
	}
	choices, _ := chunk["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices 长度应为 1，得到 %d", len(choices))
	}
	delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
	// 第一个可见分片必须带 role，否则客户端不知道这是谁的回复。
	if delta["role"] != "assistant" {
		t.Errorf("首个分片应带 role=assistant，得到 %v", delta["role"])
	}
	if delta["content"] != "Hello" {
		t.Errorf("content 应为 Hello，得到 %v", delta["content"])
	}

	// finish 事件应带 finish_reason 和用量。
	enc.Encode(&CCEvent{Type: "finish-step", FinishReason: "tool_use",
		Usage: &CCUsage{InputTokens: 100, OutputTokens: 20, CachedInputTokens: 30}})
	final := enc.Encode(&CCEvent{Type: "finish", FinishReason: "tool_use",
		TotalUsage: &CCUsage{InputTokens: 100, OutputTokens: 20, CachedInputTokens: 30}})
	if len(final) != 1 {
		t.Fatalf("finish 应产生 1 个分片，得到 %d 个", len(final))
	}
	var finalChunk map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(final[0], "data: "), "\n\n")), &finalChunk)
	finalChoices, _ := finalChunk["choices"].([]any)
	fr := finalChoices[0].(map[string]any)["finish_reason"]
	if fr != "tool_calls" {
		t.Errorf("finish_reason 应为 tool_calls，得到 %v", fr)
	}
	if _, ok := finalChunk["usage"]; !ok {
		t.Error("finish 分片应带 usage")
	}

	if truncated, _ := enc.Finish(); truncated {
		t.Error("见过 finish 之后不应判定为截断")
	}
}

// TestOpenAIEncoderDetectsTruncation 验证「上游一个完成信号都没给」会被识别。
//
// 把被截断的回答当完整回答返回，会让用户以为代码写完了。
func TestOpenAIEncoderDetectsTruncation(t *testing.T) {
	enc := NewOpenAIStreamEncoder("m", "id", 0)
	enc.Encode(&CCEvent{Type: "text-delta", Text: "half a sen"})
	if truncated, detail := enc.Finish(); !truncated {
		t.Errorf("没有 finish 事件时应判定为截断，detail=%q", detail)
	}

	// 只要出现过 finish-step 就不算截断——上游确实给过完成信号。
	withStep := NewOpenAIStreamEncoder("m", "id", 0)
	withStep.Encode(&CCEvent{Type: "finish-step", FinishReason: "stop"})
	if truncated, _ := withStep.Finish(); truncated {
		t.Error("出现过 finish-step 就不该判定为截断")
	}
}

// TestOpenAIEncoderToolCall 验证工具调用的分片形态。
func TestOpenAIEncoderToolCall(t *testing.T) {
	enc := NewOpenAIStreamEncoder("m", "id", 0)
	frames := enc.Encode(&CCEvent{
		Type:       "tool-call",
		ToolCallID: "call_1",
		ToolName:   "read_file",
		Input:      json.RawMessage(`{"path":"a.txt"}`),
	})
	if len(frames) != 1 {
		t.Fatalf("tool-call 应产生 1 个分片，得到 %d 个", len(frames))
	}
	var chunk map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(frames[0], "data: "), "\n\n")), &chunk)
	delta := chunk["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	calls, _ := delta["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("应有 1 个 tool_call，得到 %d", len(calls))
	}
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "read_file" {
		t.Errorf("工具名应为 read_file，得到 %v", fn["name"])
	}
	if fn["arguments"] != `{"path":"a.txt"}` {
		t.Errorf("参数应原样透传，得到 %v", fn["arguments"])
	}
}

// TestMapHTTPError 覆盖状态码映射，特别是 402 → 429 这条。
func TestMapHTTPError(t *testing.T) {
	cases := []struct {
		upstream int
		want     int
		wantType string
	}{
		{400, 400, "invalid_request_error"},
		{401, 401, "authentication_error"},
		// 欠费映射成限流：客户端应对方式是退避而不是重试同样的请求。
		{402, 429, "rate_limit_error"},
		{403, 401, "authentication_error"},
		{429, 429, "rate_limit_error"},
		{500, 502, "upstream_error"},
		{503, 503, "temporarily_unavailable"},
		// 未知状态码兜底成 502。
		{418, 502, "upstream_error"},
	}

	// 刻意用中性 body：带 USAGE_EXCEEDED 这类 code 时，映射会以 code 为准
	// 而不是状态码，那就不是在测状态码映射了。code 修正另有专门的测试。
	body := []byte(`{"success":false,"error":{"message":"something went wrong"}}`)
	for _, tc := range cases {
		got := MapHTTPError(tc.upstream, body)
		if got.Status != tc.want {
			t.Errorf("上游 %d 应映射为 %d，得到 %d", tc.upstream, tc.want, got.Status)
		}
		if got.Type != tc.wantType {
			t.Errorf("上游 %d 类型应为 %s，得到 %s", tc.upstream, tc.wantType, got.Type)
		}
	}
}

// TestModelNotInPlanIsNotAnAuthError 是实测踩坑后补的回归测试。
//
// 真实上游在「模型不在套餐内」时返回的是 HTTP 403 + code=FORBIDDEN，
// 消息形如 "MODEL_NOT_IN_PLAN: Claude Sonnet 4.6 available in Pro and above
// plans"。只按状态码映射会得到 authentication_error——客户端会以为是密钥
// 写错了，去查半天密钥，而真正该做的是换模型或升级套餐。
//
// 这里的响应体是从真实上游抓下来的原文，不要"简化"它。
func TestModelNotInPlanIsNotAnAuthError(t *testing.T) {
	raw := []byte(`{"success":false,"error":{"code":"FORBIDDEN","status":403,` +
		`"message":"MODEL_NOT_IN_PLAN: Claude Sonnet 4.6 available in Pro and above plans or extra on demand usage",` +
		`"docs":"https://commandcode.ai/docs/reference/errors/forbidden"}}`)

	got := MapHTTPError(403, raw)

	if got.Type == "authentication_error" {
		t.Error("模型不在套餐内不应被报成认证失败——那会把排查方向带偏")
	}
	if got.Status != 400 {
		t.Errorf("应映射为 400（请求层面的不可满足），得到 %d", got.Status)
	}
	// 消息必须原样透出：它写明了哪些套餐可用，是可直接行动的信息。
	if !strings.Contains(got.Message, "MODEL_NOT_IN_PLAN") {
		t.Errorf("应保留原始消息，得到 %q", got.Message)
	}
	if !strings.Contains(got.Message, "Pro and above") {
		t.Errorf("应保留套餐说明，得到 %q", got.Message)
	}
	// 不该带退避提示——等再久也不会变成可用。
	if got.RetryAfter != 0 {
		t.Errorf("不应带 retry_after，得到 %d", got.RetryAfter)
	}
}

// TestCodeOverridesStatus 验证上游给出的 code 优先于状态码。
func TestCodeOverridesStatus(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantType string
	}{
		{
			name:     "额度用尽",
			body:     `{"error":{"code":"USAGE_EXCEEDED","message":"quota exhausted"}}`,
			wantType: "rate_limit_error",
		},
		{
			name:     "被限流",
			body:     `{"error":{"code":"RATE_LIMITED","message":"slow down"}}`,
			wantType: "rate_limit_error",
		},
		{
			name:     "请求格式错",
			body:     `{"error":{"code":"BAD_REQUEST","message":"bad payload"}}`,
			wantType: "invalid_request_error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MapHTTPError(403, []byte(tc.body))
			if got.Type != tc.wantType {
				t.Errorf("code 应决定类型：期望 %s，得到 %s", tc.wantType, got.Type)
			}
		})
	}
}

// TestNoCodeFallsBackToStatus 验证没有 code 时仍按状态码映射。
//
// 上游并非每个错误都带 code，少了这条兜底会让错误分类整个失效。
func TestNoCodeFallsBackToStatus(t *testing.T) {
	got := MapHTTPError(429, []byte(`{"error":{"message":"too many requests"}}`))
	if got.Status != 429 || got.Type != "rate_limit_error" {
		t.Errorf("无 code 时应按状态码映射，得到 %+v", got)
	}
	if got.RetryAfter == 0 {
		t.Error("映射成限流就必须给退避提示，否则客户端会立刻重试")
	}
}

// TestMapEventErrorStatusCode 验证事件里的状态码没被丢掉。
//
// 丢掉 statusCode 会把 429/503 这类「该退避」的信号抹平成 502，
// 客户端不再按限流退避，监控也会错误归类成后端故障。
func TestMapEventErrorStatusCode(t *testing.T) {
	// 显式 statusCode 字段。
	got := MapEventError(&CCEvent{
		Type:  "error",
		Error: &CCError{Message: "slow down", StatusCode: 429},
	})
	if got.Status != 429 {
		t.Errorf("statusCode=429 应映射为 429，得到 %d", got.Status)
	}
	if !got.IsRateLimit() {
		t.Error("应被识别为限流类错误")
	}

	// 消息前缀形式。
	prefixed := MapEventError(&CCEvent{
		Type:  "error",
		Error: &CCError{Message: "<503> service unavailable"},
	})
	if prefixed.Status != 503 {
		t.Errorf("消息前缀 <503> 应映射为 503，得到 %d", prefixed.Status)
	}

	// 都没有时兜底 502。
	fallback := MapEventError(&CCEvent{Type: "error", Error: &CCError{Message: "boom"}})
	if fallback.Status != 502 {
		t.Errorf("无状态信息时应兜底 502，得到 %d", fallback.Status)
	}
}

// TestRetryable 验证「换账号重试」的判定。
//
// 认证类和限流类换账号有意义（可能只是这个号的问题）；
// 请求格式类换账号没意义，同一个请求发给谁都会被拒。
func TestRetryable(t *testing.T) {
	retryable := []int{401, 403, 402, 429, 500, 502, 503}
	for _, s := range retryable {
		if !MapHTTPError(s, nil).Retryable() {
			t.Errorf("上游 %d 应该可重试", s)
		}
	}
	if MapHTTPError(400, nil).Retryable() {
		t.Error("400 是请求格式问题，换账号重试没有意义")
	}
}
