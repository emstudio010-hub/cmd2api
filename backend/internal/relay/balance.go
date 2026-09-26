package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cmd2api/internal/domain"
	"cmd2api/internal/scheduler"
)

// 余额查询是辅助调用：上游慢不该拖住探活，更不该拖住列表页刷新。
const balanceFetchTimeout = 10 * time.Second

// balanceReadLimit 限制读取的响应体大小。余额响应只有几百字节，
// 给 1MB 已经是极大余量，纯粹是防止上游异常时把内存吃满。
const balanceReadLimit = 1 << 20

// commandCodePlanCredits 是 Command Code 各套餐的月度额度（美元）。
//
// 上游接口只返回「还剩多少」，不返回「总额」——总额只能从这张表反查。
// 数据来源是官方 CLI 包 command-code@1.66.0 里内嵌的表（2026-09-26 读取）。
//
// 上游改价时这张表会过期，所以查不到的 planId 一律按未知处理：
// 界面上少显示一个百分比，好过拿一个猜出来的分母去算。
var commandCodePlanCredits = map[string]float64{
	"individual-go":       10,
	"individual-goat":     70,
	"individual-pro":      30,
	"individual-pro-v1":   80,
	"individual-provider": 15,
	"individual-max":      150,
	"individual-ultra":    300,
	"teams-pro":           40,
}

// commandCodePlanNames 是套餐标识对应的展示名。
var commandCodePlanNames = map[string]string{
	"individual-go":       "Go",
	"individual-goat":     "GOAT",
	"individual-pro":      "Pro",
	"individual-pro-v1":   "Pro",
	"individual-provider": "Provider",
	"individual-max":      "Max",
	"individual-ultra":    "Ultra",
	"teams-pro":           "Teams Pro",
}

// PlanCredits 返回套餐的月度总额。第二个返回值为 false 表示这个套餐不认识。
func PlanCredits(planID string) (float64, bool) {
	v, ok := commandCodePlanCredits[planID]
	return v, ok
}

// PlanName 返回套餐展示名，不认识的套餐返回空串。
func PlanName(planID string) string {
	return commandCodePlanNames[planID]
}

// BalanceWindow 是一个滚动额度窗口的用量。
type BalanceWindow struct {
	// Used / Cap 单位都是美元。
	Used     float64
	Cap      float64
	Exceeded bool
	ResetAt  *time.Time
}

// Balance 是一个账号的余额快照。
type Balance struct {
	// Remaining 是当前还能花的总额度（美元）。
	//
	// 它是套餐剩余 + 额外购买 + 赠送三部分之和：这三笔钱都能花出去，
	// 只报套餐剩余会让「买了额度的人」看到偏小的数字。
	Remaining float64
	// PlanID 是套餐标识；PlanName 是展示名，可能为空。
	PlanID   string
	PlanName string
	// PeriodEnd 是当前计费周期的结束时间，取不到就是 nil。
	PeriodEnd *time.Time
	// FiveHour / Weekly 是两个滚动窗口，上游没给就是 nil。
	FiveHour *BalanceWindow
	Weekly   *BalanceWindow
}

// BalanceResult 是一次余额查询的结果。
type BalanceResult struct {
	// Supported 为 false 表示这个平台查不了余额。
	//
	// 这**不是**错误：OpenCode 目前没接，界面该显示「暂不支持」而不是一条
	// 红色报错——报错会让人以为账号本身有问题，白白去查一个不存在的故障。
	Supported bool
	Balance   *Balance
	Err       error
}

// errBalanceTargetEmpty 是内部错误，正常调用路径不该出现。
var errBalanceTargetEmpty = errors.New("余额查询目标为空")

// FetchBalance 查一个账号的余额。
//
// 按平台分派，和探活一样：两种上游的余额接口完全不同，没有共用空间。
func (c *Client) FetchBalance(ctx context.Context, target *scheduler.Target) BalanceResult {
	if target == nil {
		return BalanceResult{Err: errBalanceTargetEmpty}
	}
	if target.Platform != domain.PlatformCommandCode {
		// OpenCode 的用量端点（/zen/go/v1/usage）已经探到了，路由确实存在，
		// 但手上没有真实账号，返回结构没验证过。宁可先不做，也不要照着猜的
		// 结构写解析——猜错了会安静地显示成 0，比不显示更误导。
		return BalanceResult{}
	}
	return c.fetchCommandCodeBalance(ctx, target.APIKey)
}

// fetchCommandCodeBalance 查 Command Code 账号的余额。
//
// 打两个端点：credits 给剩余额度和滚动窗口，subscriptions 给套餐标识。
// 上游没把它们合在一起，所以这里必须发两次；两个都很轻（几百字节）。
func (c *Client) fetchCommandCodeBalance(ctx context.Context, apiKey string) BalanceResult {
	fetchCtx, cancel := context.WithTimeout(ctx, balanceFetchTimeout)
	defer cancel()

	var credits ccCreditsResponse
	if err := c.getJSON(fetchCtx, apiKey, "/alpha/billing/credits", &credits); err != nil {
		return BalanceResult{Supported: true, Err: err}
	}

	balance := &Balance{
		// 三笔额度都能花，合并成一个「还能花多少」。
		Remaining: credits.Credits.MonthlyCredits +
			credits.Credits.PurchasedCredits +
			credits.Credits.FreeCredits,
		PlanID:   credits.Credits.PlanID,
		FiveHour: credits.WindowLimits.FiveHour.balanceWindow(),
		Weekly:   credits.WindowLimits.Weekly.balanceWindow(),
	}

	// 套餐信息单独取。取不到不算失败：剩余额度才是这个功能的主产物，
	// 为了一个展示用的套餐名把整次查询判成失败，是拿次要目标否决主要目标。
	var sub ccSubscriptionResponse
	if err := c.getJSON(fetchCtx, apiKey, "/alpha/billing/subscriptions", &sub); err == nil && sub.Data != nil {
		if balance.PlanID == "" {
			balance.PlanID = sub.Data.PlanID
		}
		balance.PeriodEnd = parseRFC3339Ptr(sub.Data.CurrentPeriodEnd)
	} else if err != nil {
		c.logger.Debug("拉取套餐信息失败，仅返回额度", "err", err)
	}

	balance.PlanName = PlanName(balance.PlanID)
	return BalanceResult{Supported: true, Balance: balance}
}

// ccCreditsResponse 是 /alpha/billing/credits 的响应。
//
// 字段名对齐上游的驼峰命名；上游没有 success 包装，直接给 credits 和 windowLimits。
type ccCreditsResponse struct {
	Credits struct {
		MonthlyCredits   float64 `json:"monthlyCredits"`
		PurchasedCredits float64 `json:"purchasedCredits"`
		FreeCredits      float64 `json:"freeCredits"`
		CreditThreshold  float64 `json:"creditThreshold"`
		BelowThreshold   bool    `json:"belowThreshold"`
		// PlanID 上游偶尔会带上，但实测是缺的，所以只当兜底。
		PlanID string `json:"planId"`
	} `json:"credits"`
	WindowLimits struct {
		Limited  bool      `json:"limited"`
		FiveHour *ccWindow `json:"fiveHour"`
		Weekly   *ccWindow `json:"weekly"`
	} `json:"windowLimits"`
}

// ccWindow 是一个滚动窗口。resetAt 是**毫秒**时间戳，不是秒。
type ccWindow struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"resetAt"`
}

// balanceWindow 把上游的窗口结构转成内部表示。
func (w *ccWindow) balanceWindow() *BalanceWindow {
	if w == nil {
		return nil
	}
	out := &BalanceWindow{Used: w.Used, Cap: w.Cap, Exceeded: w.Exceeded}
	if w.ResetAt > 0 {
		t := time.UnixMilli(w.ResetAt)
		out.ResetAt = &t
	}
	return out
}

// ccSubscriptionResponse 是 /alpha/billing/subscriptions 的响应。
type ccSubscriptionResponse struct {
	Success bool `json:"success"`
	Data    *struct {
		PlanID             string `json:"planId"`
		Status             string `json:"status"`
		CurrentPeriodStart string `json:"currentPeriodStart"`
		CurrentPeriodEnd   string `json:"currentPeriodEnd"`
	} `json:"data"`
}

// getJSON 发一个带鉴权的 GET 并把响应解到 out。
func (c *Client) getJSON(ctx context.Context, apiKey, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header = c.baseHeaders(apiKey, c.sessionFor(apiKey))

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("请求上游失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, balanceReadLimit))
	if err != nil {
		return fmt.Errorf("读取上游响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回 %d：%s", resp.StatusCode, snippetForError(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("上游响应格式不符合预期: %w", err)
	}
	return nil
}

// snippetForError 把响应体裁成一小段放进错误信息。
//
// 上游出错时会返回它自己的错误 JSON，原样带出来最好排障；但不能整段塞进
// error_message 列——那是个展示字段，几百 KB 的响应会把界面撑坏。
func snippetForError(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	const max = 200
	// 按 rune 截断：按字节切会切开多字节字符，产生非法 UTF-8。
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// parseRFC3339Ptr 解析上游的时间字符串，失败返回 nil。
func parseRFC3339Ptr(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
