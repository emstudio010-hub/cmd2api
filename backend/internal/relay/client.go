package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"cmd2api/internal/config"
)

// 与 commandcode-proxy 对齐的时序常量。
const (
	// sessionDuration 是一个伪造会话的有效期。CLI 真机上会话不会短到分钟级，
	// 太频繁地换 session 本身就是异常信号。
	sessionDuration = 12 * time.Hour
	sessionJitter   = 1 * time.Hour

	// initRefresh 是「指纹 + 生命周期」两个预请求的重发周期。
	initRefresh = 8 * time.Hour
	initJitter  = 2 * time.Hour
)

// protocolVersion 是本实现**实际对齐**的 CLI wire 协议版本。
//
// 刻意写死而不是跟着 npm 上跑：真机发出去的永远是「形状 + 版本号自洽」的组合。
// 如果版本号跟着 npm 涨、形状却没改，就变成「自称最新版、却说旧方言」——
// 这比版本号略旧更容易被行为分析挑出来。升级前先读包对齐，再改这个常量。
//
// 1.66.0 已逐项比对过（command-code@1.66.0 的 dist/cli.mjs），形状没有变化：
//   - 头：Content-Type / User-Agent "cli" / x-command-code-version /
//     x-cli-environment / x-project-slug / x-taste-learning / x-session-id /
//     Authorization: Bearer，与 buildCommandAuthHeaders 一致。
//     1.66.0 新增的 x-oauth-token、x-oauth-provider、x-oss-primary-provider、
//     x-cmd-zdr、x-cmd-provider-deepseek-internal 都是条件发送的
//     （OAuth 登录、BYOK、ZDR、内部 provider），走 API key 这条路径不带。
//   - 会话 ID：sess_ + UUID 去横线后的前 16 位（generateSessionId）。
//   - 指纹体：{thumbmark, components}，盐 "command-code:device-fingerprint:v1"，
//     collectorVersion 1，runtime "cli"。
//   - 生命周期事件：{eventType:"cli_session_exists", metadata:{sessionId,
//     cliVersion, mode, os}}。
//   - 路径：/alpha/generate、/alpha/fingerprint/record、/alpha/lifecycle-events、
//     /alpha/billing/credits 全部未变。
//
// 唯一改掉的是会话 ID 的形状：原来 sessionFor 直接发一个带横线的 UUID，
// 而 CLI 发的是 sess_ 前缀那种。这个比版本号显眼得多。
const protocolVersion = "1.66.0"

// hardcodedModels 是拿不到动态模型列表时的兜底：GET /provider/v1/models 拉不到、
// 或者账号还没探活时用它，免得客户端拿到一个空列表。
//
// 内容直接抄自 CLI 自己的模型目录（command-code@1.66.0 的 dist/cli.mjs 里那张
// {SONNET_5:{id,name,...}} 表），按上游声明的顺序原样排列，只去掉带 hidden 的条目。
// 上一版是手挑的 26 条，一个版本就被甩下了（Opus 5.5、GPT-6 Luna/Sol、
// Grok 4.7 全没跟上），所以这次不手挑：整表照搬。升 CLI 时重跑
//
//	python backend/scripts/refresh_models.py
//
// 把它打出来的块换到下面即可。这是数据不是协议形状，不影响 wire 兼容性。
var hardcodedModels = []Model{
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5"},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	{ID: "claude-fable-5-1", Name: "Claude Fable 5.1"},
	{ID: "claude-fable-5", Name: "Claude Fable 5"},
	{ID: "claude-opus-5-5", Name: "Claude Opus 5.5"},
	{ID: "claude-opus-5", Name: "Claude Opus 5"},
	{ID: "claude-opus-4-8", Name: "Claude Opus 4.8"},
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7"},
	{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5"},
	{ID: "gpt-6-astra", Name: "GPT-6 Astra"},
	{ID: "gpt-6-sol", Name: "GPT-6 Sol"},
	{ID: "gpt-6-luna", Name: "GPT-6 Luna"},
	{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol"},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra"},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna"},
	{ID: "gpt-5.5", Name: "GPT-5.5"},
	{ID: "gpt-5.4", Name: "GPT-5.4"},
	{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex"},
	{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini"},
	{ID: "deepseek/deepseek-v4-pro", Name: "DeepSeek V4 Pro (latest)"},
	{ID: "deepseek/deepseek-v4-flash", Name: "DeepSeek V4 Flash (latest)"},
	{ID: "deepseek/deepseek-v4-flash-vision-exp", Name: "DeepSeek V4 Flash Vision (exp)"},
	{ID: "deepseek/deepseek-v4-flash-fast", Name: "DeepSeek V4 Flash Fast"},
	{ID: "deepseek/deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash"},
	{ID: "moonshotai/Kimi-K3", Name: "Kimi K3"},
	{ID: "moonshotai/Kimi-K2.7-Code", Name: "Kimi K2.7 Code"},
	{ID: "moonshotai/Kimi-K2.7-Code-Highspeed", Name: "Kimi K2.7 Code HighSpeed"},
	{ID: "moonshotai/Kimi-K2.6", Name: "Kimi K2.6"},
	{ID: "moonshotai/Kimi-K2.5", Name: "Kimi K2.5"},
	{ID: "z-ai/glm-5.3-flash", Name: "GLM-5.3 Flash"},
	{ID: "z-ai/glm-5.3-flashx", Name: "GLM-5.3 FlashX"},
	{ID: "zai-org/GLM-5.3", Name: "GLM-5.3"},
	{ID: "zai-org/GLM-5.2", Name: "GLM-5.2"},
	{ID: "zai-org/GLM-5.2-Fast", Name: "GLM-5.2 Fast"},
	{ID: "zai-org/GLM-5.1", Name: "GLM-5.1"},
	{ID: "zai-org/GLM-5", Name: "GLM-5"},
	{ID: "MiniMaxAI/MiniMax-M3", Name: "MiniMax M3"},
	{ID: "MiniMaxAI/MiniMax-M2.7", Name: "MiniMax M2.7"},
	{ID: "MiniMaxAI/MiniMax-M2.5", Name: "MiniMax M2.5"},
	{ID: "xiaomi/mimo-v2.6-pro", Name: "MiMo V2.6 Pro"},
	{ID: "xiaomi/mimo-v2.6-pro-ultraspeed", Name: "MiMo V2.6 Pro UltraSpeed"},
	{ID: "xiaomi/mimo-v2.6-flash", Name: "MiMo V2.6 Flash"},
	{ID: "xiaomi/mimo-v2.5-pro", Name: "MiMo V2.5 Pro"},
	{ID: "xiaomi/mimo-v2.5", Name: "MiMo V2.5"},
	{ID: "Qwen/Qwen3.8-Omni-Flash", Name: "Qwen 3.8 Omni Flash"},
	{ID: "Qwen/Qwen3.8-Max-0902", Name: "Qwen 3.8 Max 0902"},
	{ID: "Qwen/Qwen3.8-Max", Name: "Qwen 3.8 Max"},
	{ID: "Qwen/Qwen3.8-27B", Name: "Qwen 3.8 27B"},
	{ID: "Qwen/Qwen3.8-Flash", Name: "Qwen 3.8 Flash"},
	{ID: "Qwen/Qwen3.7-Max", Name: "Qwen 3.7 Max"},
	{ID: "Qwen/Qwen3.7-Plus", Name: "Qwen 3.7 Plus"},
	{ID: "Qwen/Qwen3.7-Flash", Name: "Qwen 3.7 Flash"},
	{ID: "Qwen/Qwen3.6-Max-Preview", Name: "Qwen 3.6 Max Preview"},
	{ID: "Qwen/Qwen3.6-Plus", Name: "Qwen 3.6 Plus"},
	{ID: "meituan/LongCat-2.0", Name: "LongCat 2.0"},
	{ID: "stepfun/Step-5-Preview", Name: "Step 5 Preview"},
	{ID: "stepfun/Step-3.7-Flash", Name: "Step 3.7 Flash"},
	{ID: "stepfun/Step-3.5-Flash", Name: "Step 3.5 Flash"},
	{ID: "tencent/hy3-paid", Name: "Tencent Hy3"},
	{ID: "tencent/hy4-preview", Name: "Tencent Hy4 Preview"},
	{ID: "google/gemini-3.8-flash", Name: "Gemini 3.8 Flash"},
	{ID: "google/gemini-3.7-flash", Name: "Gemini 3.7 Flash"},
	{ID: "google/gemini-3.6-flash", Name: "Gemini 3.6 Flash"},
	{ID: "google/gemini-3.5-flash", Name: "Gemini 3.5 Flash"},
	{ID: "google/gemini-3.5-flash-lite", Name: "Gemini 3.5 Flash Lite"},
	{ID: "google/gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash Lite"},
	{ID: "sakana/fugu-ultra", Name: "Fugu Ultra"},
	{ID: "nvidia/nemotron-3-ultra-550b-a55b", Name: "Nemotron 3 Ultra"},
	{ID: "thinkingmachines/inkling", Name: "Inkling"},
	{ID: "thinkingmachines/inkling-small", Name: "Inkling Small"},
	{ID: "stealth/space-bunny-alpha", Name: "Space Bunny Alpha"},
	{ID: "stealth/pixel-canary", Name: "Pixel Canary"},
	{ID: "poolside/laguna-s-2.1-free", Name: "Laguna S 2.1"},
	{ID: "inclusionai/ling-3.0-flash-sante:free", Name: "Ling 3.0 Flash Sante"},
	{ID: "meta/muse-spark-1.1", Name: "Muse Spark 1.1"},
	{ID: "meta/muse-spark-1.2", Name: "Muse Spark 1.2"},
	{ID: "meta/muse-spark-1.2-contributor", Name: "Muse Spark 1.2 Contributor"},
	{ID: "meta/muse-spark-1.3", Name: "Muse Spark 1.3"},
	{ID: "meta/muse-spark-1.3-contributor", Name: "Muse Spark 1.3 Contributor"},
	{ID: "xai/grok-4.5", Name: "Grok 4.5"},
	{ID: "xai/grok-4.6", Name: "Grok 4.6"},
	{ID: "xai/grok-4.7", Name: "Grok 4.7"},
}

// Model 是对外暴露的模型条目。
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// keyState 是每个上游账号各自的会话/指纹状态。
//
// 状态按 apiKey 分组而不是按账号 ID：换绑账号时如果复用了同一把 key，
// 上游看到的仍应是同一台设备。
type keyState struct {
	fingerprint      *Fingerprint
	nextInitAt       time.Time
	sessionID        string
	sessionExpiresAt time.Time
}

// Client 是 Command Code 上游客户端。
type Client struct {
	cfg    config.CommandCodeConfig
	http   *http.Client
	logger *slog.Logger
	device DeviceProfile

	mu    sync.Mutex
	state map[string]*keyState

	modelsMu     sync.Mutex
	cachedModels []Model
	modelsAt     time.Time
}

// NewClient 构造上游客户端。
func NewClient(cfg config.CommandCodeConfig, logger *slog.Logger) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	// 走代理访问上游。刻意用显式配置而不是 http.ProxyFromEnvironment：
	// 后者会让服务悄悄继承宿主的 HTTP_PROXY，行为随部署环境漂移，
	// 出问题时很难看出是代理在起作用。
	if cfg.UpstreamProxy != "" {
		if proxyURL, err := url.Parse(cfg.UpstreamProxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
			logger.Info("上游请求将走代理", "proxy", redactProxyURL(cfg.UpstreamProxy))
		} else {
			// config 层已经校验过，走到这里说明校验和构造用的不是同一份值。
			logger.Error("上游代理地址无法解析，将直连", "err", err)
		}
	}

	return &Client{
		cfg:    cfg,
		logger: logger,
		device: DeviceProfileFor(""),
		state:  make(map[string]*keyState),
		http: &http.Client{
			// 不设 Client.Timeout：流式响应可以持续很久，整体超时会把
			// 正常的长回答掐断。空闲超时由看门狗单独管。
			Transport: transport,
		},
	}
}

// redactProxyURL 去掉代理 URL 里的用户名口令再打日志。
func redactProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(无法解析)"
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}

// keyStateFor 取（必要时创建）某个 key 的状态。
func (c *Client) keyStateFor(apiKey string) *keyState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.state[apiKey]
	if !ok {
		st = &keyState{
			fingerprint: GenerateFingerprint(apiKey, c.cfg.FingerprintSalt, ""),
		}
		c.state[apiKey] = st
	}
	return st
}

// ForgetKey 在账号被删除时清掉它的状态，避免无界增长。
func (c *Client) ForgetKey(apiKey string) {
	c.mu.Lock()
	delete(c.state, apiKey)
	c.mu.Unlock()
}

// sessionFor 返回该 key 的会话 ID，过期则新建。
func (c *Client) sessionFor(apiKey string) string {
	st := c.keyStateFor(apiKey)
	c.mu.Lock()
	defer c.mu.Unlock()
	if st.sessionID != "" && time.Now().Before(st.sessionExpiresAt) {
		return st.sessionID
	}
	st.sessionID = newSessionID()
	st.sessionExpiresAt = time.Now().Add(sessionDuration + randomJitter(sessionJitter))
	return st.sessionID
}

// newSessionID 按 CLI 的格式造一个会话 ID。
//
// CLI 是 `sess_` + UUID 去掉横线后的前 16 位十六进制（generateSessionId）。
// 这里必须照着来：会话 ID 出现在每一个请求的 x-session-id 头上，形状不对
// 是一条一眼就能看出来的差异——比版本号旧明显得多。
func newSessionID() string {
	compact := strings.ReplaceAll(randomUUID(), "-", "")
	if len(compact) > 16 {
		compact = compact[:16]
	}
	return "sess_" + compact
}

// resolveSessionID 决定本次请求用哪个会话 ID。
//
// 优先采用客户端自带的会话标识：同一个 CLI 会话在多次请求间保持同一个
// session 是正常行为，由 cmd2api 擅自重新分配反而制造出「每次请求都是新会话」
// 的异常模式。只有客户端没给才按 key 兜底。
func (c *Client) resolveSessionID(apiKey string, hints ...string) string {
	for _, h := range hints {
		// 太短的标识不像会话 ID，忽略。
		if len(h) >= 8 {
			return h
		}
	}
	return c.sessionFor(apiKey)
}

// initWaitBudget 是预请求最多让生成请求等多久。
//
// 正常路径下这两个请求几十毫秒就结束，这个上限只是兜底：它们是辅助信号，
// 绝不能因为上游慢而把用户的生成请求一起拖住。
const initWaitBudget = 3 * time.Second

// initRetryBackoff 是预请求失败后的重试间隔。
//
// 不能像成功那样等 8 小时——失败了就该早点再试，否则这段时间里所有生成请求
// 都缺少「这个账号的 CLI 活着」这个信号。也不能每次请求都重试，那会在上游
// 持续不可用时形成请求风暴。
const initRetryBackoff = 60 * time.Second

// EnsureInitialized 按需发送两个预请求：上报设备指纹 + 上报一次生命周期事件。
//
// 上游把这两个请求当成「CLI 装好并跑起来过」的证据，缺了会让后续生成请求
// 像凭空出现。节流到 8 小时一次并带抖动，避免多个账号在同一时刻齐刷刷上报。
func (c *Client) EnsureInitialized(ctx context.Context, apiKey string) {
	st := c.keyStateFor(apiKey)
	now := time.Now()

	c.mu.Lock()
	if now.Before(st.nextInitAt) {
		c.mu.Unlock()
		return
	}
	// 先在锁内把下次时间推后：并发的多个请求会同时看到「该上报了」，
	// 不在锁内推进就会造成重复上报。
	st.nextInitAt = now.Add(initRefresh + randomJitter(initJitter))
	fp := st.fingerprint
	c.mu.Unlock()

	// 预请求带的会话 ID 必须和生成请求用同一个。
	//
	// CLI 全程只有一个会话 ID（startSession 生成一次，getSessionId 到处复用），
	// 生命周期事件里报一个、请求头上又是另一个，等于凭空多出一个会话——
	// 这正是「机器在冒充 CLI」才有的痕迹。
	sessionID := c.sessionFor(apiKey)
	headers := c.baseHeaders(apiKey, sessionID)

	fpBody, err := json.Marshal(fp)
	if err != nil {
		c.logger.Warn("序列化设备指纹失败", "err", err)
		return
	}

	lifecycle := map[string]any{
		"eventType": "cli_session_exists",
		"metadata": map[string]any{
			"sessionId":  sessionID,
			"cliVersion": protocolVersion,
			"mode":       "interactive",
			"os":         fp.Components.Platform + "-" + fp.Components.Arch,
		},
	}
	lcBody, err := json.Marshal(lifecycle)
	if err != nil {
		c.logger.Warn("序列化生命周期事件失败", "err", err)
		return
	}

	// 用脱离的上下文：预请求是辅助信息，不该因为客户端提前断开或本次请求
	// 结束而被取消。已经发出的请求让它发完，上游那边的记录才完整。
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)

	result := make(chan bool, 1)
	go func() {
		defer cancel()
		var wg sync.WaitGroup
		okFp, okLc := true, true
		wg.Add(2)
		go func() {
			defer wg.Done()
			okFp = c.post(postCtx, "/alpha/fingerprint/record", headers, fpBody, "上报设备指纹")
		}()
		go func() {
			defer wg.Done()
			okLc = c.post(postCtx, "/alpha/lifecycle-events", headers, lcBody, "上报生命周期事件")
		}()
		wg.Wait()
		result <- okFp && okLc
	}()

	timer := time.NewTimer(initWaitBudget)
	defer timer.Stop()

	select {
	case ok := <-result:
		if !ok {
			// 失败就把下次时间提前，让后续请求早点再试。
			c.mu.Lock()
			if st.nextInitAt.After(time.Now().Add(initRetryBackoff)) {
				st.nextInitAt = time.Now().Add(initRetryBackoff)
			}
			c.mu.Unlock()
			c.logger.Debug("预请求失败，稍后重试")
		}
	case <-timer.C:
		// 超时：不阻塞用户请求，后台那次尝试继续跑完。
		c.logger.Debug("预请求超时，不阻塞生成请求")
	case <-ctx.Done():
	}
}

// Generate 发起一次生成请求，返回未读的响应体供调用方按 NDJSON 解析。
//
// 调用方负责关闭返回的 body。
func (c *Client) Generate(ctx context.Context, apiKey string, sessionHints []string, body []byte) (*http.Response, error) {
	sessionID := c.resolveSessionID(apiKey, sessionHints...)

	h := http.Header{}
	h.Set("Content-Type", "application/json")
	// CLI 的 User-Agent 就是字面量 "cli"，不是产品名+版本。
	h.Set("User-Agent", "cli")
	h.Set("x-command-code-version", protocolVersion)
	h.Set("x-cli-environment", "production")
	h.Set("x-project-slug", slugifyProjectPath(c.device.ProjectDir))
	h.Set("x-taste-learning", "false")
	h.Set("x-session-id", sessionID)
	h.Set("Authorization", "Bearer "+apiKey)
	h.Set("traceparent", generateTraceparent())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/alpha/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造生成请求: %w", err)
	}
	req.Header = h

	return c.http.Do(req)
}

// Models 取模型列表：优先动态拉取，失败退回内置列表。
//
// 结果按 ModelCacheTTL 缓存，失败也缓存——否则上游一挂，每个请求都要
// 等一次 10 秒超时。
func (c *Client) Models(ctx context.Context, apiKey string) []Model {
	c.modelsMu.Lock()
	if len(c.cachedModels) > 0 && time.Since(c.modelsAt) < c.cfg.ModelCacheTTL {
		cached := c.cachedModels
		c.modelsMu.Unlock()
		return cached
	}
	c.modelsMu.Unlock()

	models := c.fetchModels(ctx, apiKey)

	c.modelsMu.Lock()
	c.cachedModels = models
	c.modelsAt = time.Now()
	c.modelsMu.Unlock()
	return models
}

// InvalidateModels 清掉模型缓存，供后台手动刷新。
func (c *Client) InvalidateModels() {
	c.modelsMu.Lock()
	c.cachedModels = nil
	c.modelsAt = time.Time{}
	c.modelsMu.Unlock()
}

func (c *Client) fetchModels(ctx context.Context, apiKey string) []Model {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, c.cfg.BaseURL+"/provider/v1/models", nil)
	if err != nil {
		c.logger.Warn("构造模型列表请求失败", "err", err)
		return hardcodedModels
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-cli-environment", "production")
	req.Header.Set("x-command-code-version", protocolVersion)
	// provider 端点也要带会话 ID：CLI 从 1.56.1 起给「需要它的 BYOK 主机」补上了
	// 这个头，而 /provider/ 正是这类端点。
	req.Header.Set("x-session-id", c.sessionFor(apiKey))

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Warn("拉取模型列表失败，使用内置列表", "err", err)
		return hardcodedModels
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("拉取模型列表返回非 200，使用内置列表", "status", resp.StatusCode)
		return hardcodedModels
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		c.logger.Warn("读取模型列表失败，使用内置列表", "err", err)
		return hardcodedModels
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Data) == 0 {
		c.logger.Warn("模型列表格式不符合预期，使用内置列表", "err", err)
		return hardcodedModels
	}

	out := make([]Model, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		// 上游只给 id，没有展示名，就用 id 兼作 name。
		out = append(out, Model{ID: m.ID, Name: m.ID})
	}
	if len(out) == 0 {
		return hardcodedModels
	}
	c.logger.Info("已从上游拉取模型列表", "count", len(out))
	return out
}

// baseHeaders 构造预请求用的公共头。
// baseHeaders 是辅助请求（指纹上报）的公共头。
//
// 不带 x-session-id 的话这些请求看起来就不像同一个 CLI 发出来的，所以由调用方
// 把会话 ID 传进来，和生成请求保持一致。
func (c *Client) baseHeaders(apiKey, sessionID string) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "cli")
	h.Set("x-cli-environment", "production")
	h.Set("x-project-slug", slugifyProjectPath(c.device.ProjectDir))
	h.Set("x-taste-learning", "false")
	h.Set("x-session-id", sessionID)
	h.Set("Authorization", "Bearer "+apiKey)
	h.Set("x-command-code-version", protocolVersion)
	return h
}

// post 发一个辅助请求，返回是否成功。
//
// 失败不影响调用方的主流程，返回值只是用来决定要不要提前安排重试。
func (c *Client) post(ctx context.Context, path string, headers http.Header, body []byte, what string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		c.logger.Warn(what+"：构造请求失败", "err", err)
		return false
	}
	req.Header = headers

	resp, err := c.http.Do(req)
	if err != nil {
		// 预请求失败只记 debug：它可能会被重试，用 warn 会把日志刷满。
		c.logger.Debug(what+"：请求失败，稍后会重试", "err", err)
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 300 {
		// 非 2xx 要记 warn：密钥失效这类问题会一直非 2xx，
		// 用 debug 就等于把它藏起来了。
		c.logger.Warn(what+"：上游返回非 2xx", "status", resp.StatusCode)
		return false
	}
	c.logger.Debug(what + "：成功")
	return true
}

// ---- 小工具 ----

// randomUUID 生成 v4 UUID，用于 threadId / sessionId。
func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败在正常系统上不该发生；退化成时间戳也比起 panic 好。
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // 版本 4
	b[8] = (b[8] & 0x3f) | 0x80 // 变体 RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// generateTraceparent 生成 W3C traceparent 头。格式：00-<32hex>-<16hex>-01。
func generateTraceparent() string {
	return "00-" + randomHex(16) + "-" + randomHex(8) + "-01"
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// randomJitter 返回 [0, d) 内的随机时长。
func randomJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
