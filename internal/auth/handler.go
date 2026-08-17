package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/daewon/tripaw-server/internal/httpx"
	"github.com/daewon/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	rg.POST("/auth/apple", h.loginApple)
	rg.POST("/auth/kakao", h.loginKakao)
	rg.POST("/auth/refresh", h.refresh)
}

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

	user, pair, err := h.svc.LoginApple(c.Request.Context(), req.Code, req.Nickname)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	h.writeLoginResponse(c, user, pair)
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

	h.writeLoginResponse(c, user, pair)
}

func (h *Handler) writeLoginResponse(c *gin.Context, user *User, pair *token.Pair) {
	nextStep, err := h.svc.NextStep(c.Request.Context(), user.ID)
	if err != nil {
		slog.Error("다음 단계 판정 실패", "userID", user.ID, "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
		return
	}

	httpx.OK(c, http.StatusOK, TokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		User:         NewUserResponse(user),
		NextStep:     nextStep,
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

	httpx.OK(c, http.StatusOK, TokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

func (h *Handler) me(c *gin.Context) {
	userID, ok := RequireUserID(c)
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
	userID, ok := RequireUserID(c)
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
	userID, ok := RequireUserID(c)
	if !ok {
		return
	}

	var req DeleteAccountRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.DeleteAccount(c.Request.Context(), userID, req.Code); err != nil {
		writeAuthError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrProviderRejected):

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
