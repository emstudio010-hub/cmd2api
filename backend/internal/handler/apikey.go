package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/apikey"
	"cmd2api/ent/group"
	"cmd2api/internal/auth"
	"cmd2api/internal/domain"

	"github.com/gin-gonic/gin"
)

// apiKeyDTO 是下游密钥的对外表示。
type apiKeyDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	UserID    int64  `json:"user_id"`
	GroupID   *int64 `json:"group_id"`
	GroupName string `json:"group_name"`
	Status    string `json:"status"`

	Quota     float64 `json:"quota"`
	QuotaUsed float64 `json:"quota_used"`

	IPWhitelist []string `json:"ip_whitelist"`

	LastUsedAt *string   `json:"last_used_at"`
	ExpiresAt  *string   `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toAPIKeyDTO(k *ent.APIKey) apiKeyDTO {
	dto := apiKeyDTO{
		ID:          k.ID,
		Name:        k.Name,
		Key:         k.Key,
		UserID:      k.UserID,
		GroupID:     k.GroupID,
		Status:      k.Status,
		Quota:       k.Quota,
		QuotaUsed:   k.QuotaUsed,
		IPWhitelist: k.IPWhitelist,
		LastUsedAt:  timePtr(k.LastUsedAt),
		ExpiresAt:   timePtr(k.ExpiresAt),
		CreatedAt:   k.CreatedAt,
		UpdatedAt:   k.UpdatedAt,
	}
	if k.Edges.Group != nil {
		dto.GroupName = k.Edges.Group.Name
	}
	return dto
}

// ListAPIKeys 返回所有下游密钥。
func (h *Handler) ListAPIKeys(c *gin.Context) {
	ctx := c.Request.Context()
	query := h.client.APIKey.Query().Where(apikey.DeletedAtIsNil())

	if groupID := queryInt(c, "group_id", 0); groupID > 0 {
		query = query.Where(apikey.GroupIDEQ(int64(groupID)))
	}
	if status := c.Query("status"); status != "" {
		query = query.Where(apikey.StatusEQ(status))
	}
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		query = query.Where(apikey.NameContainsFold(keyword))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		h.failInternal(c, "统计密钥数量失败", err)
		return
	}

	rows, err := query.
		WithGroup().
		Order(ent.Desc(apikey.FieldID)).
		Limit(200).
		All(ctx)
	if err != nil {
		h.failInternal(c, "查询密钥列表失败", err)
		return
	}

	items := make([]apiKeyDTO, 0, len(rows))
	for _, k := range rows {
		items = append(items, toAPIKeyDTO(k))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

type createAPIKeyRequest struct {
	Name        string   `json:"name" binding:"required"`
	GroupID     *int64   `json:"group_id"`
	Quota       float64  `json:"quota"`
	IPWhitelist []string `json:"ip_whitelist"`
	ExpiresAt   *string  `json:"expires_at"`
}

// CreateAPIKey 签发一把新的下游密钥。
func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req createAPIKeyRequest
	if !bindJSON(c, &req) {
		return
	}
	claims, ok := adminClaims(c)
	if !ok {
		return
	}

	expiresAt, ok := parseOptionalTime(c, req.ExpiresAt)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	// 没指定分组时自动挑一个可用的：绝大多数部署只有一个分组，
	// 强迫管理员每次都手选纯属多余。
	groupID := req.GroupID
	if groupID == nil {
		auto, err := h.firstActiveGroupID(ctx)
		if err != nil {
			h.failInternal(c, "查询分组失败", err)
			return
		}
		if auto == 0 {
			fail(c, http.StatusBadRequest, "还没有任何分组，请先创建一个分组并绑定账号")
			return
		}
		groupID = &auto
	} else if _, err := h.client.Group.Query().
		Where(group.IDEQ(*groupID), group.DeletedAtIsNil()).
		Only(ctx); err != nil {
		fail(c, http.StatusBadRequest, "指定的分组不存在")
		return
	}

	raw, err := auth.GenerateAPIKey()
	if err != nil {
		h.failInternal(c, "生成密钥失败", err)
		return
	}

	builder := h.client.APIKey.Create().
		SetName(strings.TrimSpace(req.Name)).
		SetKey(raw).
		SetUserID(claims.UserID).
		SetStatus(domain.StatusActive).
		SetQuota(req.Quota).
		SetIPWhitelist(normalizeWhitelist(req.IPWhitelist))

	if groupID != nil {
		builder = builder.SetGroupID(*groupID)
	}
	if expiresAt != nil {
		builder = builder.SetExpiresAt(*expiresAt)
	}

	created, err := builder.Save(ctx)
	if err != nil {
		h.failInternal(c, "创建密钥失败", err)
		return
	}

	h.logger.Info("已签发 API Key", "api_key_id", created.ID, "name", created.Name, "group_id", groupID)

	// 明文只在这里返回一次。列表接口虽然也带 key（管理员需要复制），
	// 但创建这一刻是唯一保证用户能拿到完整值的地方。
	c.JSON(http.StatusCreated, toAPIKeyDTO(created))
}

type updateAPIKeyRequest struct {
	Name        *string   `json:"name"`
	GroupID     *int64    `json:"group_id"`
	Status      *string   `json:"status"`
	Quota       *float64  `json:"quota"`
	IPWhitelist *[]string `json:"ip_whitelist"`
	ExpiresAt   *string   `json:"expires_at"`
}

// UpdateAPIKey 修改密钥配置。
func (h *Handler) UpdateAPIKey(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req updateAPIKeyRequest
	if !bindJSON(c, &req) {
		return
	}
	expiresAt, ok := parseOptionalTime(c, req.ExpiresAt)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	builder := h.client.APIKey.UpdateOneID(id)

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			fail(c, http.StatusBadRequest, "名称不能为空")
			return
		}
		builder = builder.SetName(name)
	}
	if req.Status != nil {
		builder = builder.SetStatus(*req.Status)
	}
	if req.Quota != nil {
		builder = builder.SetQuota(*req.Quota)
	}
	if req.IPWhitelist != nil {
		builder = builder.SetIPWhitelist(normalizeWhitelist(*req.IPWhitelist))
	}
	if req.GroupID != nil {
		if _, err := h.client.Group.Query().
			Where(group.IDEQ(*req.GroupID), group.DeletedAtIsNil()).
			Only(ctx); err != nil {
			fail(c, http.StatusBadRequest, "指定的分组不存在")
			return
		}
		builder = builder.SetGroupID(*req.GroupID)
	}
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			builder = builder.ClearExpiresAt()
		} else {
			builder = builder.SetExpiresAt(*expiresAt)
		}
	}

	updated, err := builder.Save(ctx)
	if err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "密钥不存在")
			return
		}
		h.failInternal(c, "更新密钥失败", err)
		return
	}
	c.JSON(http.StatusOK, toAPIKeyDTO(updated))
}

// DeleteAPIKey 删除密钥。
func (h *Handler) DeleteAPIKey(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.client.APIKey.DeleteOneID(id).Exec(c.Request.Context()); err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "密钥不存在")
			return
		}
		h.failInternal(c, "删除密钥失败", err)
		return
	}
	h.logger.Info("已删除 API Key", "api_key_id", id)
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

// ResetAPIKeyQuota 把已用额度清零。
func (h *Handler) ResetAPIKeyQuota(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if _, err := h.client.APIKey.UpdateOneID(id).SetQuotaUsed(0).Save(c.Request.Context()); err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "密钥不存在")
			return
		}
		h.failInternal(c, "重置额度失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已重置"})
}

func (h *Handler) firstActiveGroupID(ctx context.Context) (int64, error) {
	g, err := h.client.Group.Query().
		Where(group.DeletedAtIsNil(), group.StatusEQ(domain.StatusActive)).
		Order(ent.Asc(group.FieldID)).
		First(ctx)
	if err != nil {
		if isNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	return g.ID, nil
}

// normalizeWhitelist 去掉空白项，并统一成小写无关的干净值。
func normalizeWhitelist(in []string) []string {
	out := make([]string, 0, len(in))
	for _, entry := range in {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
