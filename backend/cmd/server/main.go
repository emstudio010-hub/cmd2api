// Command server 是 cmd2api 的后端服务入口。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// 内嵌时区数据库。容器（尤其 Alpine）默认不带 tzdata，没有它 TZ=Asia/Shanghai
	// 会被静默忽略、日志时间全变 UTC。内嵌一份约 450KB，换来运行镜像零系统依赖，
	// 也能直接跑在 scratch 上。
	_ "time/tzdata"

	"cmd2api/internal/auth"
	"cmd2api/internal/config"
	"cmd2api/internal/crypto"
	"cmd2api/internal/db"
	"cmd2api/internal/handler"
	"cmd2api/internal/relay"
	"cmd2api/internal/scheduler"
	"cmd2api/internal/server"
	"cmd2api/internal/service"
)

func main() {
	if err := run(); err != nil {
		// 启动期失败直接退出并给出可读原因：容器里最需要的是
		// 「哪一项配置不对」，而不是一段栈。
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger()
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger.Info("cmd2api 正在启动",
		"addr", cfg.Server.Addr(),
		"upstream", cfg.CommandCC.BaseURL,
		"db_host", cfg.Database.Host,
		"db_name", cfg.Database.Name,
	)

	// 收到 SIGTERM/SIGINT 时取消这个 ctx，触发优雅退出。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Warn("关闭数据库连接失败", "err", err)
		}
	}()
	logger.Info("数据库已就绪")

	vault, err := crypto.NewVault(cfg.Auth.EncryptionKey)
	if err != nil {
		return fmt.Errorf("初始化凭证加密: %w", err)
	}
	issuer := auth.NewIssuer(cfg.Auth.JWTSecret, cfg.Auth.TokenTTL)

	if err := service.BootstrapAdmin(ctx, client, cfg.Bootstrap, logger); err != nil {
		return err
	}

	relayClient := relay.NewClient(cfg.CommandCC, logger)
	sched := scheduler.New(client, vault, cfg.Health.FailureThreshold, logger)
	recorder := service.NewRecorder(client, logger)
	relayService := relay.NewService(relayClient, sched, recorder, cfg, logger)
	accountService := service.NewAccountService(client, vault, relayClient, sched, logger)

	// 健康检查任务随主 ctx 一起退出。
	if cfg.Health.Enabled {
		go accountService.RunHealthChecks(ctx, cfg.Health.Interval)
	} else {
		logger.Info("账号健康检查已关闭")
	}

	h := handler.New(client, vault, issuer, sched, relayService, accountService, cfg, logger)
	router := server.NewRouter(server.Options{
		Handler:   h,
		Issuer:    issuer,
		Client:    client,
		StaticDir: staticDir(),
		Mode:      cfg.Server.Mode,
	})

	httpServer := &http.Server{
		Addr:    cfg.Server.Addr(),
		Handler: router,
		// 不设 WriteTimeout：流式回答可能持续很久，写超时会把正常的长回答掐断。
		// 读取超时保留，避免慢速请求占着连接不放。
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务已监听", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case <-ctx.Done():
		logger.Info("收到退出信号，正在优雅关闭")
	}

	// 给在途请求一点时间收尾，超时就强制关。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("优雅关闭超时，强制退出", "err", err)
	}
	logger.Info("已关闭")
	return nil
}

// newLogger 构造结构化日志器。
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	// 容器里日志走 stdout 交给编排系统收集，不自己写文件——
	// 写文件会带来轮转、磁盘占满、权限一堆额外的运维问题。
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}

// staticDir 定位前端构建产物。
//
// 支持环境变量覆盖，便于把前端放在镜像外的挂载卷里更新。
func staticDir() string {
	if dir := os.Getenv("STATIC_DIR"); dir != "" {
		return dir
	}
	// 容器内约定路径；本地开发时这个目录可能不存在，router 会自动跳过。
	return "./web"
}
