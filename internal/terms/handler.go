package terms

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/snap-crew/tripaw-server/internal/auth"
	"github.com/snap-crew/tripaw-server/internal/httpx"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	rg.GET("/terms", h.list)
}

func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	rg.POST("/terms/agreements", h.agree)
}

func (h *Handler) list(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context())
	if err != nil {
		slog.Error("약관 목록 조회 실패", "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
		return
	}

	httpx.OK(c, http.StatusOK, newListResponse(items))
}

func (h *Handler) agree(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	var req AgreeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request",
			"agreements 목록이 필요합니다")
		return
	}

	items := make([]Agreement, 0, len(req.Agreements))
	for _, a := range req.Agreements {
		if a.TermID == nil || a.Agreed == nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request",
				"각 항목에 termId 와 agreed 가 필요합니다")
			return
		}
		items = append(items, Agreement{TermID: *a.TermID, Agreed: *a.Agreed})
	}

	err := h.svc.Agree(c.Request.Context(), userID, items)
	switch {
	case errors.Is(err, ErrRequiredNotAgreed):
		httpx.Error(c, http.StatusUnprocessableEntity, "required_terms_not_agreed",
			"필수 약관에 모두 동의해야 합니다")
	case err != nil:
		slog.Error("약관 동의 저장 실패", "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	default:
		c.Status(http.StatusNoContent)
	}
}
