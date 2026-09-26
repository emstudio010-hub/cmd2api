// Package server 组装 HTTP 路由与静态资源。
package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cmd2api/ent"
	"cmd2api/internal/auth"
	"cmd2api/internal/handler"
	"cmd2api/internal/middleware"

	"github.com/gin-gonic/gin"
)

// Options 是构建路由所需的参数。
type Options struct {
	Handler *handler.Handler
	Issuer  *auth.Issuer
	Client  *ent.Client
	// StaticDir 是前端构建产物目录。为空或不存在时只提供 API。
	StaticDir string
	Mode      string
}

// NewRouter 构建 gin 引擎。
func NewRouter(opts Options) *gin.Engine {
	gin.SetMode(opts.Mode)

	r := gin.New()
	r.Use(gin.Recovery())
	// 结构化访问日志。默认的 gin.Logger 输出不好过滤，也不带请求耗时分布，
	// 这里用自定义的：只记路径、状态、耗时和请求 ID，不记请求体（可能含用户代码）。
	r.Use(accessLog())

	// 健康检查放在鉴权之外，供容器探针和反向代理使用。
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	h := opts.Handler

	// ---- 管理后台 ----
	api := r.Group("/api")
	{
		// 登录本身不能要求已登录。
		api.POST("/auth/login", h.Login)

		admin := api.Group("")
		admin.Use(middleware.AdminAuth(opts.Issuer))
		{
			admin.GET("/auth/me", h.Me)
			admin.POST("/auth/password", h.ChangePassword)

			admin.GET("/dashboard", h.Dashboard)

			admin.GET("/accounts", h.ListAccounts)
			admin.POST("/accounts", h.CreateAccount)
			admin.POST("/accounts/batch", h.BatchImportAccounts)
			admin.GET("/accounts/:id", h.GetAccount)
			admin.PUT("/accounts/:id", h.UpdateAccount)
			admin.DELETE("/accounts/:id", h.DeleteAccount)
			admin.POST("/accounts/:id/check", h.CheckAccount)

			admin.GET("/groups", h.ListGroups)
			admin.POST("/groups", h.CreateGroup)
			admin.PUT("/groups/:id", h.UpdateGroup)
			admin.DELETE("/groups/:id", h.DeleteGroup)

			admin.GET("/keys", h.ListAPIKeys)
			admin.POST("/keys", h.CreateAPIKey)
			admin.PUT("/keys/:id", h.UpdateAPIKey)
			admin.DELETE("/keys/:id", h.DeleteAPIKey)
			admin.POST("/keys/:id/reset-quota", h.ResetAPIKeyQuota)

			admin.GET("/logs", h.ListUsageLogs)

			admin.GET("/models", h.AdminListModels)
			admin.GET("/settings", h.Settings)
		}
	}

	// ---- 下游 API（用 cmd2api 签发的密钥鉴权）----
	v1 := r.Group("/v1")
	v1.Use(middleware.APIKeyAuth(opts.Client))
	{
		v1.POST("/chat/completions", h.ChatCompletions)
		v1.POST("/messages", h.Messages)
		v1.GET("/models", h.ListModels)
	}

	attachStatic(r, opts.StaticDir)
	return r
}

// attachStatic 挂载前端静态资源，并为前端路由提供 SPA fallback。
func attachStatic(r *gin.Engine, dir string) {
	if dir == "" {
		return
	}
	indexPath := filepath.Join(dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		// 前端还没构建。不报错——后端可以单独跑起来调接口。
		return
	}

	r.Static("/assets", filepath.Join(dir, "assets"))
	r.StaticFile("/favicon.ico", filepath.Join(dir, "favicon.ico"))
	r.StaticFile("/logo.svg", filepath.Join(dir, "logo.svg"))

	// SPA fallback：除 /api 与 /v1 之外的未知路径一律返回 index.html，
	// 交给前端路由处理。没有这条，刷新 /accounts 这类前端路由就是 404。
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
			return
		}
		if c.Request.Method != http.MethodGet {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.File(indexPath)
	})
}
