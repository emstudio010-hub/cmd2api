package handler

import (
	"context"
	"net/http"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/account"
	"cmd2api/ent/apikey"
	"cmd2api/ent/usagelog"
	"cmd2api/internal/domain"

	"github.com/gin-gonic/gin"
)

// usageLogDTO 是单条用量记录的对外表示。
type usageLogDTO struct {
	ID        int64  `json:"id"`
	Model     string `json:"model"`
	RequestID string `json:"request_id"`

	UserID      int64  `json:"user_id"`
	APIKeyID    int64  `json:"api_key_id"`
	APIKeyName  string `json:"api_key_name"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	GroupID     *int64 `json:"group_id"`

	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`

	TotalCost      float64 `json:"total_cost"`
	RateMultiplier float64 `json:"rate_multiplier"`

	Stream       bool    `json:"stream"`
	DurationMs   *int    `json:"duration_ms"`
	FirstTokenMs *int    `json:"first_token_ms"`
	StatusCode   int     `json:"status_code"`
	ErrorMessage *string `json:"error_message"`
	IPAddress    *string `json:"ip_address"`
	UserAgent    *string `json:"user_agent"`

	CreatedAt time.Time `json:"created_at"`
}

// ListUsageLogs 分页返回用量日志。
func (h *Handler) ListUsageLogs(c *gin.Context) {
	limit, offset := pageParams(c)
	ctx := c.Request.Context()

	query := h.client.UsageLog.Query()

	start, end, ok := h.parseRange(c)
	if !ok {
		return
	}
	query = query.Where(usagelog.CreatedAtGTE(start), usagelog.CreatedAtLT(end))

	if model := c.Query("model"); model != "" {
		query = query.Where(usagelog.ModelEQ(model))
	}
	if keyID := queryInt(c, "api_key_id", 0); keyID > 0 {
		query = query.Where(usagelog.APIKeyIDEQ(int64(keyID)))
	}
	if accountID := queryInt(c, "account_id", 0); accountID > 0 {
		query = query.Where(usagelog.AccountIDEQ(int64(accountID)))
	}
	if requestID := c.Query("request_id"); requestID != "" {
		query = query.Where(usagelog.RequestIDEQ(requestID))
	}
	// status=error 只看失败请求——排障时最常用的筛法。
	switch c.Query("status") {
	case "error":
		query = query.Where(usagelog.StatusCodeGTE(400))
	case "ok":
		query = query.Where(usagelog.StatusCodeLT(400))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		h.failInternal(c, "统计日志条数失败", err)
		return
	}

	rows, err := query.
		Order(ent.Desc(usagelog.FieldCreatedAt)).
		Limit(limit).
		Offset(offset).
		WithAPIKey().
		WithAccount().
		All(ctx)
	if err != nil {
		h.failInternal(c, "查询用量日志失败", err)
		return
	}

	items := make([]usageLogDTO, 0, len(rows))
	for _, row := range rows {
		dto := usageLogDTO{
			ID:                  row.ID,
			Model:               row.Model,
			RequestID:           row.RequestID,
			UserID:              row.UserID,
			APIKeyID:            row.APIKeyID,
			AccountID:           row.AccountID,
			GroupID:             row.GroupID,
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			CacheCreationTokens: row.CacheCreationTokens,
			CacheReadTokens:     row.CacheReadTokens,
			TotalCost:           row.TotalCost,
			RateMultiplier:      row.RateMultiplier,
			Stream:              row.Stream,
			DurationMs:          row.DurationMs,
			FirstTokenMs:        row.FirstTokenMs,
			StatusCode:          row.StatusCode,
			ErrorMessage:        row.ErrorMessage,
			IPAddress:           row.IPAddress,
			UserAgent:           row.UserAgent,
			CreatedAt:           row.CreatedAt,
		}
		if row.Edges.APIKey != nil {
			dto.APIKeyName = row.Edges.APIKey.Name
		}
		if row.Edges.Account != nil {
			dto.AccountName = row.Edges.Account.Name
		}
		items = append(items, dto)
	}

	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      offset/limit + 1,
		"page_size": limit,
		"start":     start.UTC().Format(time.RFC3339),
		"end":       end.UTC().Format(time.RFC3339),
	})
}

// parseRange 解析时间范围，默认最近 24 小时。
func (h *Handler) parseRange(c *gin.Context) (time.Time, time.Time, bool) {
	now := time.Now()
	end := now
	start := now.Add(-24 * time.Hour)

	if raw := c.Query("end"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			fail(c, http.StatusBadRequest, "end 需要是 RFC3339 格式")
			return time.Time{}, time.Time{}, false
		}
		end = t
	}
	if raw := c.Query("start"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			fail(c, http.StatusBadRequest, "start 需要是 RFC3339 格式")
			return time.Time{}, time.Time{}, false
		}
		start = t
	}
	if !start.Before(end) {
		fail(c, http.StatusBadRequest, "start 必须早于 end")
		return time.Time{}, time.Time{}, false
	}
	// 兜底上限：范围过大时按天聚合也要扫全表，容易把数据库拖住。
	if end.Sub(start) > 90*24*time.Hour {
		fail(c, http.StatusBadRequest, "时间范围不能超过 90 天")
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// dashboardSummary 是概览数字。
type dashboardSummary struct {
	Requests        int64   `json:"requests"`
	Failed          int64   `json:"failed"`
	SuccessRate     float64 `json:"success_rate"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	TotalCost       float64 `json:"total_cost"`
	AvgDurationMs   float64 `json:"avg_duration_ms"`
	AvgFirstTokenMs float64 `json:"avg_first_token_ms"`
}

// seriesPoint 是时间序列上的一个点。
type seriesPoint struct {
	Bucket       time.Time `json:"bucket"`
	Requests     int64     `json:"requests"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	TotalCost    float64   `json:"total_cost"`
}

// breakdownItem 是「按模型」/「按账号」的聚合项。
type breakdownItem struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Requests  int64   `json:"requests"`
	Tokens    int64   `json:"tokens"`
	TotalCost float64 `json:"total_cost"`
}

// Dashboard 返回仪表盘所需的全部数据。
//
// 一次请求把概览、时间序列、分项排行都返回：这几个查询共享同一个时间范围，
// 拆成多个接口会让前端的时间筛选器变成一串并发请求。
func (h *Handler) Dashboard(c *gin.Context) {
	start, end, ok := h.parseRange(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	// 范围超过 2 天就按天聚合，否则按小时——否则一周的图会有 168 个点。
	bucket := "hour"
	if end.Sub(start) > 48*time.Hour {
		bucket = "day"
	}

	summary, err := h.dashboardSummary(ctx, start, end)
	if err != nil {
		h.failInternal(c, "统计概览失败", err)
		return
	}
	series, err := h.dashboardSeries(ctx, start, end, bucket)
	if err != nil {
		h.failInternal(c, "统计时间序列失败", err)
		return
	}
	models, err := h.dashboardByModel(ctx, start, end)
	if err != nil {
		h.failInternal(c, "统计模型分布失败", err)
		return
	}
	accounts, err := h.dashboardByAccount(ctx, start, end)
	if err != nil {
		h.failInternal(c, "统计账号分布失败", err)
		return
	}

	// 账号与密钥的当前状态，用于顶部状态卡。
	accountTotal, _ := h.client.Account.Query().Where(account.DeletedAtIsNil()).Count(ctx)
	accountActive, _ := h.client.Account.Query().Where(account.DeletedAtIsNil(), account.StatusEQ(domain.StatusActive)).Count(ctx)
	keyTotal, _ := h.client.APIKey.Query().Where(apikey.DeletedAtIsNil()).Count(ctx)

	// 按平台拆分的账号数，用于展示两个账号池的规模。
	byPlatform := map[string]int{}
	if rows, err := h.client.QueryContext(ctx,
		`SELECT platform, COUNT(*) FROM accounts WHERE deleted_at IS NULL GROUP BY platform`); err == nil {
		for rows.Next() {
			var platform string
			var count int
			if err := rows.Scan(&platform, &count); err == nil {
				if platform == "" {
					platform = domain.PlatformCommandCode
				}
				byPlatform[platform] = count
			}
		}
		rows.Close()
	} else {
		// 统计失败不该让整个仪表盘挂掉，留空即可。
		h.logger.Warn("统计各平台账号数失败", "err", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"summary":  summary,
		"series":   series,
		"models":   models,
		"accounts": accounts,
		"runtime": gin.H{
			"accounts_total":       accountTotal,
			"accounts_active":      accountActive,
			"api_keys_total":       keyTotal,
			"accounts_by_platform": byPlatform,
			"bucket":               bucket,
		},
		"start": start.UTC().Format(time.RFC3339),
		"end":   end.UTC().Format(time.RFC3339),
	})
}

func (h *Handler) dashboardSummary(ctx context.Context, start, end time.Time) (dashboardSummary, error) {
	const query = `
		SELECT
			COUNT(*)                                            AS requests,
			COUNT(*) FILTER (WHERE status_code >= 400)          AS failed,
			COALESCE(SUM(input_tokens), 0)                      AS input_tokens,
			COALESCE(SUM(output_tokens), 0)                     AS output_tokens,
			COALESCE(SUM(cache_read_tokens), 0)                 AS cache_read_tokens,
			COALESCE(SUM(total_cost), 0)                        AS total_cost,
			COALESCE(AVG(duration_ms), 0)                       AS avg_duration_ms,
			COALESCE(AVG(first_token_ms) FILTER (WHERE first_token_ms IS NOT NULL), 0) AS avg_first_token_ms
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2`

	var out dashboardSummary
	rows, err := h.client.QueryContext(ctx, query, start, end)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	if !rows.Next() {
		return out, rows.Err()
	}
	if err := rows.Scan(
		&out.Requests, &out.Failed, &out.InputTokens, &out.OutputTokens,
		&out.CacheReadTokens, &out.TotalCost, &out.AvgDurationMs, &out.AvgFirstTokenMs,
	); err != nil {
		return out, err
	}
	if out.Requests > 0 {
		out.SuccessRate = float64(out.Requests-out.Failed) / float64(out.Requests)
	}
	return out, rows.Err()
}

func (h *Handler) dashboardSeries(ctx context.Context, start, end time.Time, bucket string) ([]seriesPoint, error) {
	const query = `
		SELECT
			date_trunc($3, created_at)          AS bucket,
			COUNT(*)                            AS requests,
			COALESCE(SUM(input_tokens), 0)      AS input_tokens,
			COALESCE(SUM(output_tokens), 0)     AS output_tokens,
			COALESCE(SUM(total_cost), 0)        AS total_cost
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY bucket
		ORDER BY bucket`

	rows, err := h.client.QueryContext(ctx, query, start, end, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]seriesPoint, 0, 48)
	for rows.Next() {
		var p seriesPoint
		if err := rows.Scan(&p.Bucket, &p.Requests, &p.InputTokens, &p.OutputTokens, &p.TotalCost); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

func (h *Handler) dashboardByModel(ctx context.Context, start, end time.Time) ([]breakdownItem, error) {
	const query = `
		SELECT
			model,
			COUNT(*)                                        AS requests,
			COALESCE(SUM(input_tokens + output_tokens), 0)   AS tokens,
			COALESCE(SUM(total_cost), 0)                     AS total_cost
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY model
		ORDER BY requests DESC
		LIMIT 10`

	rows, err := h.client.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]breakdownItem, 0, 10)
	for rows.Next() {
		var item breakdownItem
		if err := rows.Scan(&item.Key, &item.Requests, &item.Tokens, &item.TotalCost); err != nil {
			return nil, err
		}
		item.Label = item.Key
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *Handler) dashboardByAccount(ctx context.Context, start, end time.Time) ([]breakdownItem, error) {
	// LEFT JOIN 保底：账号被软删除后用量记录仍要能显示出来，
	// 否则历史统计会因为删了一个号而凭空少一块。
	const query = `
		SELECT
			u.account_id::text,
			COALESCE(a.name, '[已删除]')                     AS label,
			COUNT(*)                                        AS requests,
			COALESCE(SUM(u.input_tokens + u.output_tokens), 0) AS tokens,
			COALESCE(SUM(u.total_cost), 0)                   AS total_cost
		FROM usage_logs u
		LEFT JOIN accounts a ON a.id = u.account_id
		WHERE u.created_at >= $1 AND u.created_at < $2
		GROUP BY u.account_id, a.name
		ORDER BY requests DESC
		LIMIT 10`

	rows, err := h.client.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]breakdownItem, 0, 10)
	for rows.Next() {
		var item breakdownItem
		if err := rows.Scan(&item.Key, &item.Label, &item.Requests, &item.Tokens, &item.TotalCost); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
