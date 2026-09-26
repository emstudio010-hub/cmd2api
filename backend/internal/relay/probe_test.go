package relay

import (
	"strings"
	"testing"
)

// realProbeStream 是从真实 Command Code 上游抓下来的探活响应。
//
// 用真实数据而不是构造的假数据：探活的判定逻辑曾经在这里出过一次错
// （明明有 reasoning-delta 内容却报「没有生成内容」），构造的假样本
// 恰好绕开了出问题的那个形状。
//
// 第 02 行刻意保留了上游回显的完整请求体——它就是当初把读取预算吃掉的元凶。
var realProbeStream = []string{
	`{"type":"start"}`,
	`{"type":"start-step","request":{"body":{"maxOutputTokens":1,"prompt":[{"role":"user","content":[{"type":"text","text":"ok"}]}],"providerOptions":{"gateway":{"safetyIdentifier":"cmd2api"}}},"url":"https://api.commandcode.ai/alpha/generate"}}`,
	`{"type":"reasoning-start","id":"reasoning-0","providerMetadata":{"gateway":{"generationId":"gen_01M3E5K5NM3R3SCKG98H9MS2EZ"}}}`,
	`{"type":"reasoning-delta","id":"reasoning-0","text":"The"}`,
	`{"type":"reasoning-end","id":"reasoning-0"}`,
	`{"type":"finish-step","finishReason":"length","rawFinishReason":"length","usage":{"inputTokens":32,"inputTokenDetails":{"noCacheTokens":32,"cacheReadTokens":0},"outputTokens":1}}`,
	`{"type":"finish","finishReason":"length","rawFinishReason":"length","totalUsage":{"inputTokens":32,"inputTokenDetails":{"noCacheTokens":32,"cacheReadTokens":0},"outputTokens":1}}`,
	`{"type":"provider-metadata","providerMetadata":{"deepseek":{"promptCacheHitTokens":0,"promptCacheMissTokens":32}}}`,
}

// TestScanForStreamErrorAcceptsRealProbe 用真实响应验证探活判定。
//
// max_tokens=1 的探活请求，上游只会吐一小段 reasoning 就 finish。
// 判定逻辑必须认这是「账号可用」——把能用的账号判成不可用，
// 会导致健康检查把所有账号逐个自动禁用。
func TestScanForStreamErrorAcceptsRealProbe(t *testing.T) {
	body := strings.NewReader(strings.Join(realProbeStream, "\n"))

	if err := scanForStreamError(body); err != nil {
		t.Fatalf("真实探活响应应判定为可用，却报了错: %s", err.Message)
	}
}

// TestScanForStreamErrorDetectsRealError 验证流内的错误事件仍能被抓到。
func TestScanForStreamErrorDetectsRealError(t *testing.T) {
	stream := `{"type":"start"}
{"type":"error","error":{"message":"<429> rate limited","code":"RATE_LIMITED","statusCode":429}}`
	body := strings.NewReader(stream)

	err := scanForStreamError(body)
	if err == nil {
		t.Fatal("流内错误事件应被识别")
	}
	if !err.IsRateLimit() {
		t.Errorf("应识别为限流，得到 %+v", err)
	}
}

// TestScanForStreamErrorRejectsEmptyStream 验证「确实什么都没有」仍算失败。
//
// 这两件事必须区分开：上游说成功但一个字都没生成（异常），
// 和上游说成功且生成了内容（正常）。前者要报错，后者不能报错。
func TestScanForStreamErrorRejectsEmptyStream(t *testing.T) {
	stream := `{"type":"start"}
{"type":"start-step","request":{"body":{}}}`

	if err := scanForStreamError(strings.NewReader(stream)); err == nil {
		t.Error("只有开始事件、没有任何内容时应判定为失败")
	}
}

// TestScanForStreamErrorHandlesHugeEchoLine 验证超长的请求回显行不会吃掉读取预算。
//
// 上游在 start-step 里回显整个请求体。客户端发大 payload（长对话、图片）时，
// 这一行可能有几百 KB——如果按行读并且预算被它吃光，
// 后面的内容事件就再也读不到了，探活会误判为失败。
func TestScanForStreamErrorHandlesHugeEchoLine(t *testing.T) {
	// 构造一行远超读取预算的请求回显。
	huge := strings.Repeat("x", 200*1024)
	stream := `{"type":"start-step","request":{"body":{"prompt":"` + huge + `"}}}` + "\n" +
		`{"type":"text-delta","text":"ok"}` + "\n" +
		`{"type":"finish","finishReason":"stop"}`

	if err := scanForStreamError(strings.NewReader(stream)); err != nil {
		t.Fatalf("超长回显行之后的内容仍应被读到，却报了错: %s", err.Message)
	}
}
