// Package router 는 HTTP 라우트를 한곳에서 조립한다.
package router

import (
	"net/http"

	"github.com/daewon/tripaw-server/internal/auth"
	"github.com/daewon/tripaw-server/internal/httpx"
	"github.com/daewon/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
)

// New 는 라우트가 모두 등록된 gin 엔진을 만든다.
func New(tokens *token.Manager, authHandler *auth.Handler) *gin.Engine {
	r := gin.New()
	r.Use(httpx.Logger(), httpx.Recovery())

	// 로드밸런서·컨테이너 헬스체크용. 인증 없이 열어둔다.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api")

	// 로그인 전에 부르는 것들.
	authHandler.RegisterPublic(api)

	// 액세스 토큰이 있어야 하는 것들.
	protected := api.Group("")
	protected.Use(auth.RequireAuth(tokens))
	authHandler.RegisterProtected(protected)

	return r
}
