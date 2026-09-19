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

// corsOrigins lists the web clients allowed to call the API from a browser:
// the local Expo web client and the deployed Vercel app.
var corsOrigins = map[string]bool{
	"http://localhost:8081":         true,
	"http://127.0.0.1:8081":         true,
	"https://trippaw-web.vercel.app": true,
}

// devCors permits the web clients in corsOrigins. ngrok-skip-browser-warning
// is allowed so the Vercel app can reach the API through an ngrok free tunnel
// without hitting its browser interstitial.
func devCors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if corsOrigins[origin] {
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, ngrok-skip-browser-warning")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
