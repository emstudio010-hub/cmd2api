package scheduler

import (
	"testing"
	"time"

	"cmd2api/ent"
)

func f(v float64) *float64 { return &v }

// accountWithWindows 造一个带余额快照的账号。nil 表示该字段未知。
func accountWithWindows(
	fetchedAt *time.Time,
	used, capacity *float64, resetAt *time.Time,
) *ent.Account {
	return &ent.Account{
		ID:               1,
		BalanceFetchedAt: fetchedAt,
		Balance5hUsed:    used,
		Balance5hCap:     capacity,
		Balance5hResetAt: resetAt,
	}
}

func TestWindowExhausted(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-5 * time.Minute)
	stale := now.Add(-windowSnapshotTTL - time.Minute)
	future := now.Add(2 * time.Hour)
	past := now.Add(-time.Minute)

	cases := []struct {
		name string
		acc  *ent.Account
		want bool
	}{
		{
			name: "打满且还没重置",
			acc:  accountWithWindows(&fresh, f(10), f(10), &future),
			want: true,
		},
		{
			name: "超过额度（上游可能给小数）",
			acc:  accountWithWindows(&fresh, f(10.4), f(10), &future),
			want: true,
		},
		{
			name: "正好等于额度",
			acc:  accountWithWindows(&fresh, f(10), f(10), &future),
			want: true,
		},
		{
			name: "重置时刻已过，可以再用",
			acc:  accountWithWindows(&fresh, f(10), f(10), &past),
			want: false,
		},
		{
			name: "重置时刻恰好是现在",
			acc:  accountWithWindows(&fresh, f(10), f(10), &now),
			want: false,
		},
		{
			name: "还有余量",
			acc:  accountWithWindows(&fresh, f(9.99), f(10), &future),
			want: false,
		},
		{
			name: "快照太旧就不作数",
			acc:  accountWithWindows(&stale, f(10), f(10), &future),
			want: false,
		},
		{
			name: "从没刷成功过",
			acc:  accountWithWindows(nil, f(10), f(10), &future),
			want: false,
		},
		{
			name: "打满但不知道何时重置",
			acc:  accountWithWindows(&fresh, f(10), f(10), nil),
			want: true,
		},
		{
			name: "额度未知",
			acc:  accountWithWindows(&fresh, f(10), nil, &future),
			want: false,
		},
		{
			name: "用量未知",
			acc:  accountWithWindows(&fresh, nil, f(10), &future),
			want: false,
		},
		{
			name: "额度为 0 不能算满",
			acc:  accountWithWindows(&fresh, f(0), f(0), &future),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowExhausted(tc.acc, now); got != tc.want {
				t.Errorf("windowExhausted = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestWindowExhaustedCoversWeekly 确认周窗口也参与判断。
//
// 只盯 5 小时窗口的话，周额度打满的账号会被一直选用，然后一直被上游拒。
func TestWindowExhaustedCoversWeekly(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	future := now.Add(48 * time.Hour)

	acc := &ent.Account{
		ID:                   1,
		BalanceFetchedAt:     &fresh,
		Balance5hUsed:        f(1),
		Balance5hCap:         f(10),
		Balance5hResetAt:     &future,
		BalanceWeeklyUsed:    f(200),
		BalanceWeeklyCap:     f(200),
		BalanceWeeklyResetAt: &future,
	}
	if !windowExhausted(acc, now) {
		t.Error("周窗口打满时应当判定为已满")
	}

	// 5 小时窗口单独看是没满的。
	if windowFull(acc.Balance5hUsed, acc.Balance5hCap, acc.Balance5hResetAt, now) {
		t.Error("5 小时窗口不该被判满")
	}
}

// TestSnapshotFutureTimestampIsFresh 守住时钟被往回调过的情况。
//
// fetchedAt 落在未来时 now.Sub 是负数，不能因为「差值不是正数」就当成过期，
// 那会让所有账号在同一次时钟回拨后集体失去窗口保护。
func TestSnapshotFutureTimestampIsFresh(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	skewed := now.Add(10 * time.Minute)
	future := now.Add(2 * time.Hour)

	acc := accountWithWindows(&skewed, f(10), f(10), &future)
	if !windowExhausted(acc, now) {
		t.Error("fetchedAt 在未来时应当仍按新鲜处理")
	}
}

// TestSnapshotTTLBoundary 钉住保鲜期的边界是「含」还是「不含」。
func TestSnapshotTTLBoundary(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour)

	atBoundary := now.Add(-windowSnapshotTTL)
	if !windowExhausted(accountWithWindows(&atBoundary, f(10), f(10), &future), now) {
		t.Error("正好到保鲜期应当还算数")
	}

	justOver := now.Add(-windowSnapshotTTL - time.Second)
	if windowExhausted(accountWithWindows(&justOver, f(10), f(10), &future), now) {
		t.Error("超过保鲜期就不该算数")
	}
}

// TestSplitByWindowPreservesOrder 确认分拨时两拨内部都保持原顺序。
//
// 顺序就是优先级和「最久未使用」，打乱了等于把调度策略改掉。
func TestSplitByWindowPreservesOrder(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	future := now.Add(2 * time.Hour)

	mk := func(id int64, exhausted bool) *ent.Account {
		acc := &ent.Account{ID: id, BalanceFetchedAt: &fresh}
		if exhausted {
			acc.Balance5hUsed = f(10)
			acc.Balance5hCap = f(10)
			acc.Balance5hResetAt = &future
		} else {
			acc.Balance5hUsed = f(1)
			acc.Balance5hCap = f(10)
			acc.Balance5hResetAt = &future
		}
		return acc
	}

	// 1 满 / 2 可用 / 3 满 / 4 可用
	accounts := []*ent.Account{mk(1, true), mk(2, false), mk(3, true), mk(4, false)}
	preferred, exhausted := splitByWindow(accounts, now)

	if len(preferred) != 2 || len(exhausted) != 2 {
		t.Fatalf("分拨结果不对: preferred=%d exhausted=%d", len(preferred), len(exhausted))
	}
	if preferred[0].ID != 2 || preferred[1].ID != 4 {
		t.Errorf("可用那一拨顺序错了: %d, %d", preferred[0].ID, preferred[1].ID)
	}
	if exhausted[0].ID != 1 || exhausted[1].ID != 3 {
		t.Errorf("打满那一拨顺序错了: %d, %d", exhausted[0].ID, exhausted[1].ID)
	}
}

// TestSplitByWindowKeepsExhaustedAsFallback 确认打满的账号不会被丢掉。
//
// 全池都打满时直接返回「没有可用账号」是错的：上游的拒绝才是准确答复，
// 而且我们的快照可能已经过时。
func TestSplitByWindowKeepsExhaustedAsFallback(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	future := now.Add(2 * time.Hour)

	all := []*ent.Account{
		{
			ID: 1, BalanceFetchedAt: &fresh,
			Balance5hUsed: f(10), Balance5hCap: f(10), Balance5hResetAt: &future,
		},
		{
			ID: 2, BalanceFetchedAt: &fresh,
			BalanceWeeklyUsed: f(50), BalanceWeeklyCap: f(50), BalanceWeeklyResetAt: &future,
		},
	}

	preferred, exhausted := splitByWindow(all, now)
	if len(preferred) != 0 {
		t.Errorf("这一池不该有可用账号，实际 %d 个", len(preferred))
	}
	if len(exhausted) != 2 {
		t.Fatalf("打满的账号必须留作兜底，实际 %d 个", len(exhausted))
	}
}
