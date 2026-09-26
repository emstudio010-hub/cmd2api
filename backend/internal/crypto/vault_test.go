package crypto

import (
	"strings"
	"testing"
)

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

// TestEncryptDecryptRoundTrip 验证最基本的往返。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	v, err := NewVault(testKey())
	if err != nil {
		t.Fatalf("构造 Vault 失败: %v", err)
	}

	for _, plain := range []string{
		"user_abcdef1234567890",
		"",
		"含中文的凭证",
		strings.Repeat("x", 10000),
	} {
		enc, err := v.Encrypt(plain)
		if err != nil {
			t.Fatalf("加密 %q 失败: %v", plain, err)
		}
		got, err := v.Decrypt(enc)
		if err != nil {
			t.Fatalf("解密 %q 失败: %v", plain, err)
		}
		if got != plain {
			t.Errorf("往返后不一致: 得到 %q，期望 %q", got, plain)
		}
	}
}

// TestEncryptIsNonDeterministic 验证同一明文两次加密得到不同密文。
//
// 靠随机 nonce 实现。如果密文可预测，攻击者就能看出「两个账号用了同一把密钥」。
func TestEncryptIsNonDeterministic(t *testing.T) {
	v, _ := NewVault(testKey())
	const plain = "user_same_key"

	first, _ := v.Encrypt(plain)
	second, _ := v.Encrypt(plain)
	if first == second {
		t.Error("同一明文两次加密产生了相同密文，nonce 可能没随机")
	}
	// 但都必须能解回原文。
	for _, enc := range []string{first, second} {
		if got, _ := v.Decrypt(enc); got != plain {
			t.Errorf("解密结果不对: %q", got)
		}
	}
}

// TestDecryptRejectsTampered 验证密文被改动后会解密失败而不是返回垃圾。
//
// 这是选 GCM 而不是 CBC 的原因：GCM 自带完整性校验。
// 少了它，一个字节的损坏会被悄悄解成另一段看似合法的明文。
func TestDecryptRejectsTampered(t *testing.T) {
	v, _ := NewVault(testKey())
	enc, _ := v.Encrypt("user_secret_value")

	// 翻转最后一个字符，制造损坏密文。
	raw := []byte(enc)
	if raw[len(raw)-1] == 'A' {
		raw[len(raw)-1] = 'B'
	} else {
		raw[len(raw)-1] = 'A'
	}
	if _, err := v.Decrypt(string(raw)); err == nil {
		t.Error("被篡改的密文应当解密失败")
	}
}

// TestDecryptRejectsGarbage 验证乱七八糟的输入不会 panic。
func TestDecryptRejectsGarbage(t *testing.T) {
	v, _ := NewVault(testKey())
	for _, bad := range []string{"", "not-base64!!!", "YWJj", strings.Repeat("A", 100)} {
		if _, err := v.Decrypt(bad); err == nil {
			t.Errorf("输入 %q 应当报错", bad)
		}
	}
}

// TestVaultRejectsWrongKeySize 验证密钥长度被强制校验。
//
// AES-256 必须要 32 字节。放行错误长度会让「配置写错」变成运行期才暴露的问题。
func TestVaultRejectsWrongKeySize(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33, 64} {
		key := make([]byte, size)
		if _, err := NewVault(key); err == nil {
			t.Errorf("%d 字节的密钥应当被拒绝", size)
		}
	}
}

// TestCrossVaultDecryptFails 验证换了密钥就解不出旧密文。
//
// 这条同时是对运维的提醒：ENCRYPTION_KEY 换了，已录入的账号就全废了。
func TestCrossVaultDecryptFails(t *testing.T) {
	first, _ := NewVault(testKey())
	other := make([]byte, 32)
	for i := range other {
		other[i] = byte(255 - i)
	}
	second, _ := NewVault(other)

	enc, _ := first.Encrypt("user_value")
	if _, err := second.Decrypt(enc); err == nil {
		t.Error("用另一把密钥解密应当失败")
	}
}

// TestMaskKey 验证打码只保留首尾、且不泄露中间内容。
func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"user_abcdefghijklmn": "user****klmn",
		"short":               "****",
		"user_x":              "****",
	}
	for in, want := range cases {
		if got := MaskKey(in); got != want {
			t.Errorf("MaskKey(%q) = %q，期望 %q", in, got, want)
		}
	}

	// 打码结果绝不能包含原文中段。
	masked := MaskKey("user_secretmiddle_part_xyz")
	if strings.Contains(masked, "secretmiddle") {
		t.Errorf("打码后仍泄露中段内容: %q", masked)
	}
}
