package server

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// accessLog 返回一个记录访问日志的中间件。
//
// 刻意不记录请求体与响应体：中转的请求里是用户自己的代码和对话，
// 日志里留一份既占空间又是隐私风险。排障需要时看 usage_logs 就够了。
func accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		path := c.Request.URL.Path

		c.Next()

		elapsed := time.Since(started)
		status := c.Writer.Status()

		// 静态资源不记，否则前端每刷新一次就刷一屏日志。
		if !isAPIPath(path) {
			return
		}

		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"duration_ms", elapsed.Milliseconds(),
			"ip", c.ClientIP(),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "errors", c.Errors.String())
		}

		switch {
		case status >= 500:
			slog.Error("请求处理失败", attrs...)
		case status >= 400:
			slog.Warn("请求被拒绝", attrs...)
		case elapsed > 10*time.Second:
			// 慢请求单独标出来：中转场景下慢通常意味着上游在拖，
			// 值得在日志里一眼看见。
			slog.Warn("请求耗时较长", attrs...)
		default:
			slog.Info("请求完成", attrs...)
		}
	}
}

func isAPIPath(path string) bool {
	return len(path) >= 4 && (path[:4] == "/api" || path[:3] == "/v1")
}
