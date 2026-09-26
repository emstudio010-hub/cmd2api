package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cmd2api/ent"
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

// oauthOutcomeTTL 是一次握手的**结果**留给面板轮询的时间。
//
// 比 state 本身的十分钟短得多：这段时间唯一的作用是让发起授权那个标签页
// 发现「好了」。两分钟还没轮到它看一眼，那它多半已经不在了。
const oauthOutcomeTTL = 2 * time.Minute

// oauthWhoamiTimeout 是校验密钥那一步的上游超时。
//
// 用户前面已经等了一整个登录流程，这一步再拖住只会让人以为卡死了；
// 上游十秒还不回话，就当这把密钥这次没验成，让他重试。
const oauthWhoamiTimeout = 10 * time.Second

// OAuthCallbackPath 是 studio 把授权结果交回来的路径。
//
// 为什么是这么短、还挂在站点根上的一条：studio 只接受 **localhost** 的回调
// 地址（它自己的授权页上印着 "Only localhost URLs are allowed for security"），
// 而官方 CLI 的本地服务监听的正是 `/callback`。这是唯一被验证过、studio 真会
// 往上递东西的形状，照着抄最省事。
//
// 导出是给 router 用的：注册路由的人和拼地址的人必须是同一份常量，不然哪天
// 改了一处，另一处就悄悄指向一个没人监听的路径。
const OAuthCallbackPath = "/callback"

// OAuthLegacyCallbackPath 是这套流程早先用的长路径。
//
// 留着只为不作废已经发出去、还没走完的授权链接——那些链接里的回调地址写死了
// 长路径，删掉注册就等于让它们跳进 404。
const OAuthLegacyCallbackPath = "/api/accounts/oauth/commandcode/callback"

// oauthCallbackMaxBody 是回调请求体的上限。
//
// 正常一包就是几个短字段。给 64KB 是留足余量又不至于让一个不设防的端点
// 被一大坨东西撑住内存——这条路径在鉴权之外，任何人都能打。
const oauthCallbackMaxBody = 64 << 10

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

// oauthOutcome 是一次握手的最终结果。
//
// 存在的理由：授权是在**另一个标签页**里完成的，发起授权的那个页面只能靠
// 问一句「好了没」来知道结果。把它记在这里，那个页面就不用整页跳走、
// 也不用人肉刷新。
type oauthOutcome struct {
	OK bool
	// AccountID / Name 在 OK 时有效。
	AccountID int64
	Name      string
	// Message 是失败时给用户看的那句话，已经是可以直接显示的措辞。
	Message string
	At      time.Time
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
	// outcomes 按 state 记结果，比 items 多活一小会儿，好让轮询的那个
	// 标签页赶得上。它不参与认身份，所以单独一张表、单独一套过期。
	outcomes map[string]oauthOutcome
}

func newOAuthStateStore() *oauthStateStore {
	return &oauthStateStore{
		items:    make(map[string]oauthPending),
		outcomes: make(map[string]oauthOutcome),
	}
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
	for key, out := range s.outcomes {
		if now.Sub(out.At) > oauthOutcomeTTL {
			delete(s.outcomes, key)
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

// pending 只看一眼这次握手还在不在，不取走。
//
// 给状态查询用：轮询的页面得能区分「还在等」和「这次握手根本不存在（或已
// 经过期）」，后者再等下去是白等。查询不走 take，否则第一次轮询就会把
// 回调要用的那份字段吃掉。
func (s *oauthStateStore) pending(state string) bool {
	if state == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[state]
	return ok && !time.Now().After(item.ExpiresAt)
}

// finish 记下一次握手的结果，供发起授权的页面轮询。
func (s *oauthStateStore) finish(state string, out oauthOutcome) {
	if state == "" {
		return
	}
	out.At = time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[state] = out
}

// outcome 读一次结果。
func (s *oauthStateStore) outcome(state string) (oauthOutcome, bool) {
	if state == "" {
		return oauthOutcome{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, ok := s.outcomes[state]
	if !ok {
		return oauthOutcome{}, false
	}
	if time.Since(out.At) > oauthOutcomeTTL {
		delete(s.outcomes, state)
		return oauthOutcome{}, false
	}
	return out, true
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
	loopback := isLoopbackBase(callbackURL)

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

	// 交给 studio 的回调地址不一定是「我们的」地址，见 oauthCopyBackCallback。
	studioCallback := callbackURL
	if !loopback {
		studioCallback = oauthCopyBackCallback
	}

	c.JSON(http.StatusOK, gin.H{
		"auth_url":     relay.CommandCodeAuthURL(studioCallback, state, loopback),
		"callback_url": callbackURL,
		"expires_at":   expires.Format(time.RFC3339),
		// 回调地址指不指向本机，决定了这趟授权是「跳回来」还是「抄回来」：
		// studio 只接受 localhost 的回调。前端拿它决定给哪一种引导，见
		// isLoopbackBase。
		"callback_is_loopback": loopback,
		// state 回给前端，让它能轮询「好了没」。这不算额外泄漏：它就藏在
		// auth_url 里，而 auth_url 本来就要交给这个页面去跳转。
		"state": state,
	})
}

// oauthCopyBackCallback 是面板不在本机时，我们交给 studio 的回调地址。
//
// 它**故意**是一个没人监听的地址：studio 只接受 localhost 的回调，而面板在
// 远程服务器上时，回调地址必然是个远程域名，studio 会把整页换成 Invalid
// Request——用户连选账号的界面都见不到（这正是这个常量存在的理由）。
//
// 所以干脆给它一个真的 localhost 地址，但那个端口上什么都没有。配合不带
// mode=redirect，studio 的 fetch 会打空，然后它自己退回到
// /studio/auth/cli/fallback，那一页会把密钥明明白白显示出来让用户复制
// （"Copy your API key"）。用户抄回面板，走的是同一条 /complete。
//
// 这不是绕过 studio 的检查，是照着它自己的设计走：那条回落路径就是官方 CLI
// 在本地服务没起来时的正常归宿。
//
// 端口是挑的：**不能**是面板常用的 8080——本机自己跑着一个 cmd2api 是很常见
// 的情况，挑中了就等于把密钥送到另一个实例上，在那台机器上建出一个谁也说不
// 清的账号。47821 落在动态端口区间里，且不属于任何常见服务。
const oauthCopyBackCallback = "http://127.0.0.1:47821/callback"

// isLoopbackBase 判断一个地址是不是指向本机。
//
// 这个判断有实际后果：studio 只接受 localhost 的回调地址（它自己的授权页上
// 印着 "Only localhost URLs are allowed for security"，判不过就整页换成
// Invalid Request）。面板装在远程服务器上时，回调地址必然是那个远程域名，
// 于是 studio 连授权界面都不给进——不是我们收不到结果，是根本走不到那一步。
//
// 所以前端需要知道这件事，才能把「自动收尾」和「手动粘贴」的引导给对。
func isLoopbackBase(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// createAccountFromKey 用一把刚拿到的上游密钥把账号建出来。
//
// 两条路共用这一个函数：studio 把浏览器跳回回调地址的（自动），和用户把
// 回调地址粘回来的（手动）。两条路拿到的都是同一种东西——一把普通 API
// key——所以之后每一步都该一模一样；分开写迟早会分叉出「自动建的账号有
// 余额、手动建的没有」这种说不清的不一致。
//
// 返回的 error 已经是能直接给用户看的中文，调用方原样透出即可。
func (h *Handler) createAccountFromKey(
	ctx context.Context,
	fields service.CreateAccountInput,
	apiKey string,
	adminID int64,
) (*ent.Account, relay.Whoami, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, relay.Whoami{}, errors.New("授权结果里没有密钥，请重试")
	}

	// 先验一次再建号。宁可让用户看到一句「密钥没能通过校验」，也不要建出
	// 一个用不了的账号让他在列表里自己发现。
	verifyCtx, cancel := context.WithTimeout(ctx, oauthWhoamiTimeout)
	whoami, err := h.accounts.FetchWhoami(verifyCtx, apiKey)
	cancel()
	if err != nil {
		h.logger.Warn("授权回来的密钥校验失败", "admin_id", adminID, "err", err)
		// 交给 relay 那一层措辞：它知道上游的错误体长什么样。直接 err.Error()
		// 显示出来的是一段被截断的 JSON，用户看不懂。
		return nil, relay.Whoami{}, errors.New(relay.DescribeKeyCheckError(err))
	}

	if fields.Name == "" {
		// 名称留空时用站上的用户名兜底，别建出一堆「未命名」。
		fields.Name = firstNonEmpty(whoami.UserName, whoami.Name, "Command Code 账号")
	}
	// 这两条由服务端定死：这条路只服务 Command Code，平台的取值不接受外部
	// 输入；密钥就是上面刚验过的那把。
	fields.Platform = domain.PlatformCommandCode
	fields.APIKey = apiKey

	acc, err := h.accounts.Create(ctx, fields)
	if err != nil {
		h.logger.Warn("授权建号失败", "admin_id", adminID, "err", err)
		return nil, relay.Whoami{}, fmt.Errorf("账号创建失败：%s", truncateRunes(err.Error(), 120))
	}

	// 顺手取一次余额。走的是计费接口，不消耗生成额度，但能让这个新账号
	// 一出现在列表里就带着余额和套餐——刚点完授权就有东西可看。
	// 失败无所谓：余额刷不出来不该让整趟授权看起来是失败的。
	if _, err := h.accounts.RefreshBalance(ctx, acc.ID); err != nil {
		h.logger.Warn("授权后刷新余额失败", "account_id", acc.ID, "err", err)
	}
	return acc, whoami, nil
}

// oauthCallbackPayload 是 studio 交回来的那一包东西。
//
// 字段名不是我们定的：官方 CLI 的服务端按 apiKey / state / userId / userName /
// keyName 去取，少一个就整包不收（它的 isCommandAuthCallbackRequest 就是这么判的）。
// 我们真正要的只有 apiKey 和 state，但名字得跟着上游走。
type oauthCallbackPayload struct {
	APIKey           string `json:"apiKey" form:"apiKey"`
	State            string `json:"state" form:"state"`
	UserID           string `json:"userId" form:"userId"`
	UserName         string `json:"userName" form:"userName"`
	KeyName          string `json:"keyName" form:"keyName"`
	Error            string `json:"error" form:"error"`
	ErrorDescription string `json:"error_description" form:"error_description"`
}

// readOAuthCallbackPayload 从回调请求里取出 studio 交回来的东西。
//
// 只收 GET query 是不够的：现在的 studio 一律用 **POST** 递结果——
// mode=redirect 时它造一张隐藏表单整体提交（application/x-www-form-urlencoded），
// 不带 mode 时用 fetch 发 JSON。GET 那条留着只因为旧版是这样，成本是几行。
//
// 顺便一个好处：密钥从 query 挪进了请求体，连「URL 会不会被别处记下来」
// 这个问题都不用再考虑了。
func readOAuthCallbackPayload(c *gin.Context) oauthCallbackPayload {
	var p oauthCallbackPayload
	if c.Request.Method == http.MethodGet {
		_ = c.ShouldBindQuery(&p)
		return p
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, oauthCallbackMaxBody))
	if err != nil {
		return oauthCallbackPayload{}
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return oauthCallbackPayload{}
	}

	// 先看 Content-Type，再看内容本身像不像 JSON。
	// 多这一道是因为发的形式取决于 studio 走哪条分支，不值得让整条链路
	// 因为一个头标错了就失败。
	if strings.Contains(c.GetHeader("Content-Type"), "application/json") || body[0] == '{' {
		_ = json.Unmarshal(body, &p)
		return p
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return oauthCallbackPayload{}
	}
	return oauthCallbackPayload{
		APIKey:           strings.TrimSpace(values.Get("apiKey")),
		State:            strings.TrimSpace(values.Get("state")),
		UserID:           values.Get("userId"),
		UserName:         values.Get("userName"),
		KeyName:          values.Get("keyName"),
		Error:            strings.TrimSpace(values.Get("error")),
		ErrorDescription: strings.TrimSpace(values.Get("error_description")),
	}
}

// AccountOAuthCallback 接收 studio 交回来的授权结果。
//
// 这个端点**故意不挂 AdminAuth**：它是浏览器发起的请求，mode=redirect 时更是
// 一次顶层表单提交，浏览器不会给它带 Authorization 头。身份完全靠一次性
// state 认——这也是上面那个 store 存在的理由。
//
// 它必须待在 AdminAuth 之外，所以注册在 api 组而不是 admin 组里。
func (h *Handler) AccountOAuthCallback(c *gin.Context) {
	payload := readOAuthCallbackPayload(c)
	state := strings.TrimSpace(payload.State)
	if state == "" && payload.APIKey == "" && payload.Error == "" {
		// 用浏览器直接打开这个地址，或者不是 studio 发来的东西。说清楚这条
		// 路径是干嘛的，比丢一句「参数错误」有用。
		fail(c, http.StatusBadRequest,
			"这是授权回调地址，正常流程里由授权页自动提交，不需要手动打开")
		return
	}
	pending, ok := h.oauthStates.take(state)
	if !ok {
		// 这次握手已经被别处收掉了。最可能的场景：用户走了手动粘贴那条路
		// （浏览器跳不回来），然后原来那个标签页又好巧不巧地跳了回来。
		// 那不是失败——账号已经建出来了。这里要如实报成功，否则用户会在
		// 一个标签页里看到「已失效」、在另一个里看到「已创建」，不知道该
		// 信哪个，多半会再建一个。
		if out, done := h.oauthStates.outcome(state); done && out.OK {
			values := url.Values{}
			values.Set("oauth", "ok")
			values.Set("id", strconv.FormatInt(out.AccountID, 10))
			values.Set("name", out.Name)
			h.oauthRedirect(c, values)
			return
		}
		// 不区分「没这个 state」和「过期了」：对外说一样的话，
		// 免得把「哪些 state 曾经存在过」这种信息漏出去。
		h.oauthFail(c, state, "授权链接已失效，请重新发起")
		return
	}
	if h.accounts == nil {
		h.oauthFail(c, state, "账号服务未启用")
		return
	}

	// 用户在站上点了拒绝时，studio 会带 error 回来。
	if code := strings.TrimSpace(payload.Error); code != "" {
		if code == "access_denied" {
			h.oauthFail(c, state, "已取消授权")
			return
		}
		desc := strings.TrimSpace(payload.ErrorDescription)
		if desc == "" {
			desc = code
		}
		h.oauthFail(c, state, "授权失败："+truncateRunes(desc, 120))
		return
	}

	ctx := c.Request.Context()
	acc, whoami, err := h.createAccountFromKey(ctx, pending.Fields, payload.APIKey, pending.AdminID)
	if err != nil {
		h.oauthFail(c, state, err.Error())
		return
	}

	h.logger.Info("浏览器授权新建账号成功",
		"admin_id", pending.AdminID, "account_id", acc.ID, "user_name", whoami.UserName)
	h.oauthStates.finish(state, oauthOutcome{OK: true, AccountID: acc.ID, Name: acc.Name})

	values := url.Values{}
	values.Set("oauth", "ok")
	values.Set("id", strconv.FormatInt(acc.ID, 10))
	values.Set("name", acc.Name)
	h.oauthRedirect(c, values)
}

// ---- 等结果与手动收尾 ----

// AccountOAuthStatus 让发起授权的那个页面问一句「好了没」。
//
// 有了它，授权就不必再抢走当前页面：在新标签页里完成登录，这个页面轮询
// 到结果后自己刷新列表。轮询到「好了」之前，用户填了一半的表单一直还在。
func (h *Handler) AccountOAuthStatus(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		fail(c, http.StatusBadRequest, "缺少 state")
		return
	}
	if out, ok := h.oauthStates.outcome(state); ok {
		if out.OK {
			c.JSON(http.StatusOK, gin.H{
				"status":     "ok",
				"account_id": out.AccountID,
				"name":       out.Name,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "error", "message": out.Message})
		return
	}
	if h.oauthStates.pending(state) {
		c.JSON(http.StatusOK, gin.H{"status": "pending"})
		return
	}
	// 既没有结果、也没有握手：要么过期了，要么这个 state 从来不存在。
	// 前端据此停止轮询并提示重新发起，而不是一直转圈。
	c.JSON(http.StatusOK, gin.H{
		"status":  "gone",
		"message": "这次授权已失效，请重新发起",
	})
}

// completeAccountOAuthRequest 是手动收尾的入参。
type completeAccountOAuthRequest struct {
	// Result 是用户粘回来的回调地址，或者一把裸密钥。
	Result string `json:"result" binding:"required"`
	// State 是发起时后端回的那个握手标识。带上它就能沿用后端存着的那份
	// 表单字段；丢了也不要紧，下面这几个字段会顶上。
	State string `json:"state"`
	// 以下与发起授权时同义，是 state 认不出来时的兜底。
	Name           string  `json:"name"`
	Notes          string  `json:"notes"`
	Concurrency    int     `json:"concurrency"`
	Priority       int     `json:"priority"`
	RateMultiplier float64 `json:"rate_multiplier"`
	GroupIDs       []int64 `json:"group_ids"`
	ExpiresAt      *string `json:"expires_at"`
}

// CompleteAccountOAuth 收下用户手动粘回来的授权结果。
//
// 走这条路的情形很具体：studio 把浏览器跳回**跑着 cmd2api 的那台机器**上
// 的回调地址，而如果面板在远程服务器上、浏览器连不回来，那一跳就失败了，
// 页面停在一个打不开的地址——密钥却正好在那条地址的 query 里。用户把地址栏
// 内容复制回来，这一趟授权就不算白做（state 是一次性的，redirect 失败了也
// 不会有人替他去捡）。
//
// 这条路径挂 AdminAuth：身份来自登录令牌，不是 state。所以它比回调那条路
// **更严**，不是更松——能调它的人必须已经登进了面板。
func (h *Handler) CompleteAccountOAuth(c *gin.Context) {
	claims, ok := adminClaims(c)
	if !ok {
		return
	}
	var req completeAccountOAuthRequest
	if !bindJSON(c, &req) {
		return
	}
	if h.accounts == nil {
		fail(c, http.StatusServiceUnavailable, "账号服务未启用")
		return
	}

	parsed, err := relay.ParseCommandCodeCallback(req.Result)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	// 用户在站上拒了，或者 studio 直接报错。没有密钥可建，别白跑一趟。
	if parsed.Error != "" {
		if parsed.Error == "access_denied" {
			fail(c, http.StatusBadRequest, "已取消授权")
			return
		}
		fail(c, http.StatusBadRequest,
			"授权失败："+truncateRunes(firstNonEmpty(parsed.ErrorDescription, parsed.Error), 120))
		return
	}

	state := firstNonEmpty(req.State, parsed.State)

	// 字段优先用发起时存在后端的那一份：那是用户在表单里认真填过的。
	// 粘回来的地址里也带 state，所以就算前端没把 state 传回来，这里一样能认。
	var fields service.CreateAccountInput
	if pending, found := h.oauthStates.take(state); found {
		fields = pending.Fields
	} else {
		expiresAt, ok := parseOptionalTime(c, req.ExpiresAt)
		if !ok {
			return
		}
		fields = service.CreateAccountInput{
			Name:           strings.TrimSpace(req.Name),
			Notes:          req.Notes,
			Platform:       domain.PlatformCommandCode,
			Concurrency:    req.Concurrency,
			Priority:       req.Priority,
			RateMultiplier: req.RateMultiplier,
			GroupIDs:       req.GroupIDs,
			ExpiresAt:      expiresAt,
		}
	}

	acc, whoami, err := h.createAccountFromKey(c.Request.Context(), fields, parsed.APIKey, claims.UserID)
	if err != nil {
		// 记下结果：另一个标签页可能正等着这一趟，让它别再转圈。
		h.oauthStates.finish(state, oauthOutcome{Message: err.Error()})
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	h.oauthStates.finish(state, oauthOutcome{OK: true, AccountID: acc.ID, Name: acc.Name})

	h.logger.Info("手动粘贴回调地址新建账号成功",
		"admin_id", claims.UserID, "account_id", acc.ID, "user_name", whoami.UserName)
	c.JSON(http.StatusCreated, h.toAccountDTO(acc))
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
	return base + OAuthCallbackPath
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

// oauthFail 记下失败原因，再把浏览器送回账号页。
//
// 记这一步是给发起授权的那个页面用的：它可能正在另一个标签页里轮询，
// 光把浏览器跳走，它就只能一直转到超时。
func (h *Handler) oauthFail(c *gin.Context, state, message string) {
	h.oauthStates.finish(state, oauthOutcome{Message: message})
	h.oauthRedirectError(c, message)
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
