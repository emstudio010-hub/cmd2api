package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/account"
	"cmd2api/ent/accountgroup"
	"cmd2api/ent/group"
	"cmd2api/internal/crypto"
	"cmd2api/internal/domain"
	"cmd2api/internal/relay"
	"cmd2api/internal/scheduler"
)

// AccountService 负责上游账号的增删改查与健康检查。
type AccountService struct {
	client      *ent.Client
	vault       *crypto.Vault
	relayClient *relay.Client
	sched       *scheduler.Scheduler
	logger      *slog.Logger
}

// NewAccountService 构造 AccountService。
func NewAccountService(
	client *ent.Client,
	vault *crypto.Vault,
	relayClient *relay.Client,
	sched *scheduler.Scheduler,
	logger *slog.Logger,
) *AccountService {
	return &AccountService{
		client:      client,
		vault:       vault,
		relayClient: relayClient,
		sched:       sched,
		logger:      logger,
	}
}

// CreateAccountInput 是新建账号的输入。
type CreateAccountInput struct {
	Name  string
	Notes string
	// Platform 是必填项：commandcode 或 opencode。
	Platform string
	// AccountMode 仅 OpenCode 需要（zen / go）。
	AccountMode string
	// BaseURL 仅 OpenCode 用，留空则按 AccountMode 取默认地址。
	BaseURL        string
	APIKey         string
	Concurrency    int
	Priority       int
	RateMultiplier float64
	GroupIDs       []int64
	ExpiresAt      *time.Time
}

// UpdateAccountInput 是修改账号的输入。指针为 nil 表示不改该字段。
type UpdateAccountInput struct {
	Name           *string
	Notes          *string
	APIKey         *string
	Concurrency    *int
	Priority       *int
	RateMultiplier *float64
	Status         *string
	Schedulable    *bool
	GroupIDs       *[]int64
	ExpiresAt      *time.Time
	ClearExpiry    bool
}

// ErrInvalidAPIKey 表示密钥格式不对。
var ErrInvalidAPIKey = errors.New("密钥格式不正确，应以 user_ 开头")

// ErrInvalidPlatform 表示平台取值不受支持。
var ErrInvalidPlatform = errors.New("平台取值无效，只支持 commandcode 或 opencode")

// ErrInvalidAccountMode 表示 OpenCode 的计费模式没给或给错了。
var ErrInvalidAccountMode = errors.New("OpenCode 账号必须指定 account_mode（zen 或 go）")

// ErrPlatformMismatch 表示账号平台与分组平台不一致。
var ErrPlatformMismatch = errors.New("账号平台与分组平台不一致：一个分组只能装同平台的账号")

// Create 新建一个上游账号。
func (s *AccountService) Create(ctx context.Context, in CreateAccountInput) (*ent.Account, error) {
	platform := in.Platform
	if platform == "" {
		// 兼容不传 platform 的老调用方，按 Command Code 处理。
		platform = domain.PlatformCommandCode
	}
	if !domain.IsValidPlatform(platform) {
		return nil, ErrInvalidPlatform
	}

	apiKey := strings.TrimSpace(in.APIKey)
	if apiKey == "" {
		return nil, errors.New("密钥不能为空")
	}
	// 前缀校验只对 Command Code 做：OpenCode 的密钥格式不同，
	// 套用 user_ 前缀会把合法密钥挡在外面。
	if platform == domain.PlatformCommandCode && !strings.HasPrefix(apiKey, domain.CommandCodeKeyPrefix) {
		return nil, ErrInvalidAPIKey
	}

	extra := map[string]any{}
	if platform == domain.PlatformOpenCode {
		mode := in.AccountMode
		if mode != domain.AccountModeZen && mode != domain.AccountModeGo {
			return nil, ErrInvalidAccountMode
		}
		extra["account_mode"] = mode
		// 只存显式覆盖的地址。留空不存，将来改默认地址时能自动生效，
		// 而不是被一条陈旧的快照钉死。
		if baseURL := strings.TrimSpace(in.BaseURL); baseURL != "" {
			extra["base_url"] = baseURL
		}
	}

	if len(in.GroupIDs) > 0 {
		if err := s.assertGroupsMatchPlatform(ctx, in.GroupIDs, platform); err != nil {
			return nil, err
		}
	}

	encrypted, err := s.vault.Encrypt(apiKey)
	if err != nil {
		return nil, fmt.Errorf("加密密钥: %w", err)
	}

	builder := s.client.Account.Create().
		SetName(strings.TrimSpace(in.Name)).
		SetPlatform(platform).
		SetType(domain.AccountTypeAPIKey).
		SetCredentials(map[string]any{"api_key_encrypted": encrypted}).
		SetExtra(extra).
		SetStatus(domain.StatusActive).
		SetSchedulable(true)

	if in.Notes != "" {
		builder = builder.SetNotes(in.Notes)
	}
	if in.Concurrency > 0 {
		builder = builder.SetConcurrency(in.Concurrency)
	}
	if in.Priority > 0 {
		builder = builder.SetPriority(in.Priority)
	}
	if in.RateMultiplier > 0 {
		builder = builder.SetRateMultiplier(in.RateMultiplier)
	}
	if in.ExpiresAt != nil {
		builder = builder.SetExpiresAt(*in.ExpiresAt)
	}

	created, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}

	if len(in.GroupIDs) > 0 {
		if err := s.setGroups(ctx, created.ID, in.GroupIDs, platform); err != nil {
			// 分组绑定失败就把账号删掉，避免留下一个不属于任何分组、
			// 调度不到也用不上的孤儿账号。
			if delErr := s.client.Account.DeleteOneID(created.ID).Exec(ctx); delErr != nil {
				s.logger.Error("回滚账号创建失败", "err", delErr, "account_id", created.ID)
			}
			return nil, err
		}
	}

	// 统一重读一次，把分组边带上。返回给前端的账号必须包含完整的
	// group_ids——不重读的话界面会显示成「没绑分组」，而其实绑上了。
	created = s.reloadWithGroups(ctx, created)

	s.logger.Info("已创建上游账号", "account_id", created.ID, "name", created.Name,
		"platform", platform)
	return created, nil
}

// reloadWithGroups 重新读取账号并带上分组边。
//
// 读失败时退回原对象：宁可少一个 group_ids 字段，也不该让一次成功的
// 写操作以错误的形式返回给调用方。
func (s *AccountService) reloadWithGroups(ctx context.Context, acc *ent.Account) *ent.Account {
	reloaded, err := s.client.Account.Query().
		Where(account.IDEQ(acc.ID)).
		WithGroups().
		Only(ctx)
	if err != nil {
		s.logger.Warn("重读账号分组失败", "err", err, "account_id", acc.ID)
		return acc
	}
	return reloaded
}

// Update 修改账号。
func (s *AccountService) Update(ctx context.Context, id int64, in UpdateAccountInput) (*ent.Account, error) {
	builder := s.client.Account.UpdateOneID(id)

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errors.New("账号名不能为空")
		}
		builder = builder.SetName(name)
	}
	if in.Notes != nil {
		builder = builder.SetNotes(*in.Notes)
	}
	if in.APIKey != nil {
		apiKey := strings.TrimSpace(*in.APIKey)
		if !strings.HasPrefix(apiKey, domain.CommandCodeKeyPrefix) {
			return nil, ErrInvalidAPIKey
		}
		encrypted, err := s.vault.Encrypt(apiKey)
		if err != nil {
			return nil, fmt.Errorf("加密密钥: %w", err)
		}
		builder = builder.SetCredentials(map[string]any{"api_key_encrypted": encrypted})
		// 换了密钥就是换了账号身份，旧的会话状态必须丢掉，
		// 否则新账号会顶着旧账号的 session 和指纹发请求。
		if old, err := s.client.Account.Get(ctx, id); err == nil {
			if prev, ok := s.decryptKey(old); ok {
				s.relayClient.ForgetKey(prev)
			}
		}
	}
	if in.Concurrency != nil {
		if *in.Concurrency <= 0 {
			return nil, errors.New("并发数必须大于 0")
		}
		builder = builder.SetConcurrency(*in.Concurrency)
	}
	if in.Priority != nil {
		builder = builder.SetPriority(*in.Priority)
	}
	if in.RateMultiplier != nil {
		builder = builder.SetRateMultiplier(*in.RateMultiplier)
	}
	if in.Status != nil {
		builder = builder.SetStatus(*in.Status)
		// 手动改状态时顺带把调度开关对齐，否则会出现「状态是 active
		// 但不可调度」或反过来的矛盾组合。
		if *in.Status == domain.StatusActive {
			builder = builder.SetSchedulable(true).ClearErrorMessage()
		} else {
			builder = builder.SetSchedulable(false)
		}
		builder = builder.SetConsecutiveFailures(0)
	}
	if in.Schedulable != nil {
		builder = builder.SetSchedulable(*in.Schedulable)
	}
	if in.ClearExpiry {
		builder = builder.ClearExpiresAt()
	} else if in.ExpiresAt != nil {
		builder = builder.SetExpiresAt(*in.ExpiresAt)
	}

	updated, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}

	if in.GroupIDs != nil {
		// 平台不可修改，所以直接取账号当前的平台来校验分组。
		platform := updated.Platform
		if platform == "" {
			platform = domain.PlatformCommandCode
		}
		if err := s.setGroups(ctx, id, *in.GroupIDs, platform); err != nil {
			return nil, err
		}
	}

	// 无论这次改了什么都要带上分组边：只改名字时如果不重读，
	// 响应里的 group_ids 会变成空的，前端会误以为分组被清掉了。
	return s.reloadWithGroups(ctx, updated), nil
}

// Delete 软删除账号，并清掉它的内存状态。
func (s *AccountService) Delete(ctx context.Context, id int64) error {
	acc, err := s.client.Account.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.client.Account.DeleteOneID(id).Exec(ctx); err != nil {
		return err
	}
	// 释放调度器里该账号的并发闸门与上游会话状态，
	// 否则这些 map 会随删号次数一直长。
	if key, ok := s.decryptKey(acc); ok {
		s.relayClient.ForgetKey(key)
	}
	s.logger.Info("已删除上游账号", "account_id", id, "name", acc.Name)
	return nil
}

// assertGroupsMatchPlatform 校验分组都存在且平台与账号一致。
//
// 这是分组绑定平台这个设计的执行点：一个分组只能装同平台的账号。
// 混装会让「请求某个模型」被路由到根本没有该模型的账号池上。
func (s *AccountService) assertGroupsMatchPlatform(ctx context.Context, groupIDs []int64, platform string) error {
	ids := uniqueIDs(groupIDs)
	if len(ids) == 0 {
		return nil
	}
	groups, err := s.client.Group.Query().
		Where(group.IDIn(ids...), group.DeletedAtIsNil()).
		All(ctx)
	if err != nil {
		return fmt.Errorf("校验分组: %w", err)
	}
	if len(groups) != len(ids) {
		return errors.New("包含不存在的分组")
	}
	for _, g := range groups {
		groupPlatform := g.Platform
		if groupPlatform == "" {
			groupPlatform = domain.PlatformCommandCode
		}
		if groupPlatform != platform {
			return fmt.Errorf("%w（分组「%s」是 %s 平台，账号是 %s 平台）",
				ErrPlatformMismatch, g.Name, groupPlatform, platform)
		}
	}
	return nil
}

// setGroups 重设账号的分组绑定。
func (s *AccountService) setGroups(ctx context.Context, accountID int64, groupIDs []int64, platform string) error {
	if err := s.assertGroupsMatchPlatform(ctx, groupIDs, platform); err != nil {
		return err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.AccountGroup.Delete().
		Where(accountgroup.AccountIDEQ(accountID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("清除旧分组绑定: %w", err)
	}

	for _, gid := range uniqueIDs(groupIDs) {
		if _, err := tx.AccountGroup.Create().
			SetAccountID(accountID).
			SetGroupID(gid).
			Save(ctx); err != nil {
			return fmt.Errorf("绑定分组 %d: %w", gid, err)
		}
	}

	return tx.Commit()
}

// Check 探活单个账号并回写结果。
func (s *AccountService) Check(ctx context.Context, id int64) (*relay.ProbeResult, error) {
	acc, err := s.client.Account.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// 走调度器拿接入信息，这样两种平台的探活方式由它统一分派，
	// 不用在这里再写一遍平台判断。
	target, err := s.sched.TargetByAccountID(ctx, id)
	if err != nil {
		if strings.Contains(err.Error(), "api_key") {
			return nil, errors.New("该账号的密钥无法解密，请重新录入")
		}
		return nil, err
	}

	result := s.relayClient.Probe(ctx, target)
	s.applyProbeResult(ctx, acc, result)
	return &result, nil
}

// applyProbeResult 把探活结果写回账号。
func (s *AccountService) applyProbeResult(ctx context.Context, acc *ent.Account, result relay.ProbeResult) {
	now := time.Now()
	builder := s.client.Account.UpdateOneID(acc.ID).
		SetLastHealthCheckAt(now).
		SetLatencyMs(int(result.Latency.Milliseconds()))

	if result.Err == nil {
		// 探活成功就是最有力的恢复信号：把失败计数清零、解除临时状态，
		// 让账号重新参与调度。但只要不是手动禁用的，就自动恢复。
		builder = builder.
			SetLastHealthCheckOk(true).
			ClearLastHealthCheckError().
			SetConsecutiveFailures(0)
		if acc.Status == domain.StatusError {
			builder = builder.SetStatus(domain.StatusActive).SetSchedulable(true).ClearErrorMessage()
			s.logger.Info("账号探活成功，已自动恢复", "account_id", acc.ID, "name", acc.Name)
		}
	} else {
		builder = builder.
			SetLastHealthCheckOk(false).
			SetLastHealthCheckError(result.ErrorMessage()).
			SetConsecutiveFailures(acc.ConsecutiveFailures + 1)
	}

	if _, err := builder.Save(ctx); err != nil {
		s.logger.Warn("回写探活结果失败", "err", err, "account_id", acc.ID)
	}
}

// RunHealthChecks 周期性地探活所有账号。
//
// 探活是串行 + 间隔的：并发探活一堆账号会在上游那边形成一波整齐的请求，
// 既是异常模式，也容易触发限流。
func (s *AccountService) RunHealthChecks(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.Info("账号健康检查已启动", "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("账号健康检查已停止")
			return
		case <-ticker.C:
			s.checkAll(ctx)
		}
	}
}

// checkAll 探活一轮。
func (s *AccountService) checkAll(ctx context.Context) {
	accounts, err := s.client.Account.Query().
		Where(
			account.DeletedAtIsNil(),
			// 手动禁用的账号不探活：管理员停用它是有意的，
			// 自动把它探活回来等于违背管理员的意图。
			account.StatusNEQ(domain.StatusDisabled),
		).
		Order(ent.Asc(account.FieldPriority)).
		All(ctx)
	if err != nil {
		s.logger.Warn("查询待探活账号失败", "err", err)
		return
	}

	var okCount, failCount int
	for _, acc := range accounts {
		if ctx.Err() != nil {
			return
		}
		target, err := s.sched.TargetByAccountID(ctx, acc.ID)
		if err != nil {
			s.logger.Warn("取账号接入信息失败，跳过探活", "account_id", acc.ID, "err", err)
			continue
		}

		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result := s.relayClient.Probe(probeCtx, target)
		cancel()

		s.applyProbeResult(ctx, acc, result)
		if result.Err == nil {
			okCount++
		} else {
			failCount++
			s.logger.Warn("账号探活失败",
				"account_id", acc.ID, "name", acc.Name, "reason", result.ErrorMessage())
		}
	}
	if len(accounts) > 0 {
		s.logger.Info("账号探活完成", "total", len(accounts), "ok", okCount, "failed", failCount)
	}
}

// decryptKey 解出账号的明文密钥。
func (s *AccountService) decryptKey(acc *ent.Account) (string, bool) {
	if acc == nil {
		return "", false
	}
	if enc, ok := acc.Credentials["api_key_encrypted"].(string); ok && enc != "" {
		plain, err := s.vault.Decrypt(enc)
		if err != nil {
			return "", false
		}
		return plain, true
	}
	// 兼容手工插入的明文账号。
	if raw, ok := acc.Credentials["api_key"].(string); ok && raw != "" {
		return raw, true
	}
	return "", false
}

// MaskedKey 返回账号密钥的打码形式，供列表展示。
func (s *AccountService) MaskedKey(acc *ent.Account) string {
	key, ok := s.decryptKey(acc)
	if !ok {
		return ""
	}
	return crypto.MaskKey(key)
}

// uniqueIDs 去重，保持原顺序。
func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
