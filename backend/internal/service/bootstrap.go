package service

import (
	"context"
	"fmt"
	"log/slog"

	"cmd2api/ent"
	"cmd2api/ent/user"
	"cmd2api/internal/auth"
	"cmd2api/internal/config"
	"cmd2api/internal/domain"
)

// BootstrapAdmin 保证系统里存在一个可登录的管理员。
//
// 仅在「一个用户都没有」时创建。已经存在管理员时**不会**用环境变量里的密码
// 去覆盖——否则每次重启都会把管理员改过的密码重置回去，等于密码形同虚设。
func BootstrapAdmin(ctx context.Context, client *ent.Client, cfg config.BootstrapConfig, logger *slog.Logger) error {
	count, err := client.User.Query().
		Where(user.RoleEQ(domain.RoleAdmin), user.DeletedAtIsNil()).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("查询管理员数量: %w", err)
	}
	if count > 0 {
		logger.Info("已存在管理员账号，跳过初始化")
		return nil
	}

	// 也检查一下邮箱是否被非管理员用户占用，避免撞唯一约束。
	existing, err := client.User.Query().
		Where(user.EmailEQ(cfg.Email), user.DeletedAtIsNil()).
		Only(ctx)
	if err == nil {
		// 邮箱存在但不是管理员：把它提升为管理员比新建一个更符合预期
		// （多半是上一次初始化中途改过角色）。
		if _, err := client.User.UpdateOneID(existing.ID).
			SetRole(domain.RoleAdmin).
			SetStatus(domain.StatusActive).
			Save(ctx); err != nil {
			return fmt.Errorf("提升已有用户为管理员: %w", err)
		}
		logger.Info("已将已有用户提升为管理员", "email", cfg.Email)
		return nil
	}
	if !ent.IsNotFound(err) {
		return fmt.Errorf("查询用户: %w", err)
	}

	hash, err := auth.HashPassword(cfg.Password)
	if err != nil {
		return fmt.Errorf("生成密码哈希: %w", err)
	}

	created, err := client.User.Create().
		SetEmail(cfg.Email).
		SetPasswordHash(hash).
		SetRole(domain.RoleAdmin).
		SetStatus(domain.StatusActive).
		SetNotes("首次启动自动创建的管理员").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("创建管理员: %w", err)
	}

	logger.Info("已创建初始管理员账号", "email", created.Email, "user_id", created.ID)
	logger.Warn("请登录后立即修改默认密码（后台右上角 → 修改密码）")
	return nil
}
