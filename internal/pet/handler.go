package pet

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/snap-crew/tripaw-server/internal/auth"
	"github.com/snap-crew/tripaw-server/internal/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterPublic(*gin.RouterGroup) {}

func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	rg.GET("/breeds", h.listBreeds)

	rg.PUT("/pets/draft", h.saveDraft)
	rg.GET("/pets/draft", h.getDraft)

	rg.POST("/pets", h.create)
	rg.GET("/pets", h.list)
	rg.GET("/pets/:id", h.get)
	rg.PATCH("/pets/:id", h.update)
	rg.DELETE("/pets/:id", h.remove)
}

func (h *Handler) listBreeds(c *gin.Context) {
	if _, ok := auth.RequireUserID(c); !ok {
		return
	}

	items, err := h.svc.ListBreeds(c.Request.Context(), c.Query("species"), c.Query("q"))
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newBreedListResponse(items))
}

func (h *Handler) saveDraft(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	var req DraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "step 과 payload 가 필요합니다")
		return
	}

	if err := h.svc.SaveDraft(c.Request.Context(), userID, req.Step, req.Payload); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) getDraft(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	step, payload, updatedAt, err := h.svc.FindDraft(c.Request.Context(), userID)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, DraftResponse{
		Step: step, Payload: payload, UpdatedAt: updatedAt,
	})
}

func (h *Handler) create(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "요청 본문을 읽을 수 없습니다")
		return
	}

	p, err := h.svc.Create(c.Request.Context(), userID, &req)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusCreated, newPetResponse(p))
}

func (h *Handler) list(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	items, err := h.svc.List(c.Request.Context(), userID)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newPetListResponse(items))
}

func (h *Handler) get(c *gin.Context) {
	userID, petID, ok := h.userAndPetID(c)
	if !ok {
		return
	}

	p, err := h.svc.Get(c.Request.Context(), userID, petID)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newPetResponse(p))
}

func (h *Handler) update(c *gin.Context) {
	userID, petID, ok := h.userAndPetID(c)
	if !ok {
		return
	}

	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "요청 본문을 읽을 수 없습니다")
		return
	}

	p, err := h.svc.Update(c.Request.Context(), userID, petID, &req)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newPetResponse(p))
}

func (h *Handler) remove(c *gin.Context) {
	userID, petID, ok := h.userAndPetID(c)
	if !ok {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), userID, petID); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) userAndPetID(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}

	petID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "not_found", "반려동물을 찾을 수 없습니다")
		return uuid.Nil, uuid.Nil, false
	}
	return userID, petID, true
}

func writeError(c *gin.Context, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		status := http.StatusUnprocessableEntity

		if ve.Code == "invalid_request" {
			status = http.StatusBadRequest
		}
		httpx.Error(c, status, ve.Code, ve.Detail)

	case errors.Is(err, ErrLimitExceeded):
		httpx.Error(c, http.StatusConflict, "pet_limit_exceeded",
			"반려동물은 최대 5마리까지 등록할 수 있습니다")

	case errors.Is(err, ErrForbidden):
		httpx.Error(c, http.StatusForbidden, "forbidden", "다른 사용자의 반려동물입니다")

	case errors.Is(err, ErrDraftNotFound):
		httpx.Error(c, http.StatusNotFound, "draft_not_found", "임시 저장된 프로필이 없습니다")

	case errors.Is(err, ErrNotFound):
		httpx.Error(c, http.StatusNotFound, "not_found", "반려동물을 찾을 수 없습니다")

	default:
		slog.Error("반려동물 처리 중 오류", "path", c.FullPath(), "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	}
}
