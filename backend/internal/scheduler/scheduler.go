// Package scheduler 负责从分组的上游账号池里挑一个账号来承载请求，
// 并维护账号的调度状态（限流、过载、连续失败、自动禁用）。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/account"
	"cmd2api/ent/group"
	"cmd2api/internal/crypto"
	"cmd2api/internal/domain"

	"entgo.io/ent/dialect/sql"
)

// ErrNoAccountAvailable 表示分组里当前没有可用账号。
var ErrNoAccountAvailable = errors.New("分组内没有可用账号")

// candidateLimit 是一次调度最多评估多少个候选账号。
//
// 不设上限的话，账号池很大时每个请求都要把整池读出来再逐个试闸门。
// 取前 50 个按优先级排好序的就够——真出现前 50 个全满的情况，
// 说明这个分组本来就该扩容了，让请求快速失败比拖慢更好。
const candidateLimit = 50

// Credentials 是从账号行里解出来的明文凭证。
type Credentials struct {
	APIKey string
}

// Lease 是一次账号占用。调用方必须调用 Release，通常写成 defer。
type Lease struct {
	Account     *ent.Account
	Credentials Credentials
	// Platform 是所选账号所属的上游平台，调用方据此选择协议适配器。
	Platform string
	// BaseURL 是 OpenCode 账号的可覆盖上游地址；Command Code 账号为空。
	BaseURL string
	// AccountMode 是 OpenCode 的计费模式（zen/go）；Command Code 账号为空。
	AccountMode string
	Release     func()
}

// Scheduler 持有调度所需的依赖。
type Scheduler struct {
	client  *ent.Client
	vault   *crypto.Vault
	limiter *limiter
	logger  *slog.Logger

	// failureThreshold 是连续失败多少次后自动禁用账号。
	failureThreshold int
}

// New 构造 Scheduler。
func New(client *ent.Client, vault *crypto.Vault, failureThreshold int, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		client:           client,
		vault:            vault,
		limiter:          newLimiter(),
		logger:           logger,
		failureThreshold: failureThreshold,
	}
}

// Acquire 为指定分组租一个可用账号。
//
// 按优先级从高到低、同优先级下最久没用过的优先，逐个尝试占用并发名额，
// 抢到就返回。这样同一账号会被均匀使用，也天然做到了故障转移：
// 前面的账号满了或状态不对，就自动落到后面的。
// Acquire 为指定分组租一个可用账号。excludeIDs 里的账号会被跳过。
//
// excludeIDs 是给故障转移用的：一次请求里换账号重试，必须排除已经试过的，
// 否则会在同一个坏账号上反复失败——同一个请求发给同一个账号，结果不会变，
// 只是把错误路径拖长了好几倍。
func (s *Scheduler) Acquire(ctx context.Context, groupID int64, excludeIDs ...int64) (*Lease, error) {
	// 先取分组的平台。它既用于筛账号，也要回传给调用方选协议适配器。
	grp, err := s.client.Group.Query().
		Where(group.IDEQ(groupID), group.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("分组 %d 不存在或已删除", groupID)
		}
		return nil, fmt.Errorf("查询分组: %w", err)
	}
	platform := grp.Platform
	if platform == "" {
		// 老数据没有 platform 字段时按 Command Code 处理，与表默认值一致。
		platform = domain.PlatformCommandCode
	}

	now := time.Now()
	query := s.client.Account.Query().
		Where(
			account.DeletedAtIsNil(),
			account.StatusEQ(domain.StatusActive),
			account.SchedulableEQ(true),
			account.HasGroupsWith(group.IDEQ(groupID)),
			// 再按平台过滤一遍。绑定账号时已经校验过平台一致，
			// 这里是纵深防御：万一有历史数据或直接改库造成的错配，
			// 也不至于把请求发到协议完全不同的上游去。
			account.PlatformEQ(platform),
			// 限流未解除的跳过。
			account.Or(
				account.RateLimitResetAtIsNil(),
				account.RateLimitResetAtLT(now),
			),
			// 上游过载期的跳过。
			account.Or(
				account.OverloadUntilIsNil(),
				account.OverloadUntilLT(now),
			),
			// 已过期的跳过。
			account.Or(
				account.ExpiresAtIsNil(),
				account.ExpiresAtGT(now),
			),
		)

	if len(excludeIDs) > 0 {
		query = query.Where(account.IDNotIn(excludeIDs...))
	}

	accounts, err := query.
		Order(
			ent.Asc(account.FieldPriority),
			// NULLS FIRST：从没用过的账号应该先被用上，做冷启动摊平。
			func(sel *sql.Selector) {
				sel.OrderExpr(sql.ExprP(account.FieldLastUsedAt + " asc nulls first"))
			},
		).
		Limit(candidateLimit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询候选账号: %w", err)
	}
	if len(accounts) == 0 {
		return nil, ErrNoAccountAvailable
	}

	var lastErr error
	for _, acc := range accounts {
		if !s.limiter.tryAcquire(acc.ID, acc.Concurrency) {
			continue // 该账号并发已满，试下一个
		}

		creds, err := s.credentials(acc)
		if err != nil {
			// 解密失败是配置/数据问题，不是这个账号「忙」。放开名额、
			// 记一条日志再试下一个，避免一条坏数据把整个分组拖死。
			s.limiter.release(acc.ID)
			lastErr = fmt.Errorf("账号 %d(%s) 的凭证无法解密: %w", acc.ID, acc.Name, err)
			s.logger.Error("凭证解密失败", "account_id", acc.ID, "account_name", acc.Name, "err", err)
			continue
		}

		accountID := acc.ID
		lease := &Lease{
			Account:     acc,
			Credentials: creds,
			Platform:    acc.Platform,
			Release:     func() { s.limiter.release(accountID) },
		}
		if lease.Platform == domain.PlatformOpenCode {
			lease.AccountMode = stringFromExtra(acc.Extra, "account_mode")
			lease.BaseURL = stringFromExtra(acc.Extra, "base_url")
			if lease.BaseURL == "" {
				lease.BaseURL = domain.DefaultOpenCodeBaseURL(lease.AccountMode)
			}
		}
		return lease, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w（%s 平台的 %d 个候选账号并发均已达上限）",
		ErrNoAccountAvailable, platform, len(accounts))
}

// credentials 从账号行里解出明文密钥。
//
// 优先读密文；只有密文缺失时才回退到明文。这个顺序不能反：密文存在时
// 明文字段本该是空的，先读明文会直接判定「没有 api_key」而报错。
// 保留明文回退是为了让手工插入的账号（或早期数据）仍能跑起来。
// stringFromExtra 从 extra JSONB 里取一个字符串字段。
func stringFromExtra(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	v, _ := extra[key].(string)
	return v
}

// Target 描述一个可用账号的接入信息，不持有并发名额。
type Target struct {
	AccountID   int64
	AccountName string
	APIKey      string
	Platform    string
	BaseURL     string
	AccountMode string
}

// AnyTarget 取分组内任一可用账号的接入信息，但**不占用并发名额**。
//
// 给拉模型列表、单账号探活这类辅助调用用：它们不消耗生成额度，
// 也不该把并发名额占住导致正常请求被挤掉。
func (s *Scheduler) AnyTarget(ctx context.Context, groupID int64) (*Target, error) {
	target, err := s.firstActiveAccount(ctx, groupID)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentials(target)
	if err != nil {
		return nil, err
	}
	return s.buildTarget(target, creds), nil
}

// TargetByAccountID 按账号 ID 取接入信息，用于单账号探活。
//
// 探活要针对指定账号，而不是分组里的任意一个，所以不能走 AnyTarget。
func (s *Scheduler) TargetByAccountID(ctx context.Context, accountID int64) (*Target, error) {
	acc, err := s.client.Account.Query().
		Where(account.IDEQ(accountID), account.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentials(acc)
	if err != nil {
		return nil, err
	}
	return s.buildTarget(acc, creds), nil
}

func (s *Scheduler) firstActiveAccount(ctx context.Context, groupID int64) (*ent.Account, error) {
	acc, err := s.client.Account.Query().
		Where(
			account.DeletedAtIsNil(),
			account.StatusEQ(domain.StatusActive),
			account.HasGroupsWith(group.IDEQ(groupID)),
		).
		Order(ent.Asc(account.FieldPriority)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNoAccountAvailable
		}
		return nil, fmt.Errorf("查询账号: %w", err)
	}
	return acc, nil
}

// buildTarget 把账号行与凭证组装成 Target。
func (s *Scheduler) buildTarget(acc *ent.Account, creds Credentials) *Target {
	t := &Target{
		AccountID:   acc.ID,
		AccountName: acc.Name,
		APIKey:      creds.APIKey,
		Platform:    acc.Platform,
	}
	if t.Platform == domain.PlatformOpenCode {
		t.AccountMode = stringFromExtra(acc.Extra, "account_mode")
		t.BaseURL = stringFromExtra(acc.Extra, "base_url")
		if t.BaseURL == "" {
			t.BaseURL = domain.DefaultOpenCodeBaseURL(t.AccountMode)
		}
	}
	return t
}

func (s *Scheduler) credentials(acc *ent.Account) (Credentials, error) {
	if enc, ok := acc.Credentials["api_key_encrypted"].(string); ok && enc != "" {
		plain, err := s.vault.Decrypt(enc)
		if err != nil {
			return Credentials{}, err
		}
		return Credentials{APIKey: plain}, nil
	}
	if raw, ok := acc.Credentials["api_key"].(string); ok && raw != "" {
		return Credentials{APIKey: raw}, nil
	}
	return Credentials{}, errors.New("账号未配置 api_key")
}

// ---- 状态回写 ----
//
// 这些方法都按 accountID 条件更新，不做读-改-写。
// 高并发下多个请求可能同时改同一个账号，读-改-写会丢更新。

// MarkUsed 记录账号最近被使用，并清零连续失败计数。
func (s *Scheduler) MarkUsed(ctx context.Context, accountID int64) {
	_, err := s.client.Account.Update().
		Where(account.ID(accountID)).
		SetLastUsedAt(time.Now()).
		SetConsecutiveFailures(0).
		Save(ctx)
	if err != nil {
		s.logger.Warn("回写账号使用状态失败", "account_id", accountID, "err", err)
	}
}

// MarkRateLimited 记录一次限流。resetAt 为空时按 5 分钟兜底，
// 避免账号因为一个没有 reset 时间的 429 被永久跳过。
func (s *Scheduler) MarkRateLimited(ctx context.Context, accountID int64, resetAt *time.Time) {
	reset := time.Now().Add(5 * time.Minute)
	if resetAt != nil && resetAt.After(time.Now()) {
		reset = *resetAt
	}
	_, err := s.client.Account.Update().
		Where(account.ID(accountID)).
		SetRateLimitedAt(time.Now()).
		SetRateLimitResetAt(reset).
		Save(ctx)
	if err != nil {
		s.logger.Warn("回写限流状态失败", "account_id", accountID, "err", err)
	}
}

// MarkOverloaded 记录一次上游过载（529 之类），在 until 之前不再调度该账号。
func (s *Scheduler) MarkOverloaded(ctx context.Context, accountID int64, until time.Time) {
	_, err := s.client.Account.Update().
		Where(account.ID(accountID)).
		SetOverloadUntil(until).
		Save(ctx)
	if err != nil {
		s.logger.Warn("回写过载状态失败", "account_id", accountID, "err", err)
	}
}

// MarkFailure 记录一次失败。连续失败达到阈值时自动禁用账号并把原因写进
// error_message，管理员在后台一眼能看到为什么这个号停了。
//
// 返回是否本次触发了自动禁用。
func (s *Scheduler) MarkFailure(ctx context.Context, accountID int64, cause error) bool {
	// 带 soft-delete 过滤：已删除的账号不该再被写失败计数，
	// 更不该被这条路径重新置成禁用状态。
	acc, err := s.client.Account.Query().
		Where(account.IDEQ(accountID), account.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		s.logger.Warn("读取账号失败，跳过失败计数", "account_id", accountID, "err", err)
		return false
	}
	failures := acc.ConsecutiveFailures + 1
	msg := cause.Error()

	upd := s.client.Account.Update().
		Where(account.ID(accountID)).
		SetConsecutiveFailures(failures).
		SetErrorMessage(msg)

	disabled := failures >= s.failureThreshold
	if disabled {
		upd = upd.SetStatus(domain.StatusError).SetSchedulable(false)
	}
	if _, err := upd.Save(ctx); err != nil {
		s.logger.Warn("回写失败计数失败", "account_id", accountID, "err", err)
		return false
	}
	if disabled {
		s.logger.Warn("账号连续失败已达阈值，已自动禁用",
			"account_id", accountID, "account_name", acc.Name,
			"failures", failures, "threshold", s.failureThreshold, "cause", msg)
	}
	return disabled
}
