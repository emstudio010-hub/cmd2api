package relay

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cmd2api/internal/config"
	"cmd2api/internal/domain"
	"cmd2api/internal/scheduler"
)

// 下面两段是 2026-09-26 用真实 Command Code 账号实测抓到的原始响应，
// 原样保留（只把账号身份相关的字段去掉）。对着真响应写测试，
// 而不是对着我以为的响应写——余额解析错一个字段名就会安静地显示成 0。
const realCreditsBody = `{
  "credits": {
    "belowThreshold": false,
    "creditThreshold": 0,
    "monthlyCredits": 8.6880479396,
    "purchasedCredits": 0,
    "freeCredits": 0
  },
  "windowLimits": {
    "limited": true,
    "exceeded": null,
    "fiveHour": {"used": 0.02495826, "cap": 3, "exceeded": false, "resetAt": 1790436416506},
    "weekly": {"used": 1.3119520604, "cap": 6, "exceeded": false, "resetAt": 1790858538049}
  },
  "sandboxAccess": false,
  "sandboxMinutes": null
}`

const realSubscriptionBody = `{
  "success": true,
  "data": {
    "id": "sub_test",
    "status": "active",
    "orgId": null,
    "currentPeriodStart": "2026-09-23T10:34:59.000Z",
    "currentPeriodEnd": "2026-10-23T10:34:59.000Z",
    "cancelAtPeriodEnd": false,
    "planId": "individual-go"
  }
}`

// newBalanceTestClient 起一个假上游，按 path 分派。
func newBalanceTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := NewClient(
		config.CommandCodeConfig{BaseURL: srv.URL},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	return client, srv
}

// TestCommandCodeBalanceParsing 用真实抓包验解析。
func TestCommandCodeBalanceParsing(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/alpha/billing/credits":
			_, _ = w.Write([]byte(realCreditsBody))
		case "/alpha/billing/subscriptions":
			_, _ = w.Write([]byte(realSubscriptionBody))
		default:
			// 路径写错要立刻炸，不能静默地返回空 JSON。
			t.Errorf("请求了预期之外的路径: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result := client.FetchBalance(context.Background(), &scheduler.Target{
		Platform: domain.PlatformCommandCode,
		APIKey:   "user_test",
	})
	if !result.Supported {
		t.Fatal("Command Code 应当支持查余额")
	}
	if result.Err != nil {
		t.Fatalf("不该出错: %v", result.Err)
	}

	b := result.Balance
	// 三笔额度都为 0 时，剩余就是 monthlyCredits 本身。
	if b.Remaining != 8.6880479396 {
		t.Errorf("Remaining = %v，期望 8.6880479396", b.Remaining)
	}
	// 套餐只从 subscriptions 拿得到——credits 那个响应里根本没有 planId。
	if b.PlanID != "individual-go" {
		t.Errorf("PlanID = %q，期望 individual-go", b.PlanID)
	}
	if b.PlanName != "Go" {
		t.Errorf("PlanName = %q，期望 Go", b.PlanName)
	}

	if b.FiveHour == nil {
		t.Fatal("应当解析出 5 小时窗口")
	}
	if b.FiveHour.Used != 0.02495826 || b.FiveHour.Cap != 3 {
		t.Errorf("5h 窗口 = %+v，期望 used=0.02495826 cap=3", b.FiveHour)
	}
	// resetAt 是**毫秒**时间戳。当成秒解析会得到 1970 年附近的时间，
	// 界面上显示成一个荒唐的日期，而且不会报任何错。
	if b.FiveHour.ResetAt == nil {
		t.Fatal("5h 窗口应当有重置时间")
	}
	wantReset := time.UnixMilli(1790436416506)
	if !b.FiveHour.ResetAt.Equal(wantReset) {
		t.Errorf("5h ResetAt = %v，期望 %v", b.FiveHour.ResetAt, wantReset)
	}
	if b.FiveHour.ResetAt.Year() < 2020 {
		t.Errorf("5h ResetAt 落到了 %v —— resetAt 很可能被当成了秒", b.FiveHour.ResetAt)
	}

	if b.Weekly == nil {
		t.Fatal("应当解析出周窗口")
	}
	if b.Weekly.Used != 1.3119520604 || b.Weekly.Cap != 6 {
		t.Errorf("周窗口 = %+v，期望 used=1.3119520604 cap=6", b.Weekly)
	}

	if b.PeriodEnd == nil {
		t.Fatal("应当解析出计费周期结束时间")
	}
	if got := b.PeriodEnd.UTC().Format(time.RFC3339); got != "2026-10-23T10:34:59Z" {
		t.Errorf("PeriodEnd = %s，期望 2026-10-23T10:34:59Z", got)
	}

	// 内置套餐表：Go 是 $10/月。这条对不上说明表抄错了。
	if total, ok := PlanCredits("individual-go"); !ok || total != 10 {
		t.Errorf("PlanCredits(individual-go) = %v, %v，期望 10, true", total, ok)
	}
	// 而且剩余 + 本周已用应当正好等于总额，这验证了 monthlyCredits
	// 确实是「剩余」而不是「总额」——当初就是靠这个等式下的结论。
	if sum := b.Remaining + b.Weekly.Used; sum != 10 {
		t.Errorf("剩余 %v + 周已用 %v = %v，期望正好等于套餐总额 10", b.Remaining, b.Weekly.Used, sum)
	}
}

// TestCommandCodeBalanceSumsExtraCredits 验证额外购买和赠送的额度也算进余额。
//
// 只报套餐剩余会让买过额度的人看到一个偏小的数字，然后以为钱丢了。
func TestCommandCodeBalanceSumsExtraCredits(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/alpha/billing/credits" {
			_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":5,"purchasedCredits":20,"freeCredits":1.5}}`))
			return
		}
		_, _ = w.Write([]byte(realSubscriptionBody))
	})

	result := client.FetchBalance(context.Background(), &scheduler.Target{
		Platform: domain.PlatformCommandCode, APIKey: "user_test",
	})
	if result.Err != nil {
		t.Fatalf("不该出错: %v", result.Err)
	}
	if result.Balance.Remaining != 26.5 {
		t.Errorf("Remaining = %v，期望 5+20+1.5=26.5", result.Balance.Remaining)
	}
}

// TestCommandCodeBalanceSubscriptionFailureIsNotFatal 验证套餐拉不到不算整次失败。
//
// 剩余额度才是这个功能的主产物。为了一个展示用的套餐名把整次查询判成失败，
// 是拿次要目标否决主要目标。
func TestCommandCodeBalanceSubscriptionFailureIsNotFatal(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/alpha/billing/subscriptions" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realCreditsBody))
	})

	result := client.FetchBalance(context.Background(), &scheduler.Target{
		Platform: domain.PlatformCommandCode, APIKey: "user_test",
	})
	if result.Err != nil {
		t.Fatalf("套餐接口失败不该让整次查询失败，却报了: %v", result.Err)
	}
	if result.Balance.Remaining != 8.6880479396 {
		t.Errorf("剩余额度应当照常返回，得到 %v", result.Balance.Remaining)
	}
	// 套餐拿不到就是空的，界面据此不显示套餐名和百分比。
	if result.Balance.PlanID != "" {
		t.Errorf("套餐接口失败时 PlanID 应为空，得到 %q", result.Balance.PlanID)
	}
}

// TestCommandCodeBalanceCreditsFailureIsError 验证主接口失败要如实上报。
func TestCommandCodeBalanceCreditsFailureIsError(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	})

	result := client.FetchBalance(context.Background(), &scheduler.Target{
		Platform: domain.PlatformCommandCode, APIKey: "user_bad",
	})
	if result.Err == nil {
		t.Fatal("credits 接口 401 应当报错")
	}
	if !result.Supported {
		t.Error("报错时 Supported 仍应为 true —— 是「查失败」不是「不支持」")
	}
	// 状态码要带进错误信息，否则排障时看不出是鉴权问题还是网络问题。
	if !strings.Contains(result.Err.Error(), "401") {
		t.Errorf("错误信息应当带上上游状态码，得到: %v", result.Err)
	}
}

// TestOpenCodeBalanceUnsupported 验证 OpenCode 被如实标成「不支持」而不是报错。
//
// 报错会让界面显示一条红色的「余额获取失败」，管理员会去查一个并不存在的故障。
func TestOpenCodeBalanceUnsupported(t *testing.T) {
	client, _ := newBalanceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("OpenCode 不该发任何余额请求，却请求了 %s", r.URL.Path)
	})

	result := client.FetchBalance(context.Background(), &scheduler.Target{
		Platform: domain.PlatformOpenCode, APIKey: "sk-test",
	})
	if result.Supported {
		t.Error("OpenCode 目前应当标记为不支持查余额")
	}
	if result.Err != nil {
		t.Errorf("「不支持」不是错误，不该带 Err: %v", result.Err)
	}
	if result.Balance != nil {
		t.Error("不支持时不该返回余额")
	}
}

// TestPlanCreditsUnknownPlan 验证不认识的套餐不会瞎猜一个总额。
func TestPlanCreditsUnknownPlan(t *testing.T) {
	if _, ok := PlanCredits("individual-does-not-exist"); ok {
		t.Error("不认识的套餐应当返回 ok=false，而不是某个默认值")
	}
	if name := PlanName("individual-does-not-exist"); name != "" {
		t.Errorf("不认识的套餐展示名应为空，得到 %q", name)
	}
}

// TestSnippetForErrorTruncatesOnRuneBoundary 验证错误摘要不会切碎多字节字符。
//
// 按字节截断会把一个汉字切成两半，存进 Postgres 的 text 列直接报编码错。
func TestSnippetForErrorTruncatesOnRuneBoundary(t *testing.T) {
	long := ""
	for i := 0; i < 500; i++ {
		long += "错"
	}
	got := snippetForError([]byte(long))
	if len([]rune(got)) != 201 { // 200 个字符 + 省略号
		t.Errorf("截断后应当是 201 个字符，得到 %d", len([]rune(got)))
	}
	for i, r := range got {
		if r == '�' {
			t.Fatalf("第 %d 个字符是替换符，说明切碎了多字节字符", i)
		}
	}
}
