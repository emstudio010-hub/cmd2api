package handler

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"cmd2api/ent"
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

type changeEmailRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	Email           string `json:"email" binding:"required"`
}

// maxEmailLen 跟 user 表里 email 字段的 MaxLen 保持一致。
//
// 在这里先挡一道，是为了给出「太长了」这种能看懂的话；走到数据库那层
// 报的是约束错误，对用户毫无用处。
const maxEmailLen = 255

// UpdateEmail 修改当前管理员的登录用户名（也就是邮箱）。
//
// 这个系统没有单独的「用户名」字段——登录身份就是 email，所以改用户名
// 等于改登录邮箱。
//
// 要当前密码：这是换掉**登录入口**的操作，只凭一个已登录的令牌就允许改，
// 等于把「令牌被偷」升级成「账号被永久接管」。密码是这道门唯一的把手。
func (h *Handler) UpdateEmail(c *gin.Context) {
	claims, ok := adminClaims(c)
	if !ok {
		return
	}
	var req changeEmailRequest
	if !bindJSON(c, &req) {
		return
	}

	email := strings.TrimSpace(req.Email)
	if msg := validateEmail(email); msg != "" {
		fail(c, http.StatusBadRequest, msg)
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

	// 已经是这个邮箱就不用写库了。也让「只改大小写」这种情况不必先过
	// 唯一性检查——因为它命中的正是自己。
	if strings.EqualFold(u.Email, email) {
		payload, ok := h.emailChangedPayload(c, u, u.Email)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, payload)
		return
	}

	if _, err := h.client.User.UpdateOneID(u.ID).SetEmail(email).Save(ctx); err != nil {
		if isConstraint(err) {
			fail(c, http.StatusConflict, "这个邮箱已经被占用了")
			return
		}
		h.failInternal(c, "更新登录用户名失败", err)
		return
	}

	h.logger.Info("管理员修改了登录用户名",
		"user_id", u.ID, "old_email", u.Email, "new_email", email, "ip", c.ClientIP())

	// 换一把令牌：邮箱是写进 JWT 声明里的，不换的话旧令牌会一直带着旧邮箱。
	// 拿到新令牌的浏览器不会有任何感觉，只是本地存的那份换掉了。
	u.Email = email
	payload, ok := h.emailChangedPayload(c, u, email)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, payload)
}

// emailChangedPayload 签发新令牌并拼出响应。
//
// 返回 false 表示签发失败、响应已经写好了（failInternal 写过了），调用方
// 必须直接 return——否则会再写一次 ResponseWriter，那个 panic 是 gin 里
// 最难查的一类。
func (h *Handler) emailChangedPayload(c *gin.Context, u *ent.User, email string) (gin.H, bool) {
	token, expiresAt, err := h.issuer.Issue(u.ID, email, u.Role)
	if err != nil {
		h.failInternal(c, "签发登录令牌失败", err)
		return nil, false
	}
	return gin.H{
		"message":    "登录用户名已更新",
		"token":      token,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
		"user": userDTO{
			ID:          u.ID,
			Email:       email,
			Role:        u.Role,
			Status:      u.Status,
			LastLoginAt: timePtr(u.LastLoginAt),
			CreatedAt:   u.CreatedAt,
		},
	}, true
}

// validateEmail 粗筛邮箱，返回空串表示通过。
//
// 用 net/mail 而不是自己写正则：邮箱的合法形态比看上去复杂得多，自己写的
// 正则要么放过一堆垃圾，要么把合法地址挡在外面。这里要的只是「别让人把
// 一串空格或者一个汉字存成登录名」。
func validateEmail(email string) string {
	if email == "" {
		return "请输入登录用户名（邮箱）"
	}
	if len(email) > maxEmailLen {
		return "用户名太长了，最多 " + strconv.Itoa(maxEmailLen) + " 个字符"
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return "用户名里不能有空格"
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "请填写一个合法的邮箱地址"
	}
	return ""
}
