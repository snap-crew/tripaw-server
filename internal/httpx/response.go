package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`

	Code string `json:"code,omitempty"`
}

func Error(c *gin.Context, status int, code, detail string) {
	c.AbortWithStatusJSON(status, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Code:   code,
	})
}

func OK(c *gin.Context, status int, body any) {
	c.JSON(status, body)
}
