package scheduler

import (
	"sync"
)

// limiter 是每个上游账号的并发闸门。
//
// 之所以放在内存里而不是 Redis：cmd2api 按单实例部署，进程内的计数就够了，
// 少一个必须一起运维的组件。代价是没法横向扩容——真要扩多实例时，
// 这里换成 Redis 信号量即可，接口不用动。
type limiter struct {
	mu    sync.Mutex
	slots map[int64]*slot
}

// slot 是单个账号的带缓冲 channel，容量等于该账号的并发上限。
type slot struct {
	ch chan struct{}
}

func newLimiter() *limiter {
	return &limiter{slots: make(map[int64]*slot)}
}

// tryAcquire 尝试占用一个名额。返回 false 表示该账号当前已满。
//
// capacity 由调用方从账号行读出来传入；如果和当前容量不一致（管理员改了
// 并发设置），会重建 channel——正在排队的请求会随之失败，这是可接受的，
// 因为改配置本来就该让新配置生效。
func (l *limiter) tryAcquire(accountID int64, capacity int) bool {
	if capacity <= 0 {
		// 并发上限为 0 视为不允许调度，而不是「无限」。
		// 把 0 当作无限是常见的踩坑点，这里显式拒绝，让管理员意识到配置有问题。
		return false
	}

	l.mu.Lock()
	s, ok := l.slots[accountID]
	if !ok || cap(s.ch) != capacity {
		s = &slot{ch: make(chan struct{}, capacity)}
		l.slots[accountID] = s
	}
	l.mu.Unlock()

	select {
	case s.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// release 归还一个名额。
func (l *limiter) release(accountID int64) {
	l.mu.Lock()
	s, ok := l.slots[accountID]
	l.mu.Unlock()
	if !ok {
		return
	}
	select {
	case <-s.ch:
	default:
		// 已经空了，说明 release 被多调了一次。静默忽略而不是 panic：
		// 宁可少一个名额也不要让整个服务因为一次重复归还而挂掉。
	}
}

// forget 在账号被删除时清掉它的闸门，避免 map 随账号数无限增长。
func (l *limiter) forget(accountID int64) {
	l.mu.Lock()
	delete(l.slots, accountID)
	l.mu.Unlock()
}
