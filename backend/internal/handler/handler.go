// Package handler 实现管理后台的 HTTP 接口与下游 API 的中转入口。
package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"cmd2api/ent"
	"cmd2api/internal/auth"
	"cmd2api/internal/config"
	"cmd2api/internal/crypto"
	"cmd2api/internal/domain"
	"cmd2api/internal/middleware"
	"cmd2api/internal/relay"
	"cmd2api/internal/scheduler"
	"cmd2api/internal/service"

	"github.com/gin-gonic/gin"
)

// Handler 汇总所有接口共用的依赖。
type Handler struct {
	client   *ent.Client
	vault    *crypto.Vault
	issuer   *auth.Issuer
	sched    *scheduler.Scheduler
	relay    *relay.Service
	accounts *service.AccountService
	cfg      *config.Config
	logger   *slog.Logger
}

// New 构造 Handler。
//
// relayService 允许为 nil：管理后台的接口不依赖它，这样只跑后台、
// 或者上游还没配置好的情况下服务也能起来并给出可读的报错。
func New(
	client *ent.Client,
	vault *crypto.Vault,
	issuer *auth.Issuer,
	sched *scheduler.Scheduler,
	relayService *relay.Service,
	accounts *service.AccountService,
	cfg *config.Config,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		client:   client,
		vault:    vault,
		issuer:   issuer,
		sched:    sched,
		relay:    relayService,
		accounts: accounts,
		cfg:      cfg,
		logger:   logger,
	}
}

// ---- 通用响应helper ----

// fail 返回统一格式的错误。
func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"error": msg})
}

// failInternal 记录真实错误，但只把笼统信息返回给调用方。
//
// 不把内部错误直接透出去：数据库报错里常带表名、约束名甚至连接串片段，
// 这些对排查有用，对攻击者同样有用。
func (h *Handler) failInternal(c *gin.Context, msg string, err error) {
	h.logger.Error(msg, "err", err, "path", c.FullPath(), "method", c.Request.Method)
	fail(c, http.StatusInternalServerError, msg)
}

// bindJSON 解析请求体，失败时直接写回 400。
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		fail(c, http.StatusBadRequest, "请求体格式错误: "+err.Error())
		return false
	}
	return true
}

// pathID 取出路径里的 :id 并转成 int64。
func pathID(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, "路径参数 id 无效")
		return 0, false
	}
	return id, true
}

// queryInt 读取查询参数里的整数，缺省时用 def。
func queryInt(c *gin.Context, name string, def int) int {
	if v := c.Query(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// pageParams 归一化分页参数，把越界值夹到合理范围。
//
// 不夹的话，page_size=1000000 就是一次全表扫描，管理后台被人随手一填
// 就能把数据库拖垮。
func pageParams(c *gin.Context) (limit, offset int) {
	const (
		defaultSize = 50
		maxSize     = 200
	)
	page := queryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	size := queryInt(c, "page_size", defaultSize)
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return size, (page - 1) * size
}

// timePtr 把可空时间转成 JSON 友好的形式。
func timePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// platformLabel 返回平台的中文展示名，用于自动生成账号名等场景。
func platformLabel(platform string) string {
	if platform == domain.PlatformOpenCode {
		return "OpenCode"
	}
	return "Command Code"
}

// adminClaims 取出当前登录声明，未登录时直接写回 401。
//
// 中间件已经保证受保护路由一定带声明，这里只是省掉每个 handler 重复写
// 「取不到就报错」那几行。
func adminClaims(c *gin.Context) (*auth.Claims, bool) {
	claims, ok := middleware.AdminClaimsFrom(c)
	if !ok {
		fail(c, http.StatusUnauthorized, "未登录")
		return nil, false
	}
	return claims, true
}

// isNotFound 判断 ent 是否返回了「记录不存在」。
func isNotFound(err error) bool {
	return ent.IsNotFound(err)
}

// isConstraint 判断是否命中唯一约束冲突。
func isConstraint(err error) bool {
	return ent.IsConstraintError(err)
}

// errNotFound 是内部用的哨兵，便于 handler 层统一处理。
var errNotFound = errors.New("记录不存在")
