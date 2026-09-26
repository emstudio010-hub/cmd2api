// Package db 负责打开数据库连接并保证表结构就绪。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"cmd2api/ent"
	"cmd2api/internal/config"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"
)

// Open 建立连接池、验证连通性，并把表结构迁移到当前 schema。
//
// 用 ent 的自动迁移而不是 SQL 迁移文件：这个项目表结构简单、没有历史包袱，
// 自动迁移省掉了一整套「迁移文件编号冲突」的维护成本。代价是没法做数据回填，
// 真需要时再引入版本化迁移。
func Open(ctx context.Context, cfg config.DatabaseConfig) (*ent.Client, error) {
	drv, err := entsql.Open(dialect.Postgres, cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("连接数据库: %w", err)
	}

	pool := drv.DB()
	pool.SetMaxOpenConns(cfg.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.MaxIdleConns)
	pool.SetConnMaxLifetime(time.Hour)

	if err := ping(ctx, pool); err != nil {
		return nil, err
	}

	client := ent.NewClient(ent.Driver(drv))
	if err := client.Schema.Create(ctx); err != nil {
		return nil, fmt.Errorf("迁移表结构: %w", err)
	}
	return client, nil
}

// ping 带重试地探活。
//
// 容器编排里 app 常常比 postgres 先起来，compose 的 depends_on 只保证启动顺序
// 不保证就绪。这里退避重试，省得靠 restart 策略兜底。
func ping(ctx context.Context, pool *sql.DB) error {
	const attempts = 10
	var lastErr error
	for i := range attempts {
		if err := pool.PingContext(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * time.Second):
		}
	}
	return fmt.Errorf("数据库在 %d 次重试后仍不可用: %w", attempts, lastErr)
}
