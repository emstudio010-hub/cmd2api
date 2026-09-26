package relay

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"cmd2api/internal/config"
)

// cliSessionIDPattern 是 CLI generateSessionId 的形状：
// `sess_` + UUID 去掉横线后的前 16 位十六进制。
var cliSessionIDPattern = regexp.MustCompile(`^sess_[0-9a-f]{16}$`)

// TestNewSessionIDMatchesCLIFormat 守住会话 ID 的形状。
//
// 这个值出现在每一个请求的 x-session-id 头上。原来我们发的是一个带横线的
// 裸 UUID，而 CLI 发的是 sess_ 前缀那种——形状差异比版本号旧显眼得多，
// 而且是每条请求都带一遍。
func TestNewSessionIDMatchesCLIFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := newSessionID()
		if !cliSessionIDPattern.MatchString(id) {
			t.Fatalf("会话 ID 形状不对: %q（期望 sess_ 加 16 位十六进制）", id)
		}
		if seen[id] {
			t.Fatalf("会话 ID 重复了: %q", id)
		}
		seen[id] = true
	}
}

// TestSessionForIsStablePerKey 确认同一个 key 在一段时间内复用同一个会话。
//
// 每次请求换个会话是「这不是真 CLI」最直白的信号之一：真机上会话是活的，
// 不会每次调用都重新开始。
func TestSessionForIsStablePerKey(t *testing.T) {
	client := NewClient(
		config.CommandCodeConfig{BaseURL: "http://127.0.0.1:1"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	first := client.sessionFor("user_key_a")
	if !cliSessionIDPattern.MatchString(first) {
		t.Fatalf("会话 ID 形状不对: %q", first)
	}
	for i := 0; i < 5; i++ {
		if got := client.sessionFor("user_key_a"); got != first {
			t.Fatalf("同一把密钥的会话变了: %q → %q", first, got)
		}
	}
	// 不同账号必须是不同会话：共用一个会话等于告诉上游「这些账号是一台机器」。
	if other := client.sessionFor("user_key_b"); other == first {
		t.Fatalf("不同密钥共用了一个会话: %q", other)
	}
}

// TestSessionIDsAgreeAcrossRequests 守住「同一账号全程只有一个会话 ID」。
//
// 两个预请求的头、生命周期事件里的 sessionId、以及生成请求的头，必须都是
// 同一个。原来生命周期事件里是现场新随机一个，于是每 8 小时就凭空多出一个
// 从没在别处出现过的会话。
func TestSessionIDsAgreeAcrossRequests(t *testing.T) {
	type captured struct {
		headerSession string
		bodySession   string
	}
	got := map[string]*captured{}

	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := got[r.URL.Path]
		if c == nil {
			c = &captured{}
			got[r.URL.Path] = c
		}
		c.headerSession = r.Header.Get("x-session-id")

		if r.URL.Path == "/alpha/lifecycle-events" {
			var event struct {
				EventType string `json:"eventType"`
				Metadata  struct {
					SessionID  string `json:"sessionId"`
					CliVersion string `json:"cliVersion"`
					Mode       string `json:"mode"`
					OS         string `json:"os"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Errorf("生命周期事件体解析失败: %v (%s)", err, raw)
			}
			if event.EventType != "cli_session_exists" {
				t.Errorf("事件类型 = %q", event.EventType)
			}
			if event.Metadata.CliVersion != protocolVersion {
				t.Errorf("事件里的版本 = %q，期望 %q", event.Metadata.CliVersion, protocolVersion)
			}
			if event.Metadata.Mode != "interactive" {
				t.Errorf("mode = %q", event.Metadata.Mode)
			}
			if event.Metadata.OS == "" || !strings.Contains(event.Metadata.OS, "-") {
				t.Errorf("os 应当是 platform-arch，实际 %q", event.Metadata.OS)
			}
			c.bodySession = event.Metadata.SessionID
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	client.EnsureInitialized(context.Background(), "user_wire_shape")

	fp, ok := got["/alpha/fingerprint/record"]
	if !ok {
		t.Fatal("没有发出指纹上报")
	}
	lc, ok := got["/alpha/lifecycle-events"]
	if !ok {
		t.Fatal("没有发出生命周期事件")
	}

	for _, c := range []*captured{fp, lc} {
		if !cliSessionIDPattern.MatchString(c.headerSession) {
			t.Errorf("请求头里的会话 ID 形状不对: %q", c.headerSession)
		}
	}
	if lc.bodySession != lc.headerSession {
		t.Errorf("生命周期事件的会话(%q)与请求头(%q)不一致", lc.bodySession, lc.headerSession)
	}
	if fp.headerSession != lc.headerSession {
		t.Errorf("两个预请求用了不同会话: %q vs %q", fp.headerSession, lc.headerSession)
	}
}

// TestUpstreamHeadersMatchCLIShape 守住每次生成请求都带齐 CLI 的头。
//
// 少一个头比多一个值更容易被挑出来：真机发出来的组合是固定的，
// 缺项意味着「这不是那个客户端」。
func TestUpstreamHeadersMatchCLIShape(t *testing.T) {
	var seen http.Header
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	resp, err := client.Generate(context.Background(), "user_key", nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_ = resp.Body.Close()

	want := map[string]string{
		"User-Agent":             "cli",
		"X-Command-Code-Version": protocolVersion,
		"X-Cli-Environment":      "production",
		"X-Taste-Learning":       "false",
		"Authorization":          "Bearer user_key",
		"Content-Type":           "application/json",
	}
	for name, value := range want {
		if got := seen.Get(name); got != value {
			t.Errorf("%s = %q，期望 %q", name, got, value)
		}
	}
	if !cliSessionIDPattern.MatchString(seen.Get("X-Session-Id")) {
		t.Errorf("x-session-id = %q，形状不对", seen.Get("X-Session-Id"))
	}
	if seen.Get("X-Project-Slug") == "" {
		t.Error("缺少 x-project-slug")
	}
	// traceparent: 00-<32hex>-<16hex>-01
	tp := seen.Get("Traceparent")
	if !regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`).MatchString(tp) {
		t.Errorf("traceparent = %q，形状不对", tp)
	}
}

// TestModelsRequestCarriesSessionID 守住 provider 端点上的会话 ID。
//
// CLI 从 1.56.1 起给「需要它的 BYOK 主机」带上 x-session-id，/provider/ 就是
// 这类端点。这条路径只在拉模型列表时走，漏掉不容易被发现。
func TestModelsRequestCarriesSessionID(t *testing.T) {
	got := http.Header{}
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.Header {
			got[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	client.fetchModels(context.Background(), "user_models")

	session := got.Get("X-Session-Id")
	if !cliSessionIDPattern.MatchString(session) {
		t.Errorf("拉模型列表时 x-session-id = %q，形状不对或缺失", session)
	}
	if v := got.Get("X-Command-Code-Version"); v != protocolVersion {
		t.Errorf("拉模型列表时版本头 = %q，期望 %q", v, protocolVersion)
	}
	// 必须和这个账号其它请求用同一个会话。
	client.mu.Lock()
	expected := client.state["user_models"].sessionID
	client.mu.Unlock()
	if session != expected {
		t.Errorf("拉模型列表的会话 %q 与账号会话 %q 不一致", session, expected)
	}
}

// TestProtocolVersionIsSemver 守住版本号本身的形状。
//
// 上游拿它做兼容分支，写成一个不像版本号的东西（比如空串、日期）
// 会让它走进意料之外的分支。
func TestProtocolVersionIsSemver(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(protocolVersion) {
		t.Fatalf("protocolVersion = %q，不是 x.y.z", protocolVersion)
	}
}

// TestInitRequestsShareSessionWithGenerate 确认预请求和生成请求不各说各话。
func TestInitRequestsShareSessionWithGenerate(t *testing.T) {
	// 必须加锁：EnsureInitialized 是把指纹和生命周期事件**并发**发出去的
	// （两个 goroutine），这个 map 会被同时写。不加锁是一个真正的数据竞争，
	// Go 运行时抓到时直接 fatal error: concurrent map writes —— 而且是偶发的，
	// 十次里过一次，最难查的那一类。
	var mu sync.Mutex
	sessions := map[string]string{}
	record := func(path, session string) {
		mu.Lock()
		sessions[path] = session
		mu.Unlock()
	}
	sessionOf := func(path string) string {
		mu.Lock()
		defer mu.Unlock()
		return sessions[path]
	}

	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		record(r.URL.Path, r.Header.Get("x-session-id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	client.EnsureInitialized(context.Background(), "user_shared_session")
	resp, err := client.Generate(context.Background(), "user_shared_session", nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_ = resp.Body.Close()

	gen := sessionOf("/alpha/generate")
	if gen == "" {
		t.Fatal("生成请求没有带会话")
	}
	for _, path := range []string{"/alpha/fingerprint/record", "/alpha/lifecycle-events"} {
		if got := sessionOf(path); got != gen {
			t.Errorf("%s 的会话 %q 与生成请求的 %q 不一致", path, got, gen)
		}
	}

	// 会话要有寿命，不能无限长——真机上会话会结束。
	client.mu.Lock()
	st := client.state["user_shared_session"]
	client.mu.Unlock()
	if st == nil || st.sessionExpiresAt.IsZero() {
		t.Fatal("会话没有设置过期时间")
	}
	if !st.sessionExpiresAt.After(time.Now()) {
		t.Error("会话一建出来就是过期的")
	}
}
