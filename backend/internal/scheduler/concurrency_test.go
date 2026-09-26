package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"cmd2api/ent"
	"cmd2api/internal/domain"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"
)

// testDBHelp 是给跳过时看的一段话。
//
// 刻意写全：postgres 在 compose 里**故意不对外暴露端口**（见 docker-compose.yml
// 的注释），所以「起个库然后从宿主机 go test」这条路是走不通的——写一段跑不了
// 的说明比不写还糟。下面这套命令是实际验证过的。
const testDBHelp = `未设置 CMD2API_TEST_DATABASE_URL，跳过需要真实数据库的测试。

  这些测试针对的是读-改-写丢更新，只有并发事务真的跑起来才暴露得出来，
  用假的 ent 客户端测不出来。要跑它们：

    # 1. 建一个一次性测试库（别指向生产库）
    docker compose exec -T postgres createdb -U cmd2api cmd2api_test

    # 2. postgres 没有对外映射端口，所以把测试跑在 compose 网络里
    docker build --target backend -t cmd2api-be-test .
    docker run --rm --network cmd2api_cmd2api \
      -e CMD2API_TEST_DATABASE_URL="host=postgres port=5432 user=cmd2api password=<.env 里的 DB_PASSWORD> dbname=cmd2api_test sslmode=disable" \
      cmd2api-be-test sh -c "cd /src/backend && go test ./internal/scheduler/ -v"

  测试每次会清空目标库的 accounts 表，所以务必用单独建的库。`

// testClient 连一个真实的 PostgreSQL，用来测**只在并发下才暴露**的行为。
//
// 这个文件里唯一的一条测试针对的是读-改-写丢更新，那件事用假的 ent 客户端
// 测不出来——丢更新恰恰是「两个事务真的同时跑」才发生的。所以这里不装作能
// 单元测试：没有数据库就跳过，并明确说清楚怎么把它跑起来。
//
// 用的库名固定带 _test 后缀，而且开跑前会把表清空。别把它指向生产库。
func testClient(t *testing.T) *ent.Client {
	t.Helper()

	dsn := os.Getenv("CMD2API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip(testDBHelp)
	}

	drv, err := entsql.Open(dialect.Postgres, dsn)
	if err != nil {
		t.Fatalf("连接测试数据库: %v", err)
	}
	client := ent.NewClient(ent.Driver(drv))

	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("迁移表结构: %v", err)
	}
	// 每次从干净的表开始，免得上一轮的账号让计数对不上。
	if _, err := client.Account.Delete().Exec(ctx); err != nil {
		t.Fatalf("清空 accounts: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func newTestScheduler(t *testing.T, client *ent.Client, threshold int) *Scheduler {
	t.Helper()
	return New(client, nil, threshold,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestMarkFailureCountsEveryFailureUnderConcurrency 守住自动禁用这条兜底。
//
// 这条测试是有来历的：MarkFailure 原来是「查出来 +1 再写回去」，跟它所在
// 那一节开头写的「不做读-改-写」自相矛盾。并发请求同时为同一个账号记失败时，
// 两个都读到 N、都写回 N+1，一次失败就这么丢了——consecutive_failures 于是
// 永远涨不到阈值，**账号该被自动停掉却一直留着**。
//
// 而这条路径是自动的、没人盯着：坏账号继续留在池子里，请求一直往上面撞。
//
// 所以断言必须精确到计数本身，不能只断言「最后被禁用了」——阈值调低一点，
// 丢了几次更新照样能凑够。
func TestMarkFailureCountsEveryFailureUnderConcurrency(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()

	// 阈值设得很高，确保这一轮不会触发禁用——这里要单独验计数。
	const threshold = 10000
	sched := newTestScheduler(t, client, threshold)

	acc, err := client.Account.Create().
		SetName("并发失败计数").
		SetPlatform(domain.PlatformCommandCode).
		SetStatus(domain.StatusActive).
		Save(ctx)
	if err != nil {
		t.Fatalf("建账号: %v", err)
	}

	const n = 60
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // 尽量让它们真正撞在一起
			sched.MarkFailure(ctx, acc.ID, errors.New("上游 500"))
		}()
	}
	close(start)
	wg.Wait()

	got, err := client.Account.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("读回账号: %v", err)
	}
	// 读-改-写那版会明显小于 n，具体少多少看调度时机——所以断言相等而不是
	// 断言「大于某个数」：少一次也是丢更新。
	if got.ConsecutiveFailures != n {
		t.Fatalf("并发记了 %d 次失败，计数却是 %d —— 丢更新了。"+
			"（读-改-写在高并发下会互相覆盖）", n, got.ConsecutiveFailures)
	}
	// 没到阈值，不该被禁用。
	if got.Status != domain.StatusActive || !got.Schedulable {
		t.Errorf("还没到阈值就被禁用了: status=%s schedulable=%v", got.Status, got.Schedulable)
	}
}

// TestMarkFailureDisablesExactlyOnceAtThreshold 守住「越过阈值」只发生一次。
//
// 判断和写入在同一条 UPDATE 里、条件带 status != error，所以并发失败涌进来时
// 只有一次能把账号翻成禁用。这条不光是省日志：多条路径同时禁用会让返回的
// 「本次是否触发禁用」变得不可信，而调用方是按它决定要不要告警的。
func TestMarkFailureDisablesExactlyOnceAtThreshold(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()

	const threshold = 5
	sched := newTestScheduler(t, client, threshold)

	acc, err := client.Account.Create().
		SetName("阈值禁用").
		SetPlatform(domain.PlatformCommandCode).
		SetStatus(domain.StatusActive).
		Save(ctx)
	if err != nil {
		t.Fatalf("建账号: %v", err)
	}

	// 20 个并发失败，阈值 5：计数必须精确到 20，禁用只该报一次。
	const n = 20
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		disabled int
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if sched.MarkFailure(ctx, acc.ID, errors.New("上游 500")) {
				mu.Lock()
				disabled++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	got, err := client.Account.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("读回账号: %v", err)
	}
	if got.ConsecutiveFailures != n {
		t.Errorf("计数应当是 %d，实际 %d", n, got.ConsecutiveFailures)
	}
	if got.Status != domain.StatusError {
		t.Errorf("越过阈值后应当被禁用，实际 status=%s", got.Status)
	}
	if got.Schedulable {
		t.Error("被禁用的账号不该还参与调度")
	}
	if got.ErrorMessage == nil || *got.ErrorMessage == "" {
		t.Error("禁用原因应当写进 error_message，管理员要靠它判断为什么停了")
	}
	if disabled != 1 {
		t.Errorf("「本次触发禁用」应当只有一次，实际 %d 次", disabled)
	}
}

// TestMarkUsedResetsFailureCount 守住恢复的语义。
//
// 成功一次就该把连续失败清零，否则账号会在「偶尔抽风」之后一路攒到阈值被
// 停掉——中间明明成功过。
func TestMarkUsedResetsFailureCount(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()

	sched := newTestScheduler(t, client, 5)
	acc, err := client.Account.Create().
		SetName("恢复清零").
		SetPlatform(domain.PlatformCommandCode).
		SetConsecutiveFailures(3).
		SetStatus(domain.StatusActive).
		Save(ctx)
	if err != nil {
		t.Fatalf("建账号: %v", err)
	}

	sched.MarkUsed(ctx, acc.ID)

	got, err := client.Account.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("读回账号: %v", err)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("成功一次后应当清零，实际 %d", got.ConsecutiveFailures)
	}
	if got.LastUsedAt == nil {
		t.Fatal("last_used_at 没写进去——同优先级下「最久没用过的优先」就失效了")
	}
	if time.Since(*got.LastUsedAt) > time.Minute {
		t.Errorf("last_used_at 写成了 %v，不像刚刚", got.LastUsedAt)
	}
}

// TestMarkFailureIgnoresSoftDeletedAccount 守住已删除的账号不被写回来。
//
// 删账号是软删除。失败路径不该给一个已经删掉的账号累加计数，更不该把它从
// 禁用状态里「改回来」——那会让一个已删除的号重新出现在调度里。
func TestMarkFailureIgnoresSoftDeletedAccount(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()

	sched := newTestScheduler(t, client, 1)
	acc, err := client.Account.Create().
		SetName("已删除").
		SetPlatform(domain.PlatformCommandCode).
		SetStatus(domain.StatusActive).
		Save(ctx)
	if err != nil {
		t.Fatalf("建账号: %v", err)
	}
	// 软删除，跟 AccountService.Delete 做的事一致。别用 DeleteOneID——那是
	// 硬删除，行真的没了，也就测不出「失败路径不该写已删除的账号」。
	if err := client.Account.UpdateOneID(acc.ID).
		SetDeletedAt(time.Now()).
		Exec(ctx); err != nil {
		t.Fatalf("软删除: %v", err)
	}

	if disabled := sched.MarkFailure(ctx, acc.ID, errors.New("上游 500")); disabled {
		t.Error("已删除的账号不该被禁用（那等于把它写回来）")
	}

	// 计数不该动。
	got, err := client.Account.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("读回账号: %v", err)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("已删除账号的计数不该被改，实际 %d", got.ConsecutiveFailures)
	}
	if got.Status != domain.StatusActive {
		t.Errorf("已删除账号的状态不该被改，实际 %s", got.Status)
	}
}

// TestNewClampsInvalidThreshold 守住配置写错时的行为。
//
// HEALTH_FAILURE_THRESHOLD 写成 0（或者手滑写成负数）时比较式恒真，账号抖
// 一下就被停掉。这条路径是自动的、没人盯着，等发现时整池账号可能都躺下了。
func TestNewClampsInvalidThreshold(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, configured := range []int{0, -1, -100} {
		s := New(nil, nil, configured, logger)
		if s.failureThreshold != 1 {
			t.Errorf("阈值配成 %d 时应当钳到 1，实际 %d", configured, s.failureThreshold)
		}
	}
	// 正常值不能被顺手改掉。
	if s := New(nil, nil, 5, logger); s.failureThreshold != 5 {
		t.Errorf("阈值 5 被改成了 %d", s.failureThreshold)
	}
}
