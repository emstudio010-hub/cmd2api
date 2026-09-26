package relay

import "testing"

// TestParseCommandCodeCallbackAcceptsRealShapes 钉住「手动粘贴」能认的几种输入。
//
// 这条路的用户是**授权已经跑完、但浏览器跳不回来**的人：面板在远程服务器上，
// studio 让他们跳回 127.0.0.1 那条地址，浏览器打不开。密钥就在那条打不开的
// 地址里。认不出他粘的东西，这趟授权就白做了——而且 state 是一次性的，
// 没有第二次机会。
func TestParseCommandCodeCallbackAcceptsRealShapes(t *testing.T) {
	const (
		key   = "user_0123456789abcdef0123456789abcdef"
		state = "c3RhdGUtdG9rZW4tZm9ydHktYnl0ZXM"
	)

	cases := []struct {
		name      string
		raw       string
		wantKey   string
		wantState string
	}{
		{
			name:      "完整的 https 回调地址",
			raw:       "https://panel.example.com/api/accounts/oauth/commandcode/callback?apiKey=" + key + "&state=" + state + "&userId=u_1&userName=alice&keyName=cli",
			wantKey:   key,
			wantState: state,
		},
		{
			// 浏览器地址栏里复制出来的常常没有 scheme。这条是**当初写错就容易
			// 漏掉**的一种：url.Parse 会把 "127.0.0.1:8080" 当成 scheme，
			// host 变空、query 丢掉，于是密钥就找不到了。
			name:      "没带 scheme 的地址",
			raw:       "127.0.0.1:8080/api/accounts/oauth/commandcode/callback?apiKey=" + key + "&state=" + state,
			wantKey:   key,
			wantState: state,
		},
		{
			name:      "只有密钥，没有地址（很多人第一反应就是这个）",
			raw:       key,
			wantKey:   key,
			wantState: "",
		},
		{
			name:      "前后和行尾有空白、还带换行",
			raw:       "  https://p.example.com/cb?apiKey=" + key + "&state=" + state + "\r\n",
			wantKey:   key,
			wantState: state,
		},
		{
			// 地址后面挂 #fragment 不该把 query 带坏。
			name:      "带 fragment",
			raw:       "http://p.example.com/cb?apiKey=" + key + "&state=" + state + "#done",
			wantKey:   key,
			wantState: state,
		},
		{
			// 用户在站上点了拒绝：没有密钥，但有 error。state 照样要认出来——
			// 失败也要记到那次握手上，好让轮询的页面停下来。
			name:      "用户拒绝授权",
			raw:       "https://p.example.com/cb?error=access_denied&state=" + state,
			wantKey:   "",
			wantState: state,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCommandCodeCallback(tc.raw)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got.APIKey != tc.wantKey {
				t.Errorf("APIKey = %q，期望 %q", got.APIKey, tc.wantKey)
			}
			if got.State != tc.wantState {
				t.Errorf("State = %q，期望 %q", got.State, tc.wantState)
			}
		})
	}
}

// TestParseCommandCodeCallbackRejectsWrongThing 确认粘错了东西时报的是人话。
//
// 这里每一条都对应一种真实会发生的误操作。报错要是只说「解析失败」，用户
// 只会把同一个错的东西再粘一遍。
func TestParseCommandCodeCallbackRejectsWrongThing(t *testing.T) {
	// 授权页地址：query 里有 callback 和 state，看着很像回调地址，
	// 但它没有 apiKey——用户会以为是我们这边坏了。
	authorize := CommandCodeAuthURL("http://127.0.0.1:8080/api/accounts/oauth/commandcode/callback", "s", true)

	cases := []struct {
		name string
		raw  string
	}{
		{"空字符串", ""},
		{"只有空白", "   \n  "},
		{"授权页地址（最容易粘错的一个）", authorize},
		{"面板首页地址", "https://panel.example.com/accounts"},
		{"没有 apiKey 也没有 error 的地址", "https://panel.example.com/cb?state=abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCommandCodeCallback(tc.raw)
			if err == nil {
				t.Fatalf("本该报错，却解析出 %+v", got)
			}
			if err.Error() == "" {
				t.Error("报错信息是空的")
			}
		})
	}
}

// TestParseCommandCodeCallbackKeepsErrorText 确认 access_denied 之外的上游
// 报错原样带回来。
//
// 上游把原因写在 error_description 里，那是排错唯一的线索；吞掉它，
// 用户和我们看到的就只剩一句「授权失败」。
func TestParseCommandCodeCallbackKeepsErrorText(t *testing.T) {
	raw := "https://p.example.com/cb?error=server_error&error_description=" +
		"%E4%B8%8A%E6%B8%B8%E6%9A%82%E4%B8%8D%E5%8F%AF%E7%94%A8"

	got, err := ParseCommandCodeCallback(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.Error != "server_error" {
		t.Errorf("Error = %q", got.Error)
	}
	// query 是 studio 编码过的，这里必须解码，否则用户看到的是一串 %E4…
	if got.ErrorDescription != "上游暂不可用" {
		t.Errorf("ErrorDescription = %q，期望解码成中文", got.ErrorDescription)
	}
}
