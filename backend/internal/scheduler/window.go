package scheduler

import (
	"time"

	"cmd2api/ent"
)

// windowSnapshotTTL 是余额快照的保鲜期。
//
// 探活每 10 分钟顺带刷一次余额，这里给三个周期。快照过期就不作数——
// 余额这条链路自己坏掉的时候，拿一份旧快照去跳过账号，会让账号一直不被使用，
// 而真正的根因（拉不到余额）反而被这个副作用盖住了。
const windowSnapshotTTL = 30 * time.Minute

// windowExhausted 判断账号是否撞上了滚动额度窗口。
//
// 上游的 5 小时 / 周窗口是硬限制，打满之后请求会被直接拒。余额快照里本来
// 就有这个信息，那就没必要非等上游拒一次再换账号——提前绕开省掉一次失败重试，
// 对已经打满的账号也少一次无谓的请求。
//
// 只认「已经打满」这一种情况。快照里没有「快打满了」的预警阈值（比如 98%）：
// 那不是上游给的信号，是我们自己编的，而编出来的阈值会让账号在其实还能用的时候
// 被绕开——那种偏差比偶尔被上游拒一次更难查。
func windowExhausted(acc *ent.Account, now time.Time) bool {
	if !snapshotIsFresh(acc.BalanceFetchedAt, now) {
		return false
	}
	return windowFull(acc.Balance5hUsed, acc.Balance5hCap, acc.Balance5hResetAt, now) ||
		windowFull(acc.BalanceWeeklyUsed, acc.BalanceWeeklyCap, acc.BalanceWeeklyResetAt, now)
}

// snapshotIsFresh 判断余额快照是否还值得信。
//
// fetchedAt 为 nil 表示从没刷成功过——这种情况不能当成「窗口满了」，
// 只能当成「不知道」。
func snapshotIsFresh(fetchedAt *time.Time, now time.Time) bool {
	if fetchedAt == nil {
		return false
	}
	// 时间在未来说明主机时钟被往回调过。当成新鲜处理：这种情况下
	// Sub 是负数，本来就不会超过 TTL。
	return now.Sub(*fetchedAt) <= windowSnapshotTTL
}

// windowFull 判断单个窗口是否已满且尚未重置。
//
// used / cap 任一为空都表示「不知道」，一律当作没满——拿不知道当满，
// 会让一个从没查过余额的账号莫名其妙不被使用。
func windowFull(used, capacity *float64, resetAt *time.Time, now time.Time) bool {
	if used == nil || capacity == nil || *capacity <= 0 || *used < *capacity {
		return false
	}
	// 重置时刻已经过去：快照是旧的，但这个窗口确实翻篇了，账号可以再用。
	// 界面上显示百分比时也依赖同一套判断，不要在这里单独放松。
	if resetAt != nil && !resetAt.After(now) {
		return false
	}
	// resetAt 为空表示不知道什么时候重置。按「还没重置」处理，
	// 等下一次探活把快照刷掉——探活失败时快照会自然过期，不会永久卡住。
	return true
}

// splitByWindow 把候选账号分成「窗口还有余量」和「已经打满」两拨。
//
// 两拨内部的先后顺序原样保留（优先级 → 最久未使用），所以调用方只要按
// preferred、exhausted 的顺序去试，就等于「先挑能用的，实在没有再用打满的」。
func splitByWindow(accounts []*ent.Account, now time.Time) (preferred, exhausted []*ent.Account) {
	for _, acc := range accounts {
		if windowExhausted(acc, now) {
			exhausted = append(exhausted, acc)
			continue
		}
		preferred = append(preferred, acc)
	}
	return preferred, exhausted
}
