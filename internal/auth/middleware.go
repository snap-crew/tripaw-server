package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/daewon/tripaw-server/internal/httpx"
	"github.com/daewon/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const contextUserIDKey = "userID"

func RequireAuth(tokens *token.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := bearerToken(c.GetHeader("Authorization"))
		if err != nil {
			httpx.Error(c, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}

		userID, err := tokens.ValidateAccess(raw)
		if err != nil {
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

func UserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(contextUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func RequireUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, ok := UserID(c)
	if !ok {
		slog.Error("인증이 필요한 라우트에 RequireAuth 미들웨어가 없습니다",
			"path", c.FullPath())
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
		return uuid.Nil, false
	}
	return userID, true
}

func bearerToken(header string) (string, error) {
	if header == "" {
		return "", errors.New("Authorization 헤더가 필요합니다")
	}

	scheme, rest, found := strings.Cut(header, " ")

	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(rest) == "" {
		return "", errors.New(`Authorization 헤더 형식이 잘못되었습니다 ("Bearer <토큰>")`)
	}
	return strings.TrimSpace(rest), nil
}
