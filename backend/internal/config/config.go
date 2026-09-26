// Package config 从环境变量加载运行配置。
//
// 刻意只用环境变量、不读配置文件：容器部署时改配置就是改 compose 里的 env，
// 少一层「配置到底从哪来的」的排查成本。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是应用的完整运行配置。
type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Auth      AuthConfig
	CommandCC CommandCodeConfig
	Bootstrap BootstrapConfig
	Health    HealthConfig
}

// ServerConfig 控制 HTTP 服务本身。
type ServerConfig struct {
	Host string
	Port int
	// Mode 传给 gin：debug / release。
	Mode string
	// PublicBaseURL 是浏览器授权流程里回调地址的基址。
	//
	// 留空表示按发起授权那个请求的 Host 推导——那是最常见也最省事的做法，
	// 因为管理员访问面板用的地址，就是浏览器能连回这台机器的地址。
	// 只有反代改写了 Host、或者面板与回调必须分属不同域名时才需要显式指定。
	PublicBaseURL string
}

// Addr 返回 net/http 用的监听地址。
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// DatabaseConfig 是 PostgreSQL 连接配置。
type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	SSLMode  string
	// MaxOpenConns 限制连接池上限，避免容器里把 PG 连接打满。
	MaxOpenConns int
	MaxIdleConns int
}

// DSN 拼出 lib/pq 使用的连接串。
func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode,
	)
}

// AuthConfig 是管理员登录与下游密钥相关的配置。
type AuthConfig struct {
	// JWTSecret 用于签发管理后台的登录令牌。
	JWTSecret string
	// TokenTTL 是登录令牌有效期。
	TokenTTL time.Duration
	// EncryptionKey 是加密上游账号凭证的 AES-256 密钥（32 字节）。
	EncryptionKey []byte
}

// CommandCodeConfig 是 Command Code 上游的接入参数。
//
// 这些默认值对着 commandcode-proxy 的 config.json 抄，改动前先确认为什么要改——
// 上游对 project slug、CLI 版本这类常量是有校验的。
type CommandCodeConfig struct {
	// BaseURL 是上游 API 根地址。
	BaseURL string
	// ProjectSlug 作为 x-project-slug 头发送。
	ProjectSlug string
	// CLI 版本号，随请求上报，用于伪装成官方 CLI。
	CLIVersion string
	// FingerprintSalt 参与设备指纹派生。同一个账号必须始终得到同一指纹，
	// 换 salt 等于让所有账号一起换设备。
	FingerprintSalt string
	// EmptySystemPlaceholder 在请求没有 system 提示词时塞一个空格占位，
	// 防止上游注入它自带的默认system提示词。
	EmptySystemPlaceholder bool
	// StreamIdleTimeout 是流式响应上游读空闲超时。
	StreamIdleTimeout time.Duration
	// NonStreamIdleTimeout 是非流式响应的上游读空闲超时。
	NonStreamIdleTimeout time.Duration
	// MaxBodyMB 限制请求体大小。
	MaxBodyMB int64
	// ModelCacheTTL 是模型列表缓存时长。
	ModelCacheTTL time.Duration
	// UpstreamProxy 是访问上游时走的 HTTP 代理，形如 http://127.0.0.1:7890。
	//
	// 留空表示直连。这个配置是必要的而不是可选的补充：Go 的 http.Transport
	// 不会自动读 HTTP_PROXY 之类的环境变量（我们用的是自定义 Transport），
	// 而服务器访问境外上游常常必须走代理。只支持 http:// 代理，
	// 与参考实现保持一致——socks5 需要额外的库，等真有需求再加。
	UpstreamProxy string
}

// BootstrapConfig 决定首次启动时如何创建管理员。
type BootstrapConfig struct {
	Email    string
	Password string
}

// HealthConfig 控制账号健康检查任务。
type HealthConfig struct {
	Enabled bool
	// Interval 是探测周期。
	Interval time.Duration
	// FailureThreshold 是连续失败多少次后自动禁用账号。
	FailureThreshold int
	// Timeout 是单次探测超时。
	Timeout time.Duration
}

// Load 从环境变量读取配置并做基本校验。
func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Host:          env("SERVER_HOST", "0.0.0.0"),
			Port:          envInt("SERVER_PORT", 8080),
			Mode:          env("GIN_MODE", "release"),
			PublicBaseURL: env("PUBLIC_BASE_URL", ""),
		},
		Database: DatabaseConfig{
			Host:         env("DB_HOST", "127.0.0.1"),
			Port:         envInt("DB_PORT", 5432),
			User:         env("DB_USER", "cmd2api"),
			Password:     env("DB_PASSWORD", ""),
			Name:         env("DB_NAME", "cmd2api"),
			SSLMode:      env("DB_SSLMODE", "disable"),
			MaxOpenConns: envInt("DB_MAX_OPEN_CONNS", 20),
			MaxIdleConns: envInt("DB_MAX_IDLE_CONNS", 5),
		},
		Auth: AuthConfig{
			JWTSecret: env("JWT_SECRET", ""),
			TokenTTL:  envDuration("JWT_TTL", 24*time.Hour),
		},
		CommandCC: CommandCodeConfig{
			BaseURL:                env("CC_API_BASE", "https://api.commandcode.ai"),
			ProjectSlug:            env("CC_PROJECT_SLUG", "cc-proxy"),
			CLIVersion:             env("CC_CLI_VERSION", "1.0.0"),
			FingerprintSalt:        env("CC_FINGERPRINT_SALT", ""),
			EmptySystemPlaceholder: envBool("CC_EMPTY_SYSTEM_PLACEHOLDER", true),
			StreamIdleTimeout:      envDuration("CC_STREAM_IDLE", 30*time.Second),
			NonStreamIdleTimeout:   envDuration("CC_NONSTREAM_IDLE", 90*time.Second),
			MaxBodyMB:              int64(envInt("CC_MAX_BODY_MB", 100)),
			ModelCacheTTL:          envDuration("CC_MODEL_CACHE_TTL", 5*time.Minute),
			UpstreamProxy:          env("CC_UPSTREAM_PROXY", ""),
		},
		Bootstrap: BootstrapConfig{
			Email:    env("BOOTSTRAP_ADMIN_EMAIL", "admin@cmd2api.local"),
			Password: env("BOOTSTRAP_ADMIN_PASSWORD", ""),
		},
		Health: HealthConfig{
			Enabled:          envBool("HEALTH_CHECK_ENABLED", true),
			Interval:         envDuration("HEALTH_CHECK_INTERVAL", 10*time.Minute),
			FailureThreshold: envInt("HEALTH_FAILURE_THRESHOLD", 5),
			Timeout:          envDuration("HEALTH_CHECK_TIMEOUT", 20*time.Second),
		},
	}

	key, err := parseEncryptionKey(env("ENCRYPTION_KEY", ""))
	if err != nil {
		return nil, err
	}
	cfg.Auth.EncryptionKey = key

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Database.Password == "" {
		return fmt.Errorf("DB_PASSWORD 不能为空")
	}
	if len(c.Auth.JWTSecret) < 16 {
		return fmt.Errorf("JWT_SECRET 必须至少 16 个字符，当前长度 %d", len(c.Auth.JWTSecret))
	}
	if len(c.Auth.EncryptionKey) != 32 {
		return fmt.Errorf("ENCRYPTION_KEY 必须解出 32 字节，当前 %d", len(c.Auth.EncryptionKey))
	}
	if c.Bootstrap.Password == "" {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD 不能为空（首次启动用它创建管理员）")
	}
	if len(c.Bootstrap.Password) < 8 {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD 至少 8 个字符")
	}
	if c.CommandCC.BaseURL == "" {
		return fmt.Errorf("CC_API_BASE 不能为空")
	}
	if c.CommandCC.UpstreamProxy != "" {
		// 提前校验：配错了应该启动就失败，而不是等第一次请求才报一个
		// 难懂的网络错误。
		u, err := url.Parse(c.CommandCC.UpstreamProxy)
		if err != nil {
			return fmt.Errorf("CC_UPSTREAM_PROXY 不是合法 URL: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("CC_UPSTREAM_PROXY 只支持 http:// 或 https:// 代理，当前 scheme=%q", u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("CC_UPSTREAM_PROXY 缺少主机名: %q", c.CommandCC.UpstreamProxy)
		}
	}
	return nil
}

// parseEncryptionKey 接受 64 位十六进制字符串或 32 字节裸串。
//
// 十六进制是推荐用法，方便在 compose 里写而不必处理转义。
func parseEncryptionKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY 未设置：用 `openssl rand -hex 32` 生成一个")
	}
	if len(raw) == 64 && isHex(raw) {
		key := make([]byte, 32)
		for i := 0; i < 32; i++ {
			v, err := strconv.ParseUint(raw[i*2:i*2+2], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("ENCRYPTION_KEY 十六进制解析失败: %w", err)
			}
			key[i] = byte(v)
		}
		return key, nil
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("ENCRYPTION_KEY 格式不对：应为 64 位十六进制或 32 字节，当前 %d 字符", len(raw))
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
