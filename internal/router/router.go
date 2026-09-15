package router

import (
	"net/http"

	"github.com/snap-crew/tripaw-server/internal/auth"
	"github.com/snap-crew/tripaw-server/internal/httpx"
	"github.com/snap-crew/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
)

type Registrar interface {
	RegisterPublic(*gin.RouterGroup)
	RegisterProtected(*gin.RouterGroup)
}

func New(tokens *token.Manager, handlers ...Registrar) *gin.Engine {
	r := gin.New()
	r.Use(httpx.Logger(), httpx.Recovery())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api")

	protected := api.Group("")
	protected.Use(auth.RequireAuth(tokens))

	for _, h := range handlers {
		h.RegisterPublic(api)
		h.RegisterProtected(protected)
	}

	return r
}
