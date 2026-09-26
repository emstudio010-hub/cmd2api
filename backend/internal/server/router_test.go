package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cmd2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// newTestRouter 用一堆 nil 依赖把路由树建起来。
//
// 建树本身就是被测对象：gin 在注册阶段就会为冲突的路由 panic（同一层的
// 静态段和参数段撞车），这种错误编译期看不出来，只有进程启动时才炸。
// 账号那一组里 /accounts/batch、/accounts/:id/check 和
// /accounts/oauth/commandcode 正好是这种形状，值得有一条测试盯着。
func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// 依赖全空：这里只测路由注册，不测业务。
	h := handler.New(nil, nil, nil, nil, nil, nil, nil, logger)
	return NewRouter(Options{Handler: h, Mode: "release"})
}

// TestNewRouterRegistersOAuthRoutes 确认这一组路由都在，而且方法对得上。
//
// 顺带盯住 gin 的建树：静态段（oauth）和参数段（:id）在 /accounts/ 这一层
// 是兄弟节点，注册冲突时 gin 会 panic。这种错编译期看不出来，只有进程启动
// 时才炸，所以值得有一条测试把整棵树建起来。
func TestNewRouterRegistersOAuthRoutes(t *testing.T) {
	r := newTestRouter(t)

	want := []struct{ method, path string }{
		{http.MethodPost, "/api/accounts/oauth/commandcode"},
		{http.MethodGet, "/api/accounts/oauth/commandcode/status"},
		{http.MethodPost, "/api/accounts/oauth/commandcode/complete"},
		{http.MethodPut, "/api/auth/profile"},
		// 回调要收 POST——studio 现在就是用 POST 递结果的；GET 只为兼容
		// 早先发出去的旧链接。两条路径、两种方法，四个组合都得在。
		{http.MethodGet, handler.OAuthCallbackPath},
		{http.MethodPost, handler.OAuthCallbackPath},
		{http.MethodGet, handler.OAuthLegacyCallbackPath},
		{http.MethodPost, handler.OAuthLegacyCallbackPath},
	}
	registered := make(map[string]bool)
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, w := range want {
		if !registered[w.method+" "+w.path] {
			t.Errorf("缺少路由 %s %s", w.method, w.path)
		}
	}
}

// TestOAuthStatusAndCompleteRequireAuth 确认「问好了没」和「手动收尾」
// 都在鉴权之内。
//
// 回调那条路必须在鉴权之外（浏览器跳转带不了头），这两条不用——它们是
// 面板自己发的 XHR。把它们也放到鉴权之外，等于开出一个可以用别人的 state
// 去建账号的口子：state 会出现在浏览器地址栏和历史记录里，抢到它的人就能
// 拿自己的密钥在这台机器上建号。
func TestOAuthStatusAndCompleteRequireAuth(t *testing.T) {
	r := newTestRouter(t)

	reqs := []*http.Request{
		httptest.NewRequest(http.MethodGet,
			"/api/accounts/oauth/commandcode/status?state=whatever", nil),
		httptest.NewRequest(http.MethodPost,
			"/api/accounts/oauth/commandcode/complete",
			strings.NewReader(`{"result":"user_x"}`)),
	}
	for _, req := range reqs {
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 应当要求登录，实际 %d", req.Method, req.URL.Path, w.Code)
		}
	}
}

// TestUpdateEmailRequiresAuth 确认改登录用户名要登录。
//
// 这条改的是登录入口本身，不是某个业务数据。放到鉴权之外等于谁都能把
// 管理员邮箱改成自己的。
func TestUpdateEmailRequiresAuth(t *testing.T) {
	r := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/api/auth/profile",
		strings.NewReader(`{"current_password":"x","email":"a@b.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", w.Code)
	}
}

// TestOAuthCallbackIsNotBehindAdminAuth 守的是这条流程能跑通的前提。
//
// 回调是 studio 让浏览器做的顶层跳转，浏览器不会给它带 Authorization 头。
// 这条路由一旦被挪进 admin 组，授权会 100% 失败，而且现象是「跳回来就报
// 未登录」，很难联想到是路由分组的问题。
func TestOAuthCallbackIsNotBehindAdminAuth(t *testing.T) {
	r := newTestRouter(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		ctype  string
	}{
		{
			name:   "studio 现在的方式：表单 POST 到 /callback",
			method: http.MethodPost,
			path:   handler.OAuthCallbackPath,
			body:   "apiKey=user_secret&state=whatever&userId=u1&userName=a&keyName=k",
			ctype:  "application/x-www-form-urlencoded",
		},
		{
			name:   "不带 mode 时 fetch 发的 JSON",
			method: http.MethodPost,
			path:   handler.OAuthCallbackPath,
			body:   `{"apiKey":"user_secret","state":"whatever"}`,
			ctype:  "application/json",
		},
		{
			name:   "旧链接里的长路径",
			method: http.MethodGet,
			path:   handler.OAuthLegacyCallbackPath + "?state=whatever&apiKey=user_secret",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			if tc.ctype != "" {
				req.Header.Set("Content-Type", tc.ctype)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code == http.StatusUnauthorized {
				t.Fatal("回调被 AdminAuth 挡住了，studio 的请求永远不会带令牌")
			}
			if w.Code == http.StatusNotFound {
				t.Fatal("回调路由没注册到这条路径上")
			}
			// 账号服务没配（测试里是 nil），所以这里走到的是「服务未启用」这条
			// 分支，但关键结论一样：请求进得来，是被当成一次真实回调处理的。
			if w.Code != http.StatusSeeOther {
				t.Fatalf("期望 303 跳回前端，实际 %d", w.Code)
			}

			location := w.Header().Get("Location")
			if !strings.HasPrefix(location, "/accounts?") {
				t.Fatalf("跳转目标应指回账号页，实际 %q", location)
			}
			// 这条是关键：303 会留在浏览器历史里，密钥绝不能出现在这个地址上。
			if strings.Contains(location, "user_secret") {
				t.Fatalf("密钥出现在了跳转地址里：%q", location)
			}
		})
	}
}

// TestOAuthCallbackRejectsUnknownState 确认回调不认来路不明的 state。
//
// 回调端点是公开的，state 是它唯一的凭证。不校验就等于任何人都能往这台
// 机器上建账号。
func TestOAuthCallbackRejectsUnknownState(t *testing.T) {
	r := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/accounts/oauth/commandcode/callback?state=forged&apiKey=user_x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("期望 303，实际 %d", w.Code)
	}
	location := w.Header().Get("Location")
	if !strings.Contains(location, "oauth=error") {
		t.Fatalf("未知 state 应当被拒绝，实际跳转 %q", location)
	}
}
