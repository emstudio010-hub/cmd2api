package handler

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cmd2api/internal/config"
	"cmd2api/internal/service"

	"github.com/gin-gonic/gin"
)

func newOAuthTestHandler(cfg *config.Config) *Handler {
	return New(nil, nil, nil, nil, nil, nil, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newTestGinContext 把一个请求包成 gin 的上下文，用来单独调处理函数。
func newTestGinContext(req *http.Request) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

// TestOAuthStateStoreIsSingleUse 守住回调端点唯一的防线。
//
// state 会出现在浏览器的地址栏和历史记录里。能重放的话，捡到它的人就能拿
// 自己的密钥在这台机器上建出一个账号——之后池子里的流量会跑到别人的账号上。
func TestOAuthStateStoreIsSingleUse(t *testing.T) {
	store := newOAuthStateStore()
	store.put("s1", oauthPending{
		Fields:    service.CreateAccountInput{Name: "a"},
		ExpiresAt: time.Now().Add(time.Minute),
	})

	first, ok := store.take("s1")
	if !ok {
		t.Fatal("第一次取应当成功")
	}
	if first.Fields.Name != "a" {
		t.Errorf("取回来的字段不对: %q", first.Fields.Name)
	}
	if _, ok := store.take("s1"); ok {
		t.Fatal("同一个 state 被用了第二次")
	}
}

func TestOAuthStateStoreRejectsExpired(t *testing.T) {
	store := newOAuthStateStore()
	store.put("s1", oauthPending{ExpiresAt: time.Now().Add(-time.Second)})

	if _, ok := store.take("s1"); ok {
		t.Fatal("过期的握手不该被接受")
	}
}

func TestOAuthStateStoreRejectsEmptyAndUnknown(t *testing.T) {
	store := newOAuthStateStore()
	if _, ok := store.take(""); ok {
		t.Fatal("空 state 不该被接受")
	}
	if _, ok := store.take("nope"); ok {
		t.Fatal("没登记过的 state 不该被接受")
	}
}

// TestOAuthStateStoreSweepsExpired 确认过期记录会被清掉。
//
// 清理挂在写入路径上，如果哪天有人把它删了，这里就会开始无限涨。
func TestOAuthStateStoreSweepsExpired(t *testing.T) {
	store := newOAuthStateStore()
	store.put("old", oauthPending{ExpiresAt: time.Now().Add(-time.Minute)})
	store.put("new", oauthPending{ExpiresAt: time.Now().Add(time.Minute)})

	store.mu.Lock()
	_, stale := store.items["old"]
	size := len(store.items)
	store.mu.Unlock()

	if stale {
		t.Error("过期记录没有被清掉")
	}
	if size != 1 {
		t.Errorf("清理后应当只剩 1 条，实际 %d", size)
	}
}

func TestNewOAuthStateIsRandomAndURLSafe(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		state, err := newOAuthState()
		if err != nil {
			t.Fatalf("生成 state 失败: %v", err)
		}
		if len(state) < 32 {
			t.Fatalf("state 太短: %q", state)
		}
		if strings.ContainsAny(state, "+/= ") {
			t.Fatalf("state 里不该有需要转义的字符: %q", state)
		}
		if seen[state] {
			t.Fatalf("state 重复了: %q", state)
		}
		seen[state] = true
	}
}

// TestOAuthCallbackURLFromRequest 确认回调地址是跟着管理员当前访问的地址走的。
//
// 这是这套流程能在本机、SSH 隧道转发、反代域名三种情况下都不用配置的原因：
// 管理员访问面板用的地址，就是浏览器能连回这台机器的地址。
func TestOAuthCallbackURLFromRequest(t *testing.T) {
	h := newOAuthTestHandler(&config.Config{})

	cases := []struct {
		name    string
		host    string
		headers map[string]string
		want    string
	}{
		{
			name: "本机直连",
			host: "127.0.0.1:8080",
			want: "http://127.0.0.1:8080" + oauthCallbackPath,
		},
		{
			name: "反代终止了 TLS",
			host: "panel.example.com",
			headers: map[string]string{
				"X-Forwarded-Proto": "https",
			},
			want: "https://panel.example.com" + oauthCallbackPath,
		},
		{
			name: "代理链里带多个 proto",
			host: "panel.example.com",
			headers: map[string]string{
				"X-Forwarded-Proto": "https, http",
			},
			want: "https://panel.example.com" + oauthCallbackPath,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/accounts/oauth/commandcode", nil)
			req.Host = tc.host
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			c := newTestGinContext(req)

			if got := h.oauthCallbackURL(c); got != tc.want {
				t.Errorf("callback = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestOAuthCallbackURLOverride 确认 PUBLIC_BASE_URL 能盖过推导。
func TestOAuthCallbackURLOverride(t *testing.T) {
	h := newOAuthTestHandler(&config.Config{
		Server: config.ServerConfig{PublicBaseURL: "https://pool.example.com/"},
	})

	req := httptest.NewRequest("POST", "/api/accounts/oauth/commandcode", nil)
	req.Host = "127.0.0.1:8080"
	c := newTestGinContext(req)

	// 结尾多写的斜杠要吃掉，否则会拼出 //api/...
	want := "https://pool.example.com" + oauthCallbackPath
	if got := h.oauthCallbackURL(c); got != want {
		t.Errorf("callback = %q，期望 %q", got, want)
	}
}

// TestOAuthCallbackURLRejectsBadHost 确认畸形 Host 会被挡掉。
//
// 这个值会被拼进一个交给第三方网站的 URL，换行、空格之类的东西混进去
// 会构造出别的东西来。
func TestOAuthCallbackURLRejectsBadHost(t *testing.T) {
	h := newOAuthTestHandler(&config.Config{})

	for _, host := range []string{"", "bad host", "evil.com/../x", "a\nb"} {
		req := httptest.NewRequest("POST", "/api/accounts/oauth/commandcode", nil)
		req.Host = host
		c := newTestGinContext(req)
		if got := h.oauthCallbackURL(c); got != "" {
			t.Errorf("Host %q 应当被拒绝，实际拼出 %q", host, got)
		}
	}
}
