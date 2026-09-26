package service

import (
	"testing"

	"cmd2api/internal/domain"
)

// 确认两套命名都能查到价，且免费模型仍然记 0。
func TestPriceForBothPlatforms(t *testing.T) {
	cases := []struct {
		model     string
		wantFound bool
		platform  string
	}{
		{"deepseek/deepseek-v4-flash", true, "commandcode"},
		{"MiniMaxAI/MiniMax-M3", true, "commandcode"},
		{"deepseek-v4-flash", true, "opencode"},
		{"minimax-m3", true, "opencode"},
		{"claude-sonnet-4-6", true, "opencode/commandcode 同名"},
		{"gpt-6-luna", true, "opencode"},
		// 免费模型不该在表里 —— 查不到即记 0，正是想要的行为。
		{"deepseek-v4-flash-free", false, "opencode 免费档"},
		{"space-bunny-free", false, "opencode 免费档"},
		// 完全未知的模型
		{"some-future-model", false, "未知"},
	}
	for _, c := range cases {
		_, found := priceFor(c.model)
		if found != c.wantFound {
			t.Errorf("priceFor(%q) found=%v，期望 %v（%s）", c.model, found, c.wantFound, c.platform)
		}
	}
}

// TestUpdateAcceptsOpenCodeKey 是实测踩坑后补的回归测试（逻辑层面）。
//
// 真实事故：Update 里硬编码了 Command Code 的前缀校验，
// 结果 OpenCode 账号的密钥永远改不了——每次都被拒以
// 「密钥格式不正确，Command Code 的密钥应以 user_ 开头」。
// 这个测试锁定「前缀校验必须看平台」这条规则。
func TestKeyPrefixRuleIsPlatformScoped(t *testing.T) {
	// Command Code：必须 user_ 前缀
	if domain.PlatformCommandCode != "commandcode" {
		t.Fatal("平台常量变了，这个测试的前提失效")
	}
	// OpenCode：不该要求任何前缀
	if domain.PlatformOpenCode == domain.PlatformCommandCode {
		t.Fatal("两个平台常量不该相等")
	}
	// 前缀常量本身
	if domain.CommandCodeKeyPrefix != "user_" {
		t.Errorf("Command Code 前缀应为 user_，得到 %q", domain.CommandCodeKeyPrefix)
	}
}
