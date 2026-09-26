package handler

import (
	"net/http"
	"strings"
	"time"

	"cmd2api/ent"
	"cmd2api/ent/account"
	"cmd2api/ent/apikey"
	"cmd2api/ent/group"
	"cmd2api/internal/domain"

	"github.com/gin-gonic/gin"
)

// groupDTO 是分组的对外表示。
type groupDTO struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Platform       string    `json:"platform"`
	RateMultiplier float64   `json:"rate_multiplier"`
	Status         string    `json:"status"`
	AccountCount   int       `json:"account_count"`
	APIKeyCount    int       `json:"api_key_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toGroupDTO(g *ent.Group, accountCount, keyCount int) groupDTO {
	platform := g.Platform
	if platform == "" {
		// 老数据没有 platform 字段时按表默认值处理。
		platform = domain.PlatformCommandCode
	}
	return groupDTO{
		ID:             g.ID,
		Name:           g.Name,
		Description:    g.Description,
		Platform:       platform,
		RateMultiplier: g.RateMultiplier,
		Status:         g.Status,
		AccountCount:   accountCount,
		APIKeyCount:    keyCount,
		CreatedAt:      g.CreatedAt,
		UpdatedAt:      g.UpdatedAt,
	}
}

// ListGroups 返回所有分组，带上账号数与密钥数。
//
// 一次性带出计数而不是让前端逐个请求：分组数量本来就不多，
// 这样列表页一个请求就能画完。
func (h *Handler) ListGroups(c *gin.Context) {
	ctx := c.Request.Context()

	groups, err := h.client.Group.Query().
		Where(group.DeletedAtIsNil()).
		Order(ent.Asc(group.FieldID)).
		All(ctx)
	if err != nil {
		h.failInternal(c, "查询分组列表失败", err)
		return
	}

	items := make([]groupDTO, 0, len(groups))
	for _, g := range groups {
		accountCount, err := g.QueryAccounts().Where(account.DeletedAtIsNil()).Count(ctx)
		if err != nil {
			h.failInternal(c, "统计分组账号数失败", err)
			return
		}
		keyCount, err := g.QueryAPIKeys().Where(apikey.DeletedAtIsNil()).Count(ctx)
		if err != nil {
			h.failInternal(c, "统计分组密钥数失败", err)
			return
		}
		items = append(items, toGroupDTO(g, accountCount, keyCount))
	}

	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

type groupRequest struct {
	Name           string  `json:"name" binding:"required"`
	Description    string  `json:"description"`
	Platform       string  `json:"platform"`
	RateMultiplier float64 `json:"rate_multiplier"`
	Status         string  `json:"status"`
}

// CreateGroup 新建分组。
func (h *Handler) CreateGroup(c *gin.Context) {
	var req groupRequest
	if !bindJSON(c, &req) {
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		fail(c, http.StatusBadRequest, "分组名不能为空")
		return
	}

	platform := req.Platform
	if platform == "" {
		platform = domain.PlatformCommandCode
	}
	if !domain.IsValidPlatform(platform) {
		fail(c, http.StatusBadRequest, "平台取值无效，只支持 commandcode 或 opencode")
		return
	}

	multiplier := req.RateMultiplier
	if multiplier <= 0 {
		multiplier = 1
	}
	status := req.Status
	if status == "" {
		status = domain.StatusActive
	}

	created, err := h.client.Group.Create().
		SetName(name).
		SetDescription(req.Description).
		SetPlatform(platform).
		SetRateMultiplier(multiplier).
		SetStatus(status).
		Save(c.Request.Context())
	if err != nil {
		if isConstraint(err) {
			fail(c, http.StatusConflict, "分组名已存在")
			return
		}
		h.failInternal(c, "创建分组失败", err)
		return
	}

	c.JSON(http.StatusCreated, toGroupDTO(created, 0, 0))
}

// UpdateGroup 修改分组。
func (h *Handler) UpdateGroup(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req groupRequest
	if !bindJSON(c, &req) {
		return
	}

	ctx := c.Request.Context()

	// 平台创建后不可更改：改了意味着组内已有的账号全部平台不匹配，
	// 那些账号会立刻调度不出去。与其让它半坏着，不如明确拒绝。
	if req.Platform != "" {
		current, err := h.client.Group.Query().
			Where(group.IDEQ(id), group.DeletedAtIsNil()).
			Only(ctx)
		if err != nil {
			if isNotFound(err) {
				fail(c, http.StatusNotFound, "分组不存在")
				return
			}
			h.failInternal(c, "查询分组失败", err)
			return
		}
		currentPlatform := current.Platform
		if currentPlatform == "" {
			currentPlatform = domain.PlatformCommandCode
		}
		if req.Platform != currentPlatform {
			fail(c, http.StatusBadRequest,
				"分组平台创建后不可更改（当前 "+currentPlatform+"）。如需换平台，请新建一个分组并把账号迁过去。")
			return
		}
	}

	builder := h.client.Group.UpdateOneID(id)

	if name := strings.TrimSpace(req.Name); name != "" {
		builder = builder.SetName(name)
	}
	builder = builder.SetDescription(req.Description)
	if req.RateMultiplier > 0 {
		builder = builder.SetRateMultiplier(req.RateMultiplier)
	}
	if req.Status != "" {
		builder = builder.SetStatus(req.Status)
	}

	updated, err := builder.Save(ctx)
	if err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "分组不存在")
			return
		}
		if isConstraint(err) {
			fail(c, http.StatusConflict, "分组名已存在")
			return
		}
		h.failInternal(c, "更新分组失败", err)
		return
	}

	c.JSON(http.StatusOK, toGroupDTO(updated, 0, 0))
}

// DeleteGroup 删除分组。
func (h *Handler) DeleteGroup(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	// 还有密钥绑在这个分组上时拒绝删除：直接删会让那些密钥找不到账号池，
	// 调用方只会收到「未绑定分组」这种莫名其妙的错误。
	keyCount, err := h.client.APIKey.Query().
		Where(apikey.GroupIDEQ(id), apikey.DeletedAtIsNil()).
		Count(ctx)
	if err != nil {
		h.failInternal(c, "统计分组密钥数失败", err)
		return
	}
	if keyCount > 0 {
		fail(c, http.StatusConflict, "该分组下还有 API Key，请先迁移或删除这些 Key")
		return
	}

	if err := h.client.Group.DeleteOneID(id).Exec(ctx); err != nil {
		if isNotFound(err) {
			fail(c, http.StatusNotFound, "分组不存在")
			return
		}
		h.failInternal(c, "删除分组失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}
