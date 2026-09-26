package handler

import (
	"testing"
	"time"

	"cmd2api/internal/service"
)

// TestOAuthOutcomeSurvivesTake 守住「另一个标签页问好了没」这条链路。
//
// 顺序是关键：授权在**另一个标签页**里完成，回调先 take 掉握手（一次性，
// 不能改），然后才写结果。发起授权的那个页面稍后才来问。如果结果和握手
// 存在同一张表里、一起被 take 删掉，那个页面就永远等不到答案，只能一直
// 转到超时——用户看到的是「授权成功了但面板一直转圈」。
func TestOAuthOutcomeSurvivesTake(t *testing.T) {
	store := newOAuthStateStore()
	store.put("s1", oauthPending{
		Fields:    service.CreateAccountInput{Name: "a"},
		ExpiresAt: time.Now().Add(time.Minute),
	})

	if _, ok := store.take("s1"); !ok {
		t.Fatal("第一次取应当成功")
	}
	store.finish("s1", oauthOutcome{OK: true, AccountID: 7, Name: "a"})

	out, ok := store.outcome("s1")
	if !ok {
		t.Fatal("take 之后结果必须仍然读得到")
	}
	if !out.OK || out.AccountID != 7 || out.Name != "a" {
		t.Errorf("结果不对: %+v", out)
	}

	// 轮询会反复读，不能像 take 那样读一次就没了。
	if _, ok := store.outcome("s1"); !ok {
		t.Error("第二次读也该拿得到：轮询就是要反复问")
	}
}

// TestOAuthPendingPeekDoesNotConsume 守住轮询的前提。
//
// 状态查询用的是 pending（只看一眼），不是 take。要是查询走了 take，
// 第一次轮询就会把回调要用的那份表单字段吃掉——账号确实会建出来，
// 但名称、分组、优先级全丢，变成「授权成功却建出一个没名字没分组的账号」。
func TestOAuthPendingPeekDoesNotConsume(t *testing.T) {
	store := newOAuthStateStore()
	store.put("s1", oauthPending{
		Fields:    service.CreateAccountInput{Name: "a", GroupIDs: []int64{3}},
		ExpiresAt: time.Now().Add(time.Minute),
	})

	for i := 0; i < 3; i++ {
		if !store.pending("s1") {
			t.Fatalf("第 %d 次查询就说握手不在了", i+1)
		}
	}

	pending, ok := store.take("s1")
	if !ok {
		t.Fatal("查询之后仍然应当能取到")
	}
	if pending.Fields.Name != "a" || len(pending.Fields.GroupIDs) != 1 {
		t.Errorf("字段被啃掉了: %+v", pending.Fields)
	}
}

// TestOAuthPendingReportsExpiry 确认过期握手被如实报告。
//
// 前端拿 "gone" 才会停止轮询并提示重新发起；一直回 "pending" 就是让人
// 对着一个永远不动的转圈等下去。
func TestOAuthPendingReportsExpiry(t *testing.T) {
	store := newOAuthStateStore()
	store.put("old", oauthPending{ExpiresAt: time.Now().Add(-time.Second)})

	if store.pending("old") {
		t.Error("已过期的握手不该报还在等")
	}
	if _, ok := store.take("old"); ok {
		t.Error("已过期的握手不该能取到")
	}
	if store.pending("never-existed") {
		t.Error("不存在的 state 不该报还在等")
	}
	if store.pending("") {
		t.Error("空 state 不该报还在等")
	}
}

// TestOAuthOutcomeExpires 确认结果不会无限攒着。
//
// 每点一次授权就留一条记录，只写不删的话，一个开着面板跑几周的实例会慢慢
// 攒下一堆没人再看的握手结果。
func TestOAuthOutcomeExpires(t *testing.T) {
	store := newOAuthStateStore()
	// 直接写一条「很久以前」的结果。
	store.mu.Lock()
	store.outcomes["stale"] = oauthOutcome{OK: true, At: time.Now().Add(-2 * oauthOutcomeTTL)}
	store.mu.Unlock()

	if _, ok := store.outcome("stale"); ok {
		t.Error("过期结果不该再读得到")
	}

	// 写入路径上顺带清理：put 之后那张表里不该还剩着过期的。
	store.put("s1", oauthPending{ExpiresAt: time.Now().Add(time.Minute)})
	store.mu.Lock()
	_, still := store.outcomes["stale"]
	store.mu.Unlock()
	if still {
		t.Error("put 应当顺手清掉过期的结果")
	}
}

// TestOAuthFailRecordsOutcome 确认失败也会被记下来。
//
// 只记成功的话，用户在站上点了「拒绝」，另一个标签页就会一直转圈——
// 明明已经有答案了，只是那个答案是「不要」。
func TestOAuthFailRecordsOutcome(t *testing.T) {
	store := newOAuthStateStore()
	store.put("s1", oauthPending{ExpiresAt: time.Now().Add(time.Minute)})
	store.finish("s1", oauthOutcome{Message: "已取消授权"})

	out, ok := store.outcome("s1")
	if !ok {
		t.Fatal("失败结果也该记下来")
	}
	if out.OK {
		t.Error("这条不该是成功")
	}
	if out.Message != "已取消授权" {
		t.Errorf("Message = %q", out.Message)
	}
}

// TestFinishWithoutStateIsNoop 确认没有 state 时不会写进一张 "" 键上。
//
// 手动粘贴那条路允许不带 state（字段从请求体来）。要是让它以 "" 为键写结果，
// 下一个同样不带 state 的请求会读到**上一个人**的结果——把别人的成功当
// 自己的答案。
func TestFinishWithoutStateIsNoop(t *testing.T) {
	store := newOAuthStateStore()
	store.finish("", oauthOutcome{OK: true, AccountID: 9})

	if _, ok := store.outcome(""); ok {
		t.Error("空 state 不该存下结果")
	}
	store.mu.Lock()
	n := len(store.outcomes)
	store.mu.Unlock()
	if n != 0 {
		t.Errorf("空 state 不该在表里留下东西，现在有 %d 条", n)
	}
}

// TestCallbackAfterManualCompleteReportsSuccess 守住两条收尾路径的相处方式。
//
// 场景：浏览器跳不回本机，用户把回调地址粘回来（手动那条路）把账号建好了；
// 紧接着原来那个标签页又好巧不巧地跳了回来，正好落在回调端点上。这时回调里的
// take 会失败——握手已经被手动那条路取走了。
//
// 如果直接报「授权链接已失效」，用户就会在一个标签页里看到失败、在另一个里
// 看到成功，不知道该信哪个，多半会再建一个账号。回调必须如实报成功。
//
// 这里直接构造最后那一瞬间的状态：握手没了，但结果记着成功。
func TestCallbackAfterManualCompleteReportsSuccess(t *testing.T) {
	store := newOAuthStateStore()
	store.put("s1", oauthPending{ExpiresAt: time.Now().Add(time.Minute)})

	// 手动那条路：取走握手、建号、记结果。
	if _, ok := store.take("s1"); !ok {
		t.Fatal("手动收尾应当能取到握手")
	}
	store.finish("s1", oauthOutcome{OK: true, AccountID: 42, Name: "手动建的账号"})

	// 回调那条路随后到达：take 必然失败，但结果在。
	if _, ok := store.take("s1"); ok {
		t.Fatal("握手已经用过了，不该还能取到")
	}
	out, ok := store.outcome("s1")
	if !ok || !out.OK {
		t.Fatalf("结果应当记着成功，实际 %+v ok=%v", out, ok)
	}
	if out.AccountID != 42 || out.Name != "手动建的账号" {
		t.Errorf("结果不对: %+v", out)
	}
}

// TestCallbackAfterManualFailureReportsFailure 确认失败也会如实传递。
//
// 手动那条路失败（比如密钥不对）时，另一个标签页不该还在转圈等。
func TestCallbackAfterManualFailureReportsFailure(t *testing.T) {
	store := newOAuthStateStore()
	store.put("s1", oauthPending{ExpiresAt: time.Now().Add(time.Minute)})
	store.take("s1")
	store.finish("s1", oauthOutcome{Message: "上游不认这把密钥"})

	out, ok := store.outcome("s1")
	if !ok {
		t.Fatal("失败结果也该记下来")
	}
	if out.OK {
		t.Error("这条不该是成功")
	}
	// 关键：失败的结果不能被当成「成功、id 是 0」发出去。
	if out.AccountID != 0 {
		t.Errorf("失败时不该有账号 id，实际 %d", out.AccountID)
	}
	if out.Message != "上游不认这把密钥" {
		t.Errorf("Message = %q", out.Message)
	}
}
