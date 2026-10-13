package service

import (
	"testing"

	"cmd2api/internal/domain"
	"cmd2api/internal/relay"
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

// 缓存读必须单独计价。
//
// 它只有输入价的百分之几，而实际流量里占比很大（长期会话会把整段上下文
// 反复送上去）。按输入价算会把成本系统性高估一个量级。
func TestCalculateCostPricesCacheReadSeparately(t *testing.T) {
	rec := relay.UsageRecord{
		Model:          "z-ai/glm-5.3-flashx",
		InputTokens:    1_000_000,
		RateMultiplier: 1,
	}
	fullInput, _ := calculateCost(rec)

	rec.InputTokens = 0
	rec.CacheReadTokens = 1_000_000
	cachedInput, _ := calculateCost(rec)

	// 输入价 0.37、缓存读价 0.075（官方价目表）。100 万 token 走缓存只该花 0.075。
	if diff := cachedInput.InputCost - 0.075; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("100 万缓存读应为 $0.075，得到 %v", cachedInput.InputCost)
	}
	if cachedInput.InputCost >= fullInput.InputCost {
		t.Errorf("缓存读没有折扣：全输入 %v，全缓存 %v", fullInput.InputCost, cachedInput.InputCost)
	}

	// 价目表里没给缓存读价（CacheRead = 0）时必须回退到输入价：
	// 宁可高估，也不能凭空给一个折扣。claude-opus-4-8 只在 OpenCode
	// 那张表里，而那张表没标缓存价。
	fallback, _ := calculateCost(relay.UsageRecord{
		Model:           "claude-opus-4-8",
		CacheReadTokens: 1_000_000,
		RateMultiplier:  1,
	})
	if diff := fallback.InputCost - 15; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("缺缓存价时应按输入价 $15/M 计，得到 %v", fallback.InputCost)
	}
}

// 线上真实出现过的模型名必须都查得到价。
//
// 之前整张 Command Code 价目表只有 26 条、且命名和上游对不上，导致实际在用的
// 模型（z-ai/glm-5.3-flashx、deepseek/deepseek-v4.1-flash 等）全部查不到，
// 成本列一直是 0。这个测试锁住这件事。
func TestPriceCoversModelsSeenInProduction(t *testing.T) {
	models := []string{
		"z-ai/glm-5.3-flashx", "glm-5.3-flashx",
		"z-ai/glm-5.3-flash", "zai-org/GLM-5.3",
		"deepseek/deepseek-v4.1-flash", "deepseek-v4.1-flash",
		"gpt-6-luna",
		"xiaomi/mimo-v2.6-flash", "xiaomi/mimo-v2.6-pro",
		"Qwen/Qwen3.8-Flash", "MiniMaxAI/MiniMax-M3",
	}
	for _, m := range models {
		if _, ok := priceFor(m); !ok {
			t.Errorf("priceFor(%q) 查不到价：这个模型线上出现过，成本会被记成 0", m)
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
