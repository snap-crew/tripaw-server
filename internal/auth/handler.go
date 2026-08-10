package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/daewon/tripaw-server/internal/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler 는 인증 관련 HTTP 엔드포인트다.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterPublic 은 로그인 없이 부를 수 있는 라우트를 등록한다.
func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	rg.POST("/auth/apple", h.loginApple)
	rg.POST("/auth/kakao", h.loginKakao)
	rg.POST("/auth/refresh", h.refresh)
}

// RegisterProtected 는 로그인이 필요한 라우트를 등록한다.
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	rg.GET("/auth/me", h.me)
	rg.POST("/auth/logout", h.logout)
	rg.DELETE("/auth/account", h.deleteAccount)
}

func (h *Handler) loginApple(c *gin.Context) {
	var req AppleLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "code 가 필요합니다")
		return
	}

	user, pair, err := h.svc.LoginApple(c.Request.Context(), req.Code)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, TokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		User:         NewUserResponse(user),
	})
}

func (h *Handler) loginKakao(c *gin.Context) {
	var req KakaoLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "accessToken 이 필요합니다")
		return
	}

	user, pair, err := h.svc.LoginKakao(c.Request.Context(), req.AccessToken)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, TokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		User:         NewUserResponse(user),
	})
}

func (h *Handler) refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "refreshToken 이 필요합니다")
		return
	}

	pair, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	// 재발급에는 사용자 정보를 싣지 않는다. 필요하면 /auth/me 를 부르면 된다.
	httpx.OK(c, http.StatusOK, TokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

func (h *Handler) me(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	user, err := h.svc.Me(c.Request.Context(), userID)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, NewUserResponse(user))
}

func (h *Handler) logout(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	if err := h.svc.Logout(c.Request.Context(), userID); err != nil {
		writeAuthError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) deleteAccount(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	// 카카오 사용자는 본문이 없어도 된다. 애플만 code 가 필요하고,
	// 없으면 서비스가 ErrAppleCodeRequired 로 알려준다.
	var req DeleteAccountRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.DeleteAccount(c.Request.Context(), userID, req.Code); err != nil {
		writeAuthError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// requireUserID 는 미들웨어가 넣어둔 사용자 ID 를 꺼낸다.
// 없으면 라우트 등록이 잘못된 것이므로 500 이 맞다.
func requireUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, ok := UserID(c)
	if !ok {
		slog.Error("인증이 필요한 라우트에 RequireAuth 미들웨어가 없습니다",
			"path", c.FullPath())
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
		return uuid.Nil, false
	}
	return userID, true
}

func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrProviderRejected):
		// 공급자가 거부한 이유(코드 만료/재사용 등)는 서버 로그에만 남긴다.
		slog.Warn("소셜 로그인 확인 실패", "path", c.FullPath(), "error", err)
		httpx.Error(c, http.StatusUnauthorized, "provider_rejected",
			"소셜 로그인 확인에 실패했습니다. 다시 시도해 주세요")

	case errors.Is(err, ErrInvalidRefreshToken):
		httpx.Error(c, http.StatusUnauthorized, "invalid_refresh_token",
			"리프레시 토큰이 유효하지 않습니다. 다시 로그인해 주세요")

	case errors.Is(err, ErrAppleCodeRequired):
		httpx.Error(c, http.StatusBadRequest, "apple_code_required",
			"애플 계정 탈퇴에는 authorization code 가 필요합니다")

	case errors.Is(err, ErrUserNotFound):
		httpx.Error(c, http.StatusNotFound, "user_not_found", "사용자를 찾을 수 없습니다")

	default:
		slog.Error("인증 처리 중 오류", "path", c.FullPath(), "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	}
}
