package config

import (
	"strings"
	"testing"
)

// setEnv 设置测试用的环境变量，返回清理函数。
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// validEnv 是一份能通过校验的最小配置。
func validEnv() map[string]string {
	return map[string]string{
		"DB_PASSWORD":              "dbpass",
		"JWT_SECRET":               "0123456789abcdefghij",
		"ENCRYPTION_KEY":           strings.Repeat("ab", 32), // 64 位十六进制
		"BOOTSTRAP_ADMIN_PASSWORD": "adminpass123",
	}
}

func TestLoadValid(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
	if len(cfg.Auth.EncryptionKey) != 32 {
		t.Errorf("ENCRYPTION_KEY 应解出 32 字节，得到 %d", len(cfg.Auth.EncryptionKey))
	}
	if cfg.CommandCC.BaseURL != "https://api.commandcode.ai" {
		t.Errorf("上游默认地址不对: %s", cfg.CommandCC.BaseURL)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("默认端口应为 8080，得到 %d", cfg.Server.Port)
	}
	// 默认开启的空 system 占位是个容易在重构里被改错的行为。
	if !cfg.CommandCC.EmptySystemPlaceholder {
		t.Error("CC_EMPTY_SYSTEM_PLACEHOLDER 应默认为 true")
	}
}

// TestEncryptionKeyParsing 覆盖十六进制与裸字节两种写法。
func TestEncryptionKeyParsing(t *testing.T) {
	hexEnv := validEnv()
	hexEnv["ENCRYPTION_KEY"] = strings.Repeat("00", 32)
	setEnv(t, hexEnv)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("64 位十六进制应被接受: %v", err)
	}
	if cfg.Auth.EncryptionKey[0] != 0 {
		t.Error("十六进制解析结果不对")
	}

	// 32 个字符的裸串也接受。
	rawEnv := validEnv()
	rawEnv["ENCRYPTION_KEY"] = strings.Repeat("k", 32)
	setEnv(t, rawEnv)
	if _, err := Load(); err != nil {
		t.Fatalf("32 字节裸串应被接受: %v", err)
	}
}

// TestLoadRejectsBadConfig 逐项验证缺失/错误的配置都被拦下，
// 且错误信息里要指出是哪个变量有问题。
func TestLoadRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantVar string
	}{
		{
			name:    "缺少加密密钥",
			mutate:  func(m map[string]string) { delete(m, "ENCRYPTION_KEY") },
			wantVar: "ENCRYPTION_KEY",
		},
		{
			name:    "加密密钥长度不对",
			mutate:  func(m map[string]string) { m["ENCRYPTION_KEY"] = "tooshort" },
			wantVar: "ENCRYPTION_KEY",
		},
		{
			name:    "加密密钥十六进制非法",
			mutate:  func(m map[string]string) { m["ENCRYPTION_KEY"] = strings.Repeat("zz", 32) },
			wantVar: "ENCRYPTION_KEY",
		},
		{
			name:    "JWT 密钥太短",
			mutate:  func(m map[string]string) { m["JWT_SECRET"] = "short" },
			wantVar: "JWT_SECRET",
		},
		{
			name:    "缺少数据库口令",
			mutate:  func(m map[string]string) { delete(m, "DB_PASSWORD") },
			wantVar: "DB_PASSWORD",
		},
		{
			name:    "缺少管理员初始密码",
			mutate:  func(m map[string]string) { delete(m, "BOOTSTRAP_ADMIN_PASSWORD") },
			wantVar: "BOOTSTRAP_ADMIN_PASSWORD",
		},
		{
			name:    "管理员初始密码太短",
			mutate:  func(m map[string]string) { m["BOOTSTRAP_ADMIN_PASSWORD"] = "short" },
			wantVar: "BOOTSTRAP_ADMIN_PASSWORD",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			tc.mutate(env)
			setEnv(t, env)

			_, err := Load()
			if err == nil {
				t.Fatal("应当报错但没有")
			}
			// 错误信息必须点名出问题的变量，否则容器里排查只能靠猜。
			if !strings.Contains(err.Error(), tc.wantVar) {
				t.Errorf("错误信息里应提到 %s，实际是: %v", tc.wantVar, err)
			}
		})
	}
}

// TestDSN 验证连接串拼接。
//
// 拼错的 DSN 会表现为「连不上数据库」，很难一眼看出是格式问题。
func TestDSN(t *testing.T) {
	db := DatabaseConfig{
		Host: "postgres", Port: 5432, User: "u",
		Password: "p", Name: "n", SSLMode: "disable",
	}
	want := "host=postgres port=5432 user=u password=p dbname=n sslmode=disable"
	if got := db.DSN(); got != want {
		t.Errorf("DSN 拼接不对:\n得到 %q\n期望 %q", got, want)
	}
}

// TestServerAddr 验证监听地址。
func TestServerAddr(t *testing.T) {
	s := ServerConfig{Host: "0.0.0.0", Port: 8080}
	if got := s.Addr(); got != "0.0.0.0:8080" {
		t.Errorf("Addr() = %q", got)
	}
}

// TestEnvDurationFallback 验证超时类的环境变量解析。
//
// 写错格式时应当静默退回默认值而不是让服务起不来——这些是可调参数，
// 不值得为它们中断启动。
func TestEnvDurationFallback(t *testing.T) {
	env := validEnv()
	env["CC_STREAM_IDLE"] = "not-a-duration"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("超时格式错误不应导致启动失败: %v", err)
	}
	if cfg.CommandCC.StreamIdleTimeout.Seconds() != 30 {
		t.Errorf("应退回默认 30s，得到 %v", cfg.CommandCC.StreamIdleTimeout)
	}

	env["CC_STREAM_IDLE"] = "45s"
	setEnv(t, env)
	cfg, _ = Load()
	if cfg.CommandCC.StreamIdleTimeout.Seconds() != 45 {
		t.Errorf("应解析为 45s，得到 %v", cfg.CommandCC.StreamIdleTimeout)
	}
}
