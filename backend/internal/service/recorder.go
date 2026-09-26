// Package service 存放连接 HTTP 层与数据层的业务逻辑。
package service

import (
	"context"
	"log/slog"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/apikey"
	"cmd2api/internal/relay"
)

// Recorder 把 relay 报上来的用量写成 usage_logs，并累加 API Key 的已用额度。
type Recorder struct {
	client *ent.Client
	logger *slog.Logger
}

// NewRecorder 构造 Recorder。
func NewRecorder(client *ent.Client, logger *slog.Logger) *Recorder {
	return &Recorder{client: client, logger: logger}
}

// Record 实现 relay.UsageRecorder。
//
// 刻意不返回错误：用量记录失败不该影响已经成功返回给用户的请求。
// 但会打 warn 日志——记录一直失败说明系统有问题，得能看见。
func (r *Recorder) Record(ctx context.Context, rec relay.UsageRecord) {
	cost, multiplier := calculateCost(rec)

	create := r.client.UsageLog.Create().
		SetUserID(rec.UserID).
		SetAPIKeyID(rec.APIKeyID).
		SetAccountID(rec.AccountID).
		SetRequestID(rec.RequestID).
		SetModel(modelOrUnknown(rec.Model)).
		SetInputTokens(rec.InputTokens).
		SetOutputTokens(rec.OutputTokens).
		SetCacheCreationTokens(rec.CacheCreationTokens).
		SetCacheReadTokens(rec.CacheReadTokens).
		SetInputCost(cost.InputCost).
		SetOutputCost(cost.OutputCost).
		SetTotalCost(cost.TotalCost).
		SetRateMultiplier(multiplier).
		SetStream(rec.Stream).
		SetDurationMs(rec.DurationMs).
		SetStatusCode(rec.StatusCode)

	if rec.GroupID != nil {
		create = create.SetGroupID(*rec.GroupID)
	}
	if rec.FirstTokenMs != nil {
		create = create.SetFirstTokenMs(*rec.FirstTokenMs)
	}
	if rec.ErrorMessage != "" {
		create = create.SetErrorMessage(rec.ErrorMessage)
	}
	if rec.UserAgent != "" {
		create = create.SetUserAgent(rec.UserAgent)
	}
	if rec.IPAddress != "" {
		create = create.SetIPAddress(rec.IPAddress)
	}

	if _, err := create.Save(ctx); err != nil {
		r.logger.Warn("写入用量日志失败", "err", err, "request_id", rec.RequestID)
		return
	}

	if cost.TotalCost <= 0 {
		return
	}
	// 用带条件表达式的原子自增，不做「读出来加完写回去」——
	// 高并发下读-改-写会丢更新，额度就会算少。
	if _, err := r.client.APIKey.Update().
		Where(apikey.ID(rec.APIKeyID)).
		AddQuotaUsed(cost.TotalCost).
		SetLastUsedAt(time.Now()).
		Save(ctx); err != nil {
		r.logger.Warn("累加 API Key 已用额度失败", "err", err, "api_key_id", rec.APIKeyID)
	}
}

// costBreakdown 是一次请求的费用拆分。
type costBreakdown struct {
	InputCost  float64
	OutputCost float64
	TotalCost  float64
}

// calculateCost 按价格表算费用。
//
// 注意：cmd2api 没有支付与充值，这里的金额只是**估算口径**，用于用量分析
// 和相对比较，不对应任何真实扣款。上游是订阅制，真实成本与 token 数不成正比。
func calculateCost(rec relay.UsageRecord) (costBreakdown, float64) {
	multiplier := rec.RateMultiplier
	if multiplier <= 0 {
		multiplier = 1
	}

	price, ok := priceFor(rec.Model)
	if !ok {
		// 没有价格就只记 token，不编造金额——宁可显示 0 也不要给一个
		// 看起来精确实则虚构的数字。
		return costBreakdown{}, multiplier
	}

	const perMillion = 1_000_000.0
	input := float64(rec.InputTokens) / perMillion * price.Input
	output := float64(rec.OutputTokens) / perMillion * price.Output

	return costBreakdown{
		InputCost:  input * multiplier,
		OutputCost: output * multiplier,
		TotalCost:  (input + output) * multiplier,
	}, multiplier
}

// ModelPrice 是每百万 token 的美元单价。
type ModelPrice struct {
	Input  float64
	Output float64
}

// priceTable 是内置价格表，单位：美元 / 百万 token。
//
// 这是**参考价**，用于让用量统计有可比的量纲。上游是订阅制，实际不按 token 计费。
// 想调整直接改这里，或在后台设置里覆盖（见 settings）。
var priceTable = map[string]ModelPrice{
	"claude-opus-4-8":              {Input: 15, Output: 75},
	"claude-opus-4-7":              {Input: 15, Output: 75},
	"claude-sonnet-4-6":            {Input: 3, Output: 15},
	"claude-haiku-4-5-20251001":    {Input: 1, Output: 5},
	"gpt-5.5":                      {Input: 1.25, Output: 10},
	"gpt-5.4":                      {Input: 1.25, Output: 10},
	"gpt-5.4-mini":                 {Input: 0.25, Output: 2},
	"gpt-5.3-codex":                {Input: 1.25, Output: 10},
	"deepseek/deepseek-v4-pro":     {Input: 0.6, Output: 2.4},
	"deepseek/deepseek-v4-flash":   {Input: 0.15, Output: 0.6},
	"moonshotai/Kimi-K2.6":         {Input: 0.6, Output: 2.5},
	"moonshotai/Kimi-K2.5":         {Input: 0.6, Output: 2.5},
	"zai-org/GLM-5.1":              {Input: 0.6, Output: 2.2},
	"zai-org/GLM-5":                {Input: 0.6, Output: 2.2},
	"MiniMaxAI/MiniMax-M3":         {Input: 0.3, Output: 1.2},
	"MiniMaxAI/MiniMax-M2.7":       {Input: 0.3, Output: 1.2},
	"MiniMaxAI/MiniMax-M2.5":       {Input: 0.3, Output: 1.2},
	"Qwen/Qwen3.6-Max-Preview":     {Input: 1.2, Output: 6},
	"Qwen/Qwen3.6-Plus":            {Input: 0.4, Output: 1.2},
	"Qwen/Qwen3.7-Max":             {Input: 1.2, Output: 6},
	"stepfun/Step-3.7-Flash":       {Input: 0.2, Output: 0.8},
	"stepfun/Step-3.5-Flash":       {Input: 0.2, Output: 0.8},
	"xiaomi/mimo-v2.5-pro":         {Input: 0.4, Output: 1.5},
	"xiaomi/mimo-v2.5":             {Input: 0.2, Output: 0.8},
	"google/gemini-3.5-flash":      {Input: 0.3, Output: 2.5},
	"google/gemini-3.1-flash-lite": {Input: 0.1, Output: 0.4},
}

// openCodePriceTable 是 OpenCode 上游的模型定价。
//
// 必须单独一张表：OpenCode 的模型命名和 Command Code 完全是两套体系——
// 同一个模型在两边叫 minimax-m3 和 MiniMaxAI/MiniMax-M3，
// 一张表覆盖不了，混在一起会让人以为"匹配上了"其实是错的。
//
// ⚠️ 这些是按**模型家族**给的估算单价，不是 OpenCode 官方报价。
// 用途仅是让用量统计在 OpenCode 场景下也有可比的量纲。
// `-free` 结尾的模型本就不收费，不在此表内（查不到即记 0，正好正确）。
var openCodePriceTable = map[string]ModelPrice{
	// Anthropic 系（Zen 档）
	"claude-opus-5-5":   {Input: 15, Output: 75},
	"claude-opus-5":     {Input: 15, Output: 75},
	"claude-opus-4-8":   {Input: 15, Output: 75},
	"claude-opus-4-7":   {Input: 15, Output: 75},
	"claude-opus-4-6":   {Input: 15, Output: 75},
	"claude-opus-4-5":   {Input: 15, Output: 75},
	"claude-sonnet-5":   {Input: 3, Output: 15},
	"claude-sonnet-4-6": {Input: 3, Output: 15},
	"claude-sonnet-4-5": {Input: 3, Output: 15},
	"claude-haiku-4-5":  {Input: 1, Output: 5},
	"claude-fable-5":    {Input: 3, Output: 15},
	"claude-fable-5-1":  {Input: 3, Output: 15},

	// OpenAI 系
	"gpt-6-luna":    {Input: 2, Output: 12},
	"gpt-6-sol":     {Input: 2, Output: 12},
	"gpt-6-astra":   {Input: 2, Output: 12},
	"gpt-5.6-luna":  {Input: 1.5, Output: 10},
	"gpt-5.6-sol":   {Input: 1.5, Output: 10},
	"gpt-5.6-terra": {Input: 1.5, Output: 10},
	"gpt-5.5":       {Input: 1.25, Output: 10},
	"gpt-5.4":       {Input: 1.25, Output: 10},
	"gpt-5.4-mini":  {Input: 0.25, Output: 2},
	"gpt-5.3-codex": {Input: 1.25, Output: 10},
	"gpt-5.1-codex": {Input: 1.25, Output: 10},
	"gpt-5":         {Input: 1.25, Output: 10},

	// Google 系
	"gemini-3.8-flash":      {Input: 0.3, Output: 2.5},
	"gemini-3.6-flash":      {Input: 0.3, Output: 2.5},
	"gemini-3.5-flash":      {Input: 0.3, Output: 2.5},
	"gemini-3.5-flash-lite": {Input: 0.1, Output: 0.4},
	"gemini-3.1-pro":        {Input: 1.25, Output: 10},

	// xAI
	"grok-4.7": {Input: 3, Output: 15},
	"grok-4.6": {Input: 3, Output: 15},
	"grok-4.5": {Input: 3, Output: 15},

	// DeepSeek
	"deepseek-v4-pro":     {Input: 0.6, Output: 2.4},
	"deepseek-v4-flash":   {Input: 0.15, Output: 0.6},
	"deepseek-v4.1-flash": {Input: 0.15, Output: 0.6},

	// Moonshot / Kimi
	"kimi-k3":        {Input: 0.6, Output: 2.5},
	"kimi-k2.7-code": {Input: 0.6, Output: 2.5},
	"kimi-k2.6":      {Input: 0.6, Output: 2.5},

	// 智谱 GLM
	"glm-5.3":       {Input: 0.6, Output: 2.2},
	"glm-5.3-flash": {Input: 0.1, Output: 0.4},
	"glm-5.2":       {Input: 0.6, Output: 2.2},
	"glm-5.1":       {Input: 0.6, Output: 2.2},

	// MiniMax
	"minimax-m3":   {Input: 0.3, Output: 1.2},
	"minimax-m2.7": {Input: 0.3, Output: 1.2},
	"minimax-m2.5": {Input: 0.3, Output: 1.2},

	// 阿里 Qwen
	"qwen3.8-max":   {Input: 1.2, Output: 6},
	"qwen3.8-flash": {Input: 0.1, Output: 0.4},
	"qwen3.7-max":   {Input: 1.2, Output: 6},
	"qwen3.7-plus":  {Input: 0.4, Output: 1.2},
	"qwen3.6-plus":  {Input: 0.4, Output: 1.2},

	// 小米 MiMo
	"mimo-v2.6-pro":   {Input: 0.4, Output: 1.5},
	"mimo-v2.6-flash": {Input: 0.1, Output: 0.4},
	"mimo-v2.5-pro":   {Input: 0.4, Output: 1.5},
	"mimo-v2.5":       {Input: 0.2, Output: 0.8},
}

func priceFor(model string) (ModelPrice, bool) {
	if p, ok := priceTable[model]; ok {
		return p, true
	}
	p, ok := openCodePriceTable[model]
	return p, ok
}

// modelOrUnknown 保证 model 字段非空——表上有 NotEmpty 约束，
// 空值会让整条用量记录写不进去。
func modelOrUnknown(model string) string {
	if model == "" {
		return "unknown"
	}
	return model
}

// 确保 Recorder 满足 relay 的接口。
var _ relay.UsageRecorder = (*Recorder)(nil)
