package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/snap-crew/tripaw-server/internal/auth"
	"github.com/snap-crew/tripaw-server/internal/httpx"
	"github.com/snap-crew/tripaw-server/internal/token"
)

type Registrar interface {
	RegisterPublic(*gin.RouterGroup)
	RegisterProtected(*gin.RouterGroup)
}

func New(tokens *token.Manager, handlers ...Registrar) *gin.Engine {
	r := gin.New()
	r.Use(devCors())
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

// devCors permits the Expo web client during local development. Production
// deployments should put the API and web app behind the same origin or replace
// this middleware with an allow-list for the deployed web origin.
func devCors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "http://localhost:8081" || origin == "http://127.0.0.1:8081" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
