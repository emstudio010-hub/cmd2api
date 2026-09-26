package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/account"
	"cmd2api/ent/group"
	"cmd2api/internal/domain"
	"cmd2api/internal/service"

	"github.com/gin-gonic/gin"
)

// accountDTO 是账号的对外表示。
//
// 绝不含明文密钥——列表页只需要让管理员认出「是哪一把」，用 mask 后的形式。
type accountDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Notes    string `json:"notes"`
	Platform string `json:"platform"`
	Type     string `json:"type"`
	Status   string `json:"status"`

	// AccountMode / BaseURL 只有 OpenCode 账号有值；
	// Command Code 账号两者都是空字符串。
	AccountMode string `json:"account_mode"`
	BaseURL     string `json:"base_url"`

	ErrorMessage *string `json:"error_message"`
	MaskedKey    string  `json:"masked_key"`

	Concurrency    int     `json:"concurrency"`
	Priority       int     `json:"priority"`
	RateMultiplier float64 `json:"rate_multiplier"`
	Schedulable    bool    `json:"schedulable"`

	ConsecutiveFailures  int     `json:"consecutive_failures"`
	LastUsedAt           *string `json:"last_used_at"`
	ExpiresAt            *string `json:"expires_at"`
	LastHealthCheckAt    *string `json:"last_health_check_at"`
	LastHealthCheckOk    bool    `json:"last_health_check_ok"`
	LastHealthCheckError *string `json:"last_health_check_error"`
	LatencyMs            *int    `json:"latency_ms"`

	GroupIDs []int64 `json:"group_ids"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (h *Handler) toAccountDTO(acc *ent.Account) accountDTO {
	dto := accountDTO{
		ID:                   acc.ID,
		Name:                 acc.Name,
		Platform:             acc.Platform,
		Type:                 acc.Type,
		Status:               acc.Status,
		ErrorMessage:         acc.ErrorMessage,
		Concurrency:          acc.Concurrency,
		Priority:             acc.Priority,
		RateMultiplier:       acc.RateMultiplier,
		Schedulable:          acc.Schedulable,
		ConsecutiveFailures:  acc.ConsecutiveFailures,
		LastUsedAt:           timePtr(acc.LastUsedAt),
		ExpiresAt:            timePtr(acc.ExpiresAt),
		LastHealthCheckAt:    timePtr(acc.LastHealthCheckAt),
		LastHealthCheckOk:    acc.LastHealthCheckOk,
		LastHealthCheckError: acc.LastHealthCheckError,
		LatencyMs:            acc.LatencyMs,
		GroupIDs:             []int64{},
		CreatedAt:            acc.CreatedAt,
		UpdatedAt:            acc.UpdatedAt,
	}
	if acc.Notes != nil {
		dto.Notes = *acc.Notes
	}
	if h.accounts != nil {
		dto.MaskedKey = h.accounts.MaskedKey(acc)
	}
	// 从加载好的边里取分组 ID。调用方负责 WithGroups()；
	// 没加载时这里是空数组，而不是接口漏字段。
	for _, g := range acc.Edges.Groups {
		dto.GroupIDs = append(dto.GroupIDs, g.ID)
	}
	// account_mode / base_url 存在 extra JSONB 里，取出来平铺到顶层，
	// 前端不必知道它是怎么存的。
	if acc.Extra != nil {
		dto.AccountMode, _ = acc.Extra["account_mode"].(string)
		dto.BaseURL, _ = acc.Extra["base_url"].(string)
		// 没显式配 base_url 时回填默认值，让界面能显示账号实际会请求到哪里。
		if dto.BaseURL == "" && dto.Platform == domain.PlatformOpenCode {
			dto.BaseURL = domain.DefaultOpenCodeBaseURL(dto.AccountMode)
		}
	}
	return dto
}

// ListAccounts 分页返回账号列表。
func (h *Handler) ListAccounts(c *gin.Context) {
	limit, offset := pageParams(c)
	ctx := c.Request.Context()

	query := h.client.Account.Query().Where(account.DeletedAtIsNil())

	if status := c.Query("status"); status != "" {
		query = query.Where(account.StatusEQ(status))
	}
	if platform := c.Query("platform"); platform != "" {
		query = query.Where(account.PlatformEQ(platform))
	}
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		query = query.Where(account.NameContainsFold(keyword))
	}
	if groupID := queryInt(c, "group_id", 0); groupID > 0 {
		query = query.Where(account.HasGroupsWith(group.IDEQ(int64(groupID))))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		h.failInternal(c, "统计账号数量失败", err)
		return
	}

	rows, err := query.
		WithGroups().
		Order(ent.Asc(account.FieldPriority), ent.Desc(account.FieldID)).
		Limit(limit).
		Offset(offset).
		All(ctx)
	if err != nil {
		h.failInternal(c, "查询账号列表失败", err)
		return
	}

	items := make([]accountDTO, 0, len(rows))
	for _, acc := range rows {
		items = append(items, h.toAccountDTO(acc))
	}

	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page_size": limit,
		"page":      offset/limit + 1,
	})
}

// GetAccount 返回单个账号详情。
func (h *Handler) GetAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	acc, err := h.client.Account.Query().
		Where(account.IDEQ(id), account.DeletedAtIsNil()).
		WithGroups().
		Only(c.Request.Context())
	if err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "账号不存在")
			return
		}
		h.failInternal(c, "查询账号失败", err)
		return
	}

	c.JSON(http.StatusOK, h.toAccountDTO(acc))
}

type createAccountRequest struct {
	Name string `json:"name" binding:"required"`
	// Platform 必填：commandcode 或 opencode。
	Platform string `json:"platform" binding:"required"`
	// AccountMode 仅 OpenCode 需要（zen / go）。
	AccountMode string `json:"account_mode"`
	// BaseURL 仅 OpenCode 用，留空则按 AccountMode 取默认地址。
	BaseURL        string  `json:"base_url"`
	Notes          string  `json:"notes"`
	APIKey         string  `json:"api_key" binding:"required"`
	Concurrency    int     `json:"concurrency"`
	Priority       int     `json:"priority"`
	RateMultiplier float64 `json:"rate_multiplier"`
	GroupIDs       []int64 `json:"group_ids"`
	ExpiresAt      *string `json:"expires_at"`
}

// CreateAccount 新建账号。
func (h *Handler) CreateAccount(c *gin.Context) {
	var req createAccountRequest
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

	acc, err := h.accounts.Create(c.Request.Context(), service.CreateAccountInput{
		Name:           req.Name,
		Platform:       req.Platform,
		AccountMode:    req.AccountMode,
		BaseURL:        req.BaseURL,
		Notes:          req.Notes,
		APIKey:         req.APIKey,
		Concurrency:    req.Concurrency,
		Priority:       req.Priority,
		RateMultiplier: req.RateMultiplier,
		GroupIDs:       req.GroupIDs,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		h.writeAccountError(c, err, "创建账号失败")
		return
	}
	c.JSON(http.StatusCreated, h.toAccountDTO(acc))
}

type updateAccountRequest struct {
	Name           *string  `json:"name"`
	Notes          *string  `json:"notes"`
	APIKey         *string  `json:"api_key"`
	Concurrency    *int     `json:"concurrency"`
	Priority       *int     `json:"priority"`
	RateMultiplier *float64 `json:"rate_multiplier"`
	Status         *string  `json:"status"`
	Schedulable    *bool    `json:"schedulable"`
	GroupIDs       *[]int64 `json:"group_ids"`
	ExpiresAt      *string  `json:"expires_at"`
}

// UpdateAccount 修改账号。
func (h *Handler) UpdateAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req updateAccountRequest
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

	acc, err := h.accounts.Update(c.Request.Context(), id, service.UpdateAccountInput{
		Name:           req.Name,
		Notes:          req.Notes,
		APIKey:         req.APIKey,
		Concurrency:    req.Concurrency,
		Priority:       req.Priority,
		RateMultiplier: req.RateMultiplier,
		Status:         req.Status,
		Schedulable:    req.Schedulable,
		GroupIDs:       req.GroupIDs,
		ExpiresAt:      expiresAt,
		// 传了空字符串视为「清除过期时间」，这是唯一能让字段从有值变空的途径。
		ClearExpiry: req.ExpiresAt != nil && *req.ExpiresAt == "",
	})
	if err != nil {
		h.writeAccountError(c, err, "更新账号失败")
		return
	}
	c.JSON(http.StatusOK, h.toAccountDTO(acc))
}

// DeleteAccount 删除账号。
func (h *Handler) DeleteAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if h.accounts == nil {
		fail(c, http.StatusServiceUnavailable, "账号服务未启用")
		return
	}
	if err := h.accounts.Delete(c.Request.Context(), id); err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "账号不存在")
			return
		}
		h.failInternal(c, "删除账号失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

// CheckAccount 探活单个账号。
//
// 这个接口会真的向上游发一次极小请求（消耗几十个 token），
// 但它是唯一能确认「账号现在到底能不能干活」的方式。
func (h *Handler) CheckAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if h.accounts == nil {
		fail(c, http.StatusServiceUnavailable, "账号服务未启用")
		return
	}

	result, err := h.accounts.Check(c.Request.Context(), id)
	if err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "账号不存在")
			return
		}
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	// 探活失败不算 HTTP 错误：请求本身成功了，只是探测结论是「账号不可用」。
	c.JSON(http.StatusOK, gin.H{
		"ok":         result.Err == nil,
		"latency_ms": result.Latency.Milliseconds(),
		"error":      result.ErrorMessage(),
	})
}

type batchImportRequest struct {
	// Keys 每行一个密钥，允许 "密钥" 或 "名称,密钥" 两种写法。
	Keys     string `json:"keys" binding:"required"`
	Platform string `json:"platform" binding:"required"`
	// AccountMode / BaseURL 语义同单个创建，仅 OpenCode 用。
	AccountMode string  `json:"account_mode"`
	BaseURL     string  `json:"base_url"`
	GroupIDs    []int64 `json:"group_ids"`
	Concurrency int     `json:"concurrency"`
	Priority    int     `json:"priority"`
}

// BatchImportAccounts 批量导入账号。
func (h *Handler) BatchImportAccounts(c *gin.Context) {
	var req batchImportRequest
	if !bindJSON(c, &req) {
		return
	}
	if h.accounts == nil {
		fail(c, http.StatusServiceUnavailable, "账号服务未启用")
		return
	}

	lines := parseKeyLines(req.Keys)
	if len(lines) == 0 {
		fail(c, http.StatusBadRequest, "没有解析到任何密钥")
		return
	}

	type failure struct {
		Line  int    `json:"line"`
		Key   string `json:"key"`
		Error string `json:"error"`
	}
	created := make([]int64, 0, len(lines))
	failures := make([]failure, 0)

	for i, item := range lines {
		name := item.Name
		if name == "" {
			// 没给名字就按密钥前缀起一个，方便管理员在列表里对号入座。
			name = platformLabel(req.Platform) + " 账号 " + item.ShortKey()
		}
		acc, err := h.accounts.Create(c.Request.Context(), service.CreateAccountInput{
			Name:        name,
			Platform:    req.Platform,
			AccountMode: req.AccountMode,
			BaseURL:     req.BaseURL,
			APIKey:      item.Key,
			Concurrency: req.Concurrency,
			Priority:    req.Priority,
			GroupIDs:    req.GroupIDs,
		})
		if err != nil {
			failures = append(failures, failure{Line: i + 1, Key: item.ShortKey(), Error: err.Error()})
			continue
		}
		created = append(created, acc.ID)
	}

	c.JSON(http.StatusOK, gin.H{
		"created":     len(created),
		"created_ids": created,
		"failed":      len(failures),
		"failures":    failures,
	})
}

// accountKeyLine 是批量导入里解析出的一条。
type accountKeyLine struct {
	Name string
	Key  string
}

// ShortKey 返回用于回显的脱敏形式。
func (l accountKeyLine) ShortKey() string {
	if len(l.Key) <= 8 {
		return "****"
	}
	return l.Key[:8] + "…"
}

// parseKeyLines 解析批量导入的文本。
//
// 支持 "名称,密钥" 和只有密钥两种写法，空行与 # 注释忽略。
func parseKeyLines(raw string) []accountKeyLine {
	var out []accountKeyLine
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.LastIndex(line, ","); idx >= 0 {
			name := strings.TrimSpace(line[:idx])
			key := strings.TrimSpace(line[idx+1:])
			if key != "" {
				out = append(out, accountKeyLine{Name: name, Key: key})
				continue
			}
		}
		out = append(out, accountKeyLine{Key: line})
	}
	return out
}

// parseOptionalTime 解析可选的 RFC3339 时间字符串。
func parseOptionalTime(c *gin.Context, raw *string) (*time.Time, bool) {
	if raw == nil || *raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		fail(c, http.StatusBadRequest, "expires_at 需要是 RFC3339 格式，例如 2026-12-31T00:00:00Z")
		return nil, false
	}
	return &t, true
}

// writeAccountError 把账号服务的错误翻成合适的 HTTP 状态码。
func (h *Handler) writeAccountError(c *gin.Context, err error, fallback string) {
	switch {
	case err == nil:
		return
	case isNotFound(err):
		fail(c, http.StatusNotFound, "账号不存在")
	case isConstraint(err):
		fail(c, http.StatusConflict, "账号名已存在")
	case err == service.ErrInvalidAPIKey:
		// 这是用户输入问题，直接把原因说清楚，别让他去猜格式。
		fail(c, http.StatusBadRequest, "密钥格式不正确，Command Code 的密钥应以 user_ 开头")
	case err == service.ErrInvalidPlatform,
		err == service.ErrInvalidAccountMode,
		errors.Is(err, service.ErrPlatformMismatch):
		// 平台/分组不匹配属于配置错误，回显具体原因比笼统的 500 有用得多。
		fail(c, http.StatusBadRequest, err.Error())
	default:
		// 业务校验类错误（并发数必须大于 0 等）直接回显；其余按内部错误处理。
		msg := err.Error()
		if strings.Contains(msg, "不能为空") || strings.Contains(msg, "必须大于") ||
			strings.Contains(msg, "不存在的分组") {
			fail(c, http.StatusBadRequest, msg)
			return
		}
		h.failInternal(c, fallback, err)
	}
}
