package relay

import (
	"strings"
	"testing"
)

// hardcodedModels 是脚本从 CLI 包体里抠出来的（scripts/refresh_models.py），
// 没经过人手，所以这里挡一下机器生成最容易出的两种错：抠重了、抠成了空壳。
func TestHardcodedModelsAreWellFormed(t *testing.T) {
	if len(hardcodedModels) < 20 {
		t.Fatalf("兜底表只剩 %d 条，像是被 refresh_models.py 抠坏了", len(hardcodedModels))
	}

	seen := make(map[string]string, len(hardcodedModels))
	for i, m := range hardcodedModels {
		if m.ID == "" {
			t.Errorf("第 %d 条没有 id", i)
			continue
		}
		if m.Name == "" {
			t.Errorf("%s 没有 name", m.ID)
		}
		// 名字应当是给人看的，不是 id 的回声。上游表里 name 都是 "Claude Opus 5.5"
		// 这种；如果哪天抠到了 label 之外的字段，这里会先叫出来。
		if m.Name == m.ID {
			t.Errorf("%s 的 name 和 id 一模一样，可能抠错字段了", m.ID)
		}
		if strings.HasPrefix(m.Name, "{") || strings.Contains(m.Name, "hidden") {
			t.Errorf("%s 的 name 是 %q，抠到了源码片段而不是显示名", m.ID, m.Name)
		}
		if prev, dup := seen[m.ID]; dup {
			t.Errorf("%s 出现了两次（第 %s 条也是它）", m.ID, prev)
		}
		seen[m.ID] = m.ID
	}
}

// 兜底表是「动态列表拉不到」时用的，所以它至少要能覆盖住我们文档和 README 里
// 点名的那些模型——不然用户照着文档填 model 名，恰好赶上上游抽风，就会被拒。
func TestHardcodedModelsCoverDocumentedOnes(t *testing.T) {
	have := make(map[string]bool, len(hardcodedModels))
	for _, m := range hardcodedModels {
		have[m.ID] = true
	}
	for _, want := range []string{
		"claude-sonnet-5",
		"claude-opus-5-5",
		"gpt-6-sol",
		"xai/grok-4.7",
		"google/gemini-3.8-flash",
	} {
		if !have[want] {
			t.Errorf("兜底表里没有 %s", want)
		}
	}
}
