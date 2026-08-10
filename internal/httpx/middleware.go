package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger 는 요청 한 건을 slog 로 남긴다.
// gin 기본 로거는 자체 포맷으로 stdout 에 쓰기 때문에, 나머지 로그와 형식을 맞춘다.
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		// 등록되지 않은 경로는 FullPath 가 비어 있다. 실제 경로를 남겨야 추적이 된다.
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

// Recovery 는 핸들러에서 panic 이 나도 서버가 죽지 않게 막고 500 을 준다.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		slog.Error("핸들러 panic", "path", c.Request.URL.Path, "panic", recovered)
		Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	})
}
