package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cmd2api/internal/domain"
	"cmd2api/internal/middleware"
	"cmd2api/internal/relay"
	"cmd2api/internal/service"

	"github.com/gin-gonic/gin"
)

// oauthStateTTL 是一次浏览器授权握手的最长存活时间。
//
// 十分钟：够人在浏览器里登录完，又不至于把窗口留得太久——state 是这条链路
// 上唯一的凭证，它活着多久，就有一个多久的空窗期。
const oauthStateTTL = 10 * time.Minute

// oauthWhoamiTimeout 是校验密钥那一步的上游超时。
//
// 用户前面已经等了一整个登录流程，这一步再拖住只会让人以为卡死了；
// 上游十秒还不回话，就当这把密钥这次没验成，让他重试。
const oauthWhoamiTimeout = 10 * time.Second

// oauthCallbackPath 是 studio 把浏览器跳回来的后端路径。
const oauthCallbackPath = "/api/accounts/oauth/commandcode/callback"

// oauthAccountsPath 是授权收尾时把浏览器送回的前端路由。
const oauthAccountsPath = "/accounts"

// oauthPending 是一次已发起、还没回来的授权。
type oauthPending struct {
	// Fields 是发起时表单里填好的账号参数，回调时直接拿去建号。不含密钥——
	// 密钥要等回调带回来才有。
	Fields service.CreateAccountInput
	// AdminID 只用于日志：出事时要能查出是谁点的授权。
	AdminID int64
	// ExpiresAt 过后这条记录作废。
	ExpiresAt time.Time
}

// oauthStateStore 保存进行中的授权握手。
//
// 放内存而不是落库：这是进程内的短命握手，用完即弃。重启丢掉最多让人重点
// 一次按钮；落库则要多一张表、一次迁移和一套清理任务——为十秒钟的东西不值。
//
// 前提是单实例部署：多副本时「发起」和「回调」可能落在不同进程上，这里
// 必须换成共享存储，否则回调一律认不出 state。
type oauthStateStore struct {
	mu    sync.Mutex
	items map[string]oauthPending
}

func newOAuthStateStore() *oauthStateStore {
	return &oauthStateStore{items: make(map[string]oauthPending)}
}

// put 记下一次握手，顺手清掉过期的。
//
// 清理挂在写入路径上而不是单开定时器：写入是低频操作（得有人点按钮），
// 借它的频率扫一遍就够，还能少一个需要管理的后台 goroutine。
func (s *oauthStateStore) put(state string, pending oauthPending) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.items {
		if now.After(item.ExpiresAt) {
			delete(s.items, key)
		}
	}
	s.items[state] = pending
}

// take 取走一次握手，取走即作废。
//
// 一次性是必须的：state 会出现在浏览器地址栏和授权页那一跳的 URL 里，
// 能被重放的话，捡到它的人就能拿自己的密钥在这台机器上建出一个账号——
// 之后池子里的流量会跑到别人的账号上去。
func (s *oauthStateStore) take(state string) (oauthPending, bool) {
	if state == "" {
		return oauthPending{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.items[state]
	if !ok {
		return oauthPending{}, false
	}
	delete(s.items, state)
	if time.Now().After(pending.ExpiresAt) {
		return oauthPending{}, false
	}
	return pending, true
}

// newOAuthState 生成一个不可猜的 state。
//
// 32 字节随机数：回调端点是公开的，state 是它唯一的凭证，
// 可猜的 state 等于没有 state。
func newOAuthState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// startAccountOAuthRequest 是发起授权的入参。
//
// 字段跟新建账号一一对应，唯独没有 api_key——那正是这趟授权要去拿的东西。
// platform 也不接受外部传入：这条流程只服务 Command Code。
type startAccountOAuthRequest struct {
	Name           string  `json:"name"`
	Notes          string  `json:"notes"`
	Concurrency    int     `json:"concurrency"`
	Priority       int     `json:"priority"`
	RateMultiplier float64 `json:"rate_multiplier"`
	GroupIDs       []int64 `json:"group_ids"`
	ExpiresAt      *string `json:"expires_at"`
}

// StartAccountOAuth 发起一次浏览器授权，把要跳转的地址交给前端。
//
// 这里不建账号：账号要等 studio 把密钥带回来才建得出来。
func (h *Handler) StartAccountOAuth(c *gin.Context) {
	var req startAccountOAuthRequest
	if !bindJSON(c, &req) {
		return
	}
	if h.accounts == nil {
		fail(c, http.StatusServiceUnavailable, "账号服务未启用")
		return
	}

	expiresAt, ok := parseOptionalTime(c, req.ExpiresAt)
	if !ok {
		return
	}

	state, err := newOAuthState()
	if err != nil {
		h.failInternal(c, "生成授权 state 失败", err)
		return
	}

	callbackURL := h.oauthCallbackURL(c)
	if callbackURL == "" {
		fail(c, http.StatusBadRequest,
			"无法确定回调地址，请检查请求的 Host 头，或用 PUBLIC_BASE_URL 显式指定")
		return
	}

	var adminID int64
	if claims, ok := middleware.AdminClaimsFrom(c); ok {
		adminID = claims.UserID
	}

	expires := time.Now().Add(oauthStateTTL)
	h.oauthStates.put(state, oauthPending{
		AdminID:   adminID,
		ExpiresAt: expires,
		Fields: service.CreateAccountInput{
			Name:           strings.TrimSpace(req.Name),
			Notes:          req.Notes,
			Platform:       domain.PlatformCommandCode,
			Concurrency:    req.Concurrency,
			Priority:       req.Priority,
			RateMultiplier: req.RateMultiplier,
			GroupIDs:       req.GroupIDs,
			ExpiresAt:      expiresAt,
		},
	})

	c.JSON(http.StatusOK, gin.H{
		"auth_url":     relay.CommandCodeAuthURL(callbackURL, state),
		"callback_url": callbackURL,
		"expires_at":   expires.Format(time.RFC3339),
	})
}

// AccountOAuthCallback 接收 studio 跳回来的授权结果。
//
// 这个端点**故意不挂 AdminAuth**：它是浏览器的顶层跳转，浏览器不会给这种
// 跳转带 Authorization 头，挂上去等于每次授权都失败。身份完全靠一次性
// state 认——这也是上面那个 store 存在的理由。
//
// 它必须待在 AdminAuth 之外，所以注册在 api 组而不是 admin 组里。
func (h *Handler) AccountOAuthCallback(c *gin.Context) {
	pending, ok := h.oauthStates.take(c.Query("state"))
	if !ok {
		// 不区分「没这个 state」和「过期了」：对外说一样的话，
		// 免得把「哪些 state 曾经存在过」这种信息漏出去。
		h.oauthRedirectError(c, "授权链接已失效，请重新发起")
		return
	}
	if h.accounts == nil {
		h.oauthRedirectError(c, "账号服务未启用")
		return
	}

	// 用户在站上点了拒绝时，studio 会带 error 回来。
	if code := c.Query("error"); code != "" {
		if code == "access_denied" {
			h.oauthRedirectError(c, "已取消授权")
			return
		}
		desc := strings.TrimSpace(c.Query("error_description"))
		if desc == "" {
			desc = code
		}
		h.oauthRedirectError(c, "授权失败："+truncateRunes(desc, 120))
		return
	}

	apiKey := strings.TrimSpace(c.Query("apiKey"))
	if apiKey == "" {
		h.oauthRedirectError(c, "授权结果里没有密钥，请重试")
		return
	}

	ctx := c.Request.Context()

	// 先验一次再建号。宁可让用户看到一句「密钥没能通过校验」，也不要建出
	// 一个用不了的账号让他在列表里自己发现。
	verifyCtx, cancel := context.WithTimeout(ctx, oauthWhoamiTimeout)
	whoami, err := h.accounts.FetchWhoami(verifyCtx, apiKey)
	cancel()
	if err != nil {
		h.logger.Warn("授权回来的密钥校验失败", "admin_id", pending.AdminID, "err", err)
		h.oauthRedirectError(c, "这把密钥没能通过校验："+truncateRunes(err.Error(), 120))
		return
	}

	fields := pending.Fields
	if fields.Name == "" {
		// 名称留空时用站上的用户名兜底，别建出一堆「未命名」。
		fields.Name = firstNonEmpty(whoami.UserName, whoami.Name, "Command Code 账号")
	}
	fields.APIKey = apiKey

	acc, err := h.accounts.Create(ctx, fields)
	if err != nil {
		h.logger.Warn("授权建号失败", "admin_id", pending.AdminID, "err", err)
		h.oauthRedirectError(c, "账号创建失败："+truncateRunes(err.Error(), 120))
		return
	}

	// 顺手取一次余额。走的是计费接口，不消耗生成额度，但能让这个新账号
	// 一出现在列表里就带着余额和套餐——刚点完授权就有东西可看。
	// 失败无所谓：余额刷不出来不该让整趟授权看起来是失败的。
	if _, err := h.accounts.RefreshBalance(ctx, acc.ID); err != nil {
		h.logger.Warn("授权后刷新余额失败", "account_id", acc.ID, "err", err)
	}

	h.logger.Info("浏览器授权新建账号成功",
		"admin_id", pending.AdminID, "account_id", acc.ID, "user_name", whoami.UserName)

	values := url.Values{}
	values.Set("oauth", "ok")
	values.Set("id", strconv.FormatInt(acc.ID, 10))
	values.Set("name", acc.Name)
	h.oauthRedirect(c, values)
}

// oauthCallbackURL 拼出交给 studio 的回调地址。
//
// 默认按发起请求的 Host 推导：那个 Host 就是管理员此刻访问面板用的地址，
// 也就是「浏览器能连回这台机器」的地址——本机直连、SSH 隧道转发、反代域名
// 三种情况它都自动是对的，不用配。
//
// PUBLIC_BASE_URL 是给推导不出来的场景留的口子（反代改写了 Host，或者面板
// 和回调必须落在不同域名上）。
func (h *Handler) oauthCallbackURL(c *gin.Context) string {
	base := ""
	if h.cfg != nil {
		base = strings.TrimRight(strings.TrimSpace(h.cfg.Server.PublicBaseURL), "/")
	}
	if base == "" {
		host := c.Request.Host
		if !isSaneHost(host) {
			return ""
		}
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		// 反代后面 TLS 在代理那头终止，Request.TLS 是空的，只能看这个头。
		// 这里不校验它可不可信：它是管理员自己发起的那个请求的一部分，
		// 被伪造最多是把自己的浏览器跳错地方。
		if proto := firstToken(c.GetHeader("X-Forwarded-Proto")); proto == "http" || proto == "https" {
			scheme = proto
		}
		base = scheme + "://" + host
	}
	return base + oauthCallbackPath
}

// isSaneHost 粗筛 Host 头，挡掉空值和带控制字符的构造。
//
// 这个值会被拼进一个交给第三方网站的 URL，不能让换行、空格之类的东西混进去。
func isSaneHost(host string) bool {
	if host == "" || len(host) > 255 {
		return false
	}
	for _, r := range host {
		if r <= ' ' || r == 0x7f || r == '/' || r == '\\' || r == '?' || r == '#' {
			return false
		}
	}
	return true
}

// oauthRedirectError 把浏览器送回账号页，并带上一句能看懂的原因。
func (h *Handler) oauthRedirectError(c *gin.Context, message string) {
	values := url.Values{}
	values.Set("oauth", "error")
	values.Set("message", message)
	h.oauthRedirect(c, values)
}

// oauthRedirect 把浏览器送回账号页。
//
// 只能用相对路径，而且**绝不能把密钥放进来**：这个 303 会留在浏览器历史里，
// 上游给的 apiKey 一旦出现在这儿，就等于写进了一个用户看不见、却一直留着
// 的地方。密钥从 query 进来，下一秒就从这条链路里消失。
func (h *Handler) oauthRedirect(c *gin.Context, values url.Values) {
	c.Redirect(http.StatusSeeOther, oauthAccountsPath+"?"+values.Encode())
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// truncateRunes 按字符截断，避免把多字节字符切成两半。
func truncateRunes(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

// firstToken 取以逗号分隔的头部里的第一段（X-Forwarded-Proto 可能是
// "https, http" 这种形式）。
func firstToken(raw string) string {
	if idx := strings.IndexByte(raw, ','); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.ToLower(strings.TrimSpace(raw))
}
