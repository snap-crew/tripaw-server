// Package httpx 는 모든 핸들러가 공유하는 HTTP 응답 형식을 정의한다.
package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Problem 은 RFC 7807 형식의 오류 응답이다.
// 오류마다 형태가 제각각이면 클라이언트가 분기를 여러 벌 만들어야 하므로 하나로 맞춘다.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	// Code 는 클라이언트가 분기하기 좋은 짧은 식별자다.
	// Detail 은 사람이 읽는 문장이라 문구가 바뀔 수 있으니 분기에 쓰지 말 것.
	Code string `json:"code,omitempty"`
}

// Error 는 오류 응답을 보내고 이후 핸들러 실행을 멈춘다.
func Error(c *gin.Context, status int, code, detail string) {
	c.AbortWithStatusJSON(status, Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Code:   code,
	})
}

// OK 는 성공 응답을 보낸다.
func OK(c *gin.Context, status int, body any) {
	c.JSON(status, body)
}
