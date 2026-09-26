package relay

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestCommandCodeAuthURL 钉住授权页地址的形状。
//
// 这套参数名是从官方 CLI 包里还原出来的（buildCommandAuthUrl），
// 拼错一个参数名不会报错，只会让浏览器停在一个白页上——所以对着
// CLI 的实际拼法写死断言。
func TestCommandCodeAuthURL(t *testing.T) {
	callback := "https://panel.example.com/api/accounts/oauth/commandcode/callback"
	raw := CommandCodeAuthURL(callback, "state-token")

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("生成的地址解析不了: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "commandcode.ai" {
		t.Fatalf("授权页应当落在 commandcode.ai，实际 %s://%s", parsed.Scheme, parsed.Host)
	}
	if parsed.Path != "/studio/auth/cli" {
		t.Fatalf("路径不对: %s", parsed.Path)
	}

	q := parsed.Query()
	if got := q.Get("callback"); got != callback {
		t.Errorf("callback = %q，期望 %q", got, callback)
	}
	if got := q.Get("state"); got != "state-token" {
		t.Errorf("state = %q", got)
	}
	// 少了 mode=redirect，studio 会走 POST JSON 那条老路径，
	// 结果就是浏览器停在授权页上不回来。
	if got := q.Get("mode"); got != "redirect" {
		t.Errorf("mode = %q，期望 redirect", got)
	}
}

// TestCommandCodeAuthURLEscapesCallback 确认回调地址被完整编码。
//
// 不编码的话地址里的 & 会把参数切断，studio 收到的是一个残缺的 callback。
func TestCommandCodeAuthURLEscapesCallback(t *testing.T) {
	callback := "http://127.0.0.1:8080/api/accounts/oauth/commandcode/callback?a=1&b=2"
	raw := CommandCodeAuthURL(callback, "s")

	// 整串里只应出现一个未编码的 "&b=2" 之外的 &，即参数分隔符本身。
	if strings.Count(raw, "callback=") != 1 {
		t.Fatalf("callback 参数出现了多次: %s", raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := parsed.Query().Get("callback"); got != callback {
		t.Fatalf("解码回来的 callback = %q，期望 %q", got, callback)
	}
}

// TestFetchWhoamiParsesRealShape 对着 CLI 实际解析的字段写。
//
// CLI 里 validateCommandApiKey 拿的就是这个响应，并且按
// `whoami.user.userName` / `whoami.user.name` 取字段。
func TestFetchWhoamiParsesRealShape(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/whoami" {
			t.Errorf("路径不对: %s", r.URL.Path)
		}
		// 鉴权必须原样带上：少一个头就是 401，而这个错误会被当成
		// 「密钥无效」显示给用户，排查起来会跑偏。
		if got := r.Header.Get("Authorization"); got != "Bearer user_test" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"user":{"id":"u_1","userName":"alice","name":"Alice","email":"a@example.com"}}`))
	})

	whoami, err := client.FetchWhoami(context.Background(), "user_test")
	if err != nil {
		t.Fatalf("FetchWhoami 失败: %v", err)
	}
	if whoami.UserName != "alice" {
		t.Errorf("UserName = %q，期望 alice", whoami.UserName)
	}
	if whoami.Name != "Alice" {
		t.Errorf("Name = %q，期望 Alice", whoami.Name)
	}
}

// TestFetchWhoamiWithoutUserIsError 守住「200 但没有账号」这种情况。
//
// 上游 200 却不带 user，说明这把密钥没对应到任何账号。放它过去就会建出
// 一个永远用不了的账号，而且列表上看着一切正常。
func TestFetchWhoamiWithoutUserIsError(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"user":null}`))
	})

	if _, err := client.FetchWhoami(context.Background(), "user_test"); err == nil {
		t.Fatal("没有 user 时应当报错")
	}
}

// TestFetchWhoamiInvalidKeyIsError 确认坏密钥会变成错误，而不是一个空身份。
func TestFetchWhoamiInvalidKeyIsError(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	})

	_, err := client.FetchWhoami(context.Background(), "user_bad")
	if err == nil {
		t.Fatal("401 时应当报错")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("错误信息里应当带上上游状态码，实际: %v", err)
	}
}
