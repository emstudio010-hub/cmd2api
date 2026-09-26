package handler

import (
	"strings"
	"testing"
)

// authFileSample 是一份 ~/.commandcode/auth.json 的真实形状。
//
// 字段名对着 CLI 实际写出来的文件（storeCommandAuthCredentials），
// 不是我以为的。密钥是编的——真密钥不进测试文件。
const authFileSample = `{
  "apiKey": "user_aaaaaaaaaaaaaaaaaaaaaaaa",
  "userId": "u_123",
  "userName": "alice",
  "keyName": "cli",
  "authenticatedAt": "2026-09-26T10:00:00.000Z"
}`

func TestParseKeyLinesPlainList(t *testing.T) {
	lines := parseKeyLines(strings.Join([]string{
		"# 这是注释",
		"",
		"user_key_one",
		"主力账号,user_key_two",
		"   ", // 只有空格
	}, "\n"))

	if len(lines) != 2 {
		t.Fatalf("应当解析出 2 条，实际 %d: %+v", len(lines), lines)
	}
	if lines[0].Key != "user_key_one" || lines[0].Name != "" {
		t.Errorf("第一条不对: %+v", lines[0])
	}
	if lines[1].Name != "主力账号" || lines[1].Key != "user_key_two" {
		t.Errorf("第二条不对: %+v", lines[1])
	}
}

// TestParseKeyLinesNameWithComma 守住「按最后一个逗号切」这条规则。
//
// 账号名里带逗号是很自然的写法，按第一个逗号切会把名字腰斩。
func TestParseKeyLinesNameWithComma(t *testing.T) {
	lines := parseKeyLines("华东,备用,user_key")
	if len(lines) != 1 {
		t.Fatalf("应当解析出 1 条，实际 %d", len(lines))
	}
	if lines[0].Name != "华东,备用" {
		t.Errorf("名称 = %q，期望 华东,备用", lines[0].Name)
	}
	if lines[0].Key != "user_key" {
		t.Errorf("密钥 = %q", lines[0].Key)
	}
}

func TestParseAuthFileSingleObject(t *testing.T) {
	lines := parseKeyLines(authFileSample)
	if len(lines) != 1 {
		t.Fatalf("应当解析出 1 条，实际 %d: %+v", len(lines), lines)
	}
	if lines[0].Key != "user_aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("密钥取错了: %q", lines[0].Key)
	}
	// 用户名加密钥名，同一个人的多把密钥靠后半段区分。
	if lines[0].Name != "alice · cli" {
		t.Errorf("名称 = %q，期望 alice · cli", lines[0].Name)
	}
}

func TestParseAuthFileArray(t *testing.T) {
	raw := `[
	  {"apiKey":"user_one","userName":"alice","keyName":"cli"},
	  {"apiKey":"user_two","userName":"bob","keyName":"desktop"}
	]`
	lines := parseKeyLines(raw)
	if len(lines) != 2 {
		t.Fatalf("应当解析出 2 条，实际 %d: %+v", len(lines), lines)
	}
	if lines[0].Key != "user_one" || lines[1].Key != "user_two" {
		t.Errorf("密钥顺序不对: %+v", lines)
	}
	if lines[1].Name != "bob · desktop" {
		t.Errorf("名称 = %q", lines[1].Name)
	}
}

// TestParseAuthFileConcatenated 守住「几个文件首尾相接」这种贴法。
//
// 这不是合法 JSON，整段 Unmarshal 会直接失败——但人手粘贴时就是这么干的，
// 一次贴五个账号比一个一个贴自然得多。
func TestParseAuthFileConcatenated(t *testing.T) {
	raw := `{"apiKey":"user_one","userName":"alice"}
{"apiKey":"user_two","userName":"bob"}
{"apiKey":"user_three"}`
	lines := parseKeyLines(raw)
	if len(lines) != 3 {
		t.Fatalf("应当解析出 3 条，实际 %d: %+v", len(lines), lines)
	}
	if lines[2].Key != "user_three" {
		t.Errorf("第三条密钥 = %q", lines[2].Key)
	}
	// 只有 apiKey 时名称留空，交给下游按密钥前缀自动起名。
	if lines[2].Name != "" {
		t.Errorf("缺 userName 时名称应当为空，实际 %q", lines[2].Name)
	}
}

// TestParseAuthFileSkipsBracesInStrings 守住字符串里的花括号。
//
// 按花括号配对来切，只要不在字符串状态里跳过括号，一个带 } 的字段值就能
// 把后面所有条目带偏——而且是那种「解析出一堆莫名其妙的东西」的偏。
func TestParseAuthFileSkipsBracesInStrings(t *testing.T) {
	raw := `{"apiKey":"user_one","userName":"a}b{c"}
{"apiKey":"user_two"}`
	lines := parseKeyLines(raw)
	if len(lines) != 2 {
		t.Fatalf("应当解析出 2 条，实际 %d: %+v", len(lines), lines)
	}
	if lines[0].Key != "user_one" || lines[1].Key != "user_two" {
		t.Errorf("密钥不对: %+v", lines)
	}
	if lines[0].Name != "a}b{c" {
		t.Errorf("名称 = %q，期望 a}b{c", lines[0].Name)
	}
}

// TestParseAuthFileEscapedQuote 守住转义引号。
//
// 用户名里带引号时不处理转义，字符串状态会提前结束，后面的括号就全乱了。
func TestParseAuthFileEscapedQuote(t *testing.T) {
	raw := `{"apiKey":"user_one","userName":"a\"b"}
{"apiKey":"user_two"}`
	lines := parseKeyLines(raw)
	if len(lines) != 2 {
		t.Fatalf("应当解析出 2 条，实际 %d: %+v", len(lines), lines)
	}
	if lines[1].Key != "user_two" {
		t.Errorf("第二条密钥 = %q", lines[1].Key)
	}
}

// TestParseAuthFileBadEntryDoesNotPoisonOthers 确认坏条目只坏自己。
//
// 一个坏条目连累整批，是最让人恼火的失败方式：操作员不得不逐个文件去试，
// 才知道是哪一个有问题。
func TestParseAuthFileBadEntryDoesNotPoisonOthers(t *testing.T) {
	raw := `{"apiKey":"user_one"}
{"apiKey": BROKEN}
{"apiKey":"user_three"}`
	lines := parseKeyLines(raw)

	// 坏的那条会以空密钥的形式留下来，交给下游按「密钥不能为空」报错，
	// 这样失败原因能跟其它情况走同一条回显路径。
	if len(lines) != 3 {
		t.Fatalf("应当保留 3 个位置，实际 %d: %+v", len(lines), lines)
	}
	if lines[0].Key != "user_one" || lines[2].Key != "user_three" {
		t.Errorf("好条目被坏条目连累了: %+v", lines)
	}
	if lines[1].Key != "" {
		t.Errorf("坏条目应当留下空密钥，实际 %q", lines[1].Key)
	}
}

// TestParseAuthFileMissingAPIKey 确认缺 apiKey 的条目不会被当成有效账号。
func TestParseAuthFileMissingAPIKey(t *testing.T) {
	lines := parseKeyLines(`{"userId":"u_1","userName":"alice"}`)
	if len(lines) != 1 {
		t.Fatalf("应当解析出 1 条，实际 %d", len(lines))
	}
	if lines[0].Key != "" {
		t.Errorf("缺 apiKey 时密钥应当为空，实际 %q", lines[0].Key)
	}
}

// TestParseKeyLinesDoesNotTreatKeysAsJSON 确认普通密钥列表不受影响。
//
// looksLikeAuthFile 只看首字符。user_ 开头的密钥永远不会撞上花括号，
// 但这条要在测试里钉住，免得哪天有人把判断放宽成「含 { 就当 JSON」。
func TestParseKeyLinesDoesNotTreatKeysAsJSON(t *testing.T) {
	lines := parseKeyLines("user_a{bc\nalice,user_def")
	if len(lines) != 2 {
		t.Fatalf("应当按行解析出 2 条，实际 %d: %+v", len(lines), lines)
	}
	if lines[0].Key != "user_a{bc" {
		t.Errorf("密钥 = %q", lines[0].Key)
	}
}

// TestSplitJSONObjectsNested 确认嵌套对象不会被拆散。
//
// auth.json 顶层不带嵌套，但数组形式的贴法里可能有（比如带 org 信息
// 的账号），配对一乱就会切出残片。
func TestSplitJSONObjectsNested(t *testing.T) {
	blocks := splitJSONObjects(`{"a":{"b":{"c":1}},"d":2}{"e":3}`)
	if len(blocks) != 2 {
		t.Fatalf("应当切出 2 块，实际 %d: %+v", len(blocks), blocks)
	}
	if blocks[0] != `{"a":{"b":{"c":1}},"d":2}` {
		t.Errorf("第一块 = %q", blocks[0])
	}
	if blocks[1] != `{"e":3}` {
		t.Errorf("第二块 = %q", blocks[1])
	}
}

// TestSplitJSONObjectsTruncated 确认截断的输入不会切出半个对象。
//
// 用户复制到一半就粘上来了，这时应当什么都不返回（而不是返回一个残片
// 让 json 解析报一个看不懂的错）。
func TestSplitJSONObjectsTruncated(t *testing.T) {
	if blocks := splitJSONObjects(`{"apiKey":"user_one"`); len(blocks) != 0 {
		t.Fatalf("截断的输入不该切出对象，实际 %+v", blocks)
	}
}
