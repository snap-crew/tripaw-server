package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/daewon/tripaw-server/internal/httpx"
	"github.com/daewon/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const contextUserIDKey = "userID"

// RequireAuth 는 Authorization 헤더의 액세스 토큰을 검사하는 미들웨어다.
//
// ValidateAccess 를 쓰는 게 중요하다. 종류를 가리지 않고 검증하면 수명이 긴
// 리프레시 토큰을 여기에 넣어도 통과해서, 액세스 토큰을 짧게 잡은 의미가 없어진다.
func RequireAuth(tokens *token.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := bearerToken(c.GetHeader("Authorization"))
		if err != nil {
			httpx.Error(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		userID, err := tokens.ValidateAccess(raw)
		if err != nil {
			// 어느 쪽이든 클라이언트가 할 일은 재발급이라 응답은 같게 준다.
			// 다만 종류가 틀린 건 클라이언트 구현 실수이므로 구분해서 알려준다.
			detail := "액세스 토큰이 유효하지 않거나 만료되었습니다"
			if errors.Is(err, token.ErrWrongTokenType) {
				detail = "액세스 토큰이 아닙니다. 리프레시 토큰을 보낸 것은 아닌지 확인하세요"
			}
			httpx.Error(c, http.StatusUnauthorized, "invalid_access_token", detail)
			return
		}

		c.Set(contextUserIDKey, userID)
		c.Next()
	}
}

// UserID 는 RequireAuth 가 넣어둔 사용자 ID 를 꺼낸다.
// RequireAuth 를 거친 라우트에서만 값이 있다.
func UserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(contextUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func bearerToken(header string) (string, error) {
	if header == "" {
		return "", errors.New("Authorization 헤더가 필요합니다")
	}

	scheme, rest, found := strings.Cut(header, " ")
	// 스킴 대소문자는 RFC 7235 상 구분하지 않는다.
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(rest) == "" {
		return "", errors.New(`Authorization 헤더 형식이 잘못되었습니다 ("Bearer <토큰>")`)
	}
	return strings.TrimSpace(rest), nil
}
