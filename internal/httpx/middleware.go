package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration", time.Since(start).Round(time.Millisecond).String(),
		}

		switch {
		case c.Writer.Status() >= http.StatusInternalServerError:
			slog.Error("요청 처리 실패", attrs...)
		case c.Writer.Status() >= http.StatusBadRequest:
			slog.Warn("요청 거부", attrs...)
		default:
			slog.Info("요청", attrs...)
		}
	}
}

func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		slog.Error("핸들러 panic", "path", c.Request.URL.Path, "panic", recovered)
		Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	})
}
