package relay

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestUpstreamStatusErrorPrefersUpstreamMessage 守住「别把一段 JSON 甩给用户」。
//
// 这是实测出来的问题：拿一把无效密钥去验，界面上显示的是
//
//	上游返回 401：{"success":false,"error":{"code":"UNAUTHORIZED","status":401,
//	 "message":"Invalid 'Authorization' header or token…
//
// 被截断的半截 JSON。上游明明写了一句人话在 message 里，取出来就行。
func TestUpstreamStatusErrorPrefersUpstreamMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "Command Code /alpha 的嵌套形状",
			body: `{"success":false,"error":{"code":"UNAUTHORIZED","status":401,` +
				`"message":"Invalid 'Authorization' header or token"}}`,
			want: "Invalid 'Authorization' header or token",
		},
		{
			name: "扁平的 message",
			body: `{"message":"rate limit exceeded"}`,
			want: "rate limit exceeded",
		},
		{
			name: "error 直接是字符串",
			body: `{"error":"something went wrong"}`,
			want: "something went wrong",
		},
		{
			name: "中文照样取出来",
			body: `{"error":{"message":"密钥已过期"}}`,
			want: "密钥已过期",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newUpstreamStatusError(http.StatusUnauthorized, []byte(tc.body))
			if err.Message != tc.want {
				t.Fatalf("Message = %q，期望 %q", err.Message, tc.want)
			}
			if strings.Contains(err.Error(), tc.body) {
				t.Errorf("错误信息里不该出现整段响应体: %s", err.Error())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息里应当包含上游原话，实际 %q", err.Error())
			}
		})
	}
}

// TestUpstreamStatusErrorFallsBackToRawBody 确认抠不出话时仍有东西可看。
//
// 上游回了 502 和一个 HTML 错误页时，界面上显示的至少要是响应体开头，
// 而不是一句无从下手的「未知错误」。
func TestUpstreamStatusErrorFallsBackToRawBody(t *testing.T) {
	err := newUpstreamStatusError(http.StatusBadGateway, []byte("<html>Bad Gateway</html>"))

	if err.Message != "" {
		t.Errorf("这不是 JSON，不该抠出 Message，实际 %q", err.Message)
	}
	if !strings.Contains(err.Error(), "Bad Gateway") {
		t.Errorf("退回原始响应体时也该带上内容，实际 %q", err.Error())
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("状态码要留着，实际 %q", err.Error())
	}
}

// TestUpstreamStatusErrorIsAuthFailure 守住 401/403 跟 5xx 的分界。
//
// 分不清的后果是措辞全错：上游 500 时告诉用户「密钥不对」，他会去换一把
// 好密钥；密钥真不对时又让人一直重试。
func TestUpstreamStatusErrorIsAuthFailure(t *testing.T) {
	auth := []int{http.StatusUnauthorized, http.StatusForbidden}
	for _, status := range auth {
		if !newUpstreamStatusError(status, nil).IsAuthFailure() {
			t.Errorf("%d 应当算认证失败", status)
		}
	}
	notAuth := []int{
		http.StatusBadRequest,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	}
	for _, status := range notAuth {
		if newUpstreamStatusError(status, nil).IsAuthFailure() {
			t.Errorf("%d 不该算认证失败", status)
		}
	}
}

// TestDescribeKeyCheckError 钉住用户最终看到的那几句话。
//
// 这一层存在的理由就是「handler 不该去解析上游响应体」，所以它必须把每种
// 情况都说成人话：密钥不对、上游坏了、压根没连上——三件事用户该做的动作
// 完全不同。
func TestDescribeKeyCheckError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantSub string
		// 不该出现的词。措辞错了比不说还糟。
		notSub string
	}{
		{
			name:    "密钥被上游拒绝",
			err:     newUpstreamStatusError(http.StatusUnauthorized, []byte(`{"error":{"message":"Invalid token"}}`)),
			wantSub: "上游不认这把密钥",
		},
		{
			name:    "密钥被拒但上游没给原因",
			err:     newUpstreamStatusError(http.StatusForbidden, []byte(`{}`)),
			wantSub: "上游不认这把密钥",
		},
		{
			name:    "上游自己 500",
			err:     newUpstreamStatusError(http.StatusInternalServerError, nil),
			wantSub: "稍后再试",
			// 上游的毛病不能说成用户的密钥有问题。
			notSub: "不认这把密钥",
		},
		{
			name:    "连不上上游",
			err:     errors.New("请求上游失败: dial tcp: connection refused"),
			wantSub: "没能连上上游",
			notSub:  "不认这把密钥",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribeKeyCheckError(tc.err)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("得到 %q，应当包含 %q", got, tc.wantSub)
			}
			if tc.notSub != "" && strings.Contains(got, tc.notSub) {
				t.Errorf("得到 %q，不该包含 %q", got, tc.notSub)
			}
			// 任何情况下都不能把整段响应体带出去。
			if strings.Contains(got, `{"`) {
				t.Errorf("提示里出现了 JSON: %q", got)
			}
		})
	}
}

// TestDescribeKeyCheckErrorHandlesNil 确认没有错误时不编出一句话。
//
// 调用方是 `errors.New(DescribeKeyCheckError(err))` 这种写法，返回空串会
// 造出一个空错误——比 panic 更难查，因为它看着像成功了。
func TestDescribeKeyCheckErrorHandlesNil(t *testing.T) {
	if got := DescribeKeyCheckError(nil); got != "" {
		t.Errorf("nil 应当返回空串，实际 %q", got)
	}
}

// TestDescribeKeyCheckErrorTruncatesRuneSafely 确认截断不切开多字节字符。
//
// 一个很长的中文错误信息被按字节截断会产生非法 UTF-8，前端拿到之后要么
// 显示成乱码，要么直接抛异常。
func TestDescribeKeyCheckErrorTruncatesRuneSafely(t *testing.T) {
	long := ""
	for i := 0; i < 300; i++ {
		long += "错"
	}
	got := DescribeKeyCheckError(errors.New(long))

	if !strings.Contains(got, "没能连上上游") {
		t.Fatalf("措辞不对: %q", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatal("截断把字符切坏了，出现了替换字符")
		}
	}
}
