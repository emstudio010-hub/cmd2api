package handler

import (
	"net/http"
	"time"

	"cmd2api/ent/user"
	"cmd2api/internal/auth"
	"cmd2api/internal/domain"
	"cmd2api/internal/middleware"

	"github.com/gin-gonic/gin"
)

type loginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// userDTO 是对外暴露的用户信息。不直接返回 ent 实体：那会带上 password_hash，
// 而且 Edges 字段会漏出去。
type userDTO struct {
	ID          int64     `json:"id"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	LastLoginAt *string   `json:"last_login_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// Login 处理管理员登录。
func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if !bindJSON(c, &req) {
		return
	}

	ctx := c.Request.Context()
	u, err := h.client.User.Query().
		Where(
			user.EmailEQ(req.Email),
			user.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		// 用户不存在与密码错误返回同样的信息，避免把「这个邮箱存在吗」
		// 暴露给探测者。
		h.logger.Info("登录失败：账号不存在", "email", req.Email, "ip", c.ClientIP())
		fail(c, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}

	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		h.logger.Info("登录失败：密码错误", "email", req.Email, "ip", c.ClientIP())
		fail(c, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}

	if u.Status != domain.StatusActive {
		fail(c, http.StatusForbidden, "该账号已被禁用")
		return
	}
	if u.Role != domain.RoleAdmin {
		// 仅管理员模式：普通用户没有可登录的入口。
		fail(c, http.StatusForbidden, "需要管理员权限")
		return
	}

	token, expiresAt, err := h.issuer.Issue(u.ID, u.Email, u.Role)
	if err != nil {
		h.failInternal(c, "签发登录令牌失败", err)
		return
	}

	// 登录时间回写失败不影响登录本身。
	if _, err := h.client.User.UpdateOneID(u.ID).SetLastLoginAt(time.Now()).Save(ctx); err != nil {
		h.logger.Warn("更新登录时间失败", "err", err, "user_id", u.ID)
	}
	h.logger.Info("管理员登录成功", "email", u.Email, "ip", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"token":      token,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
		"user": userDTO{
			ID:          u.ID,
			Email:       u.Email,
			Role:        u.Role,
			Status:      u.Status,
			LastLoginAt: timePtr(u.LastLoginAt),
			CreatedAt:   u.CreatedAt,
		},
	})
}

// Me 返回当前登录的管理员信息。
//
// 前端用它做启动时的登录态校验：令牌可能已经过期，靠这个接口探一次。
func (h *Handler) Me(c *gin.Context) {
	claims, ok := middleware.AdminClaimsFrom(c)
	if !ok {
		fail(c, http.StatusUnauthorized, "未登录")
		return
	}
	u, err := h.client.User.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		fail(c, http.StatusUnauthorized, "账号不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user": userDTO{
			ID:          u.ID,
			Email:       u.Email,
			Role:        u.Role,
			Status:      u.Status,
			LastLoginAt: timePtr(u.LastLoginAt),
			CreatedAt:   u.CreatedAt,
		},
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

// ChangePassword 修改当前管理员的密码。
func (h *Handler) ChangePassword(c *gin.Context) {
	claims, ok := middleware.AdminClaimsFrom(c)
	if !ok {
		fail(c, http.StatusUnauthorized, "未登录")
		return
	}
	var req changePasswordRequest
	if !bindJSON(c, &req) {
		return
	}

	ctx := c.Request.Context()
	u, err := h.client.User.Get(ctx, claims.UserID)
	if err != nil {
		fail(c, http.StatusUnauthorized, "账号不存在")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.CurrentPassword) {
		fail(c, http.StatusBadRequest, "当前密码不正确")
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		h.failInternal(c, "生成密码哈希失败", err)
		return
	}
	if _, err := h.client.User.UpdateOneID(u.ID).SetPasswordHash(hash).Save(ctx); err != nil {
		h.failInternal(c, "更新密码失败", err)
		return
	}

	h.logger.Info("管理员修改了密码", "email", u.Email, "ip", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "密码已更新"})
}
