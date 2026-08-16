package trip

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/daewon/tripaw-server/internal/auth"
	"github.com/daewon/tripaw-server/internal/httpx"
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
	rg.GET("/trips", h.list)
	rg.POST("/trips", h.create)
	rg.GET("/trips/:id", h.get)
	rg.PATCH("/trips/:id", h.update)
	rg.DELETE("/trips/:id", h.remove)
	rg.POST("/trips/:id/duplicate", h.duplicate)

	rg.POST("/trips/:id/days/:dayNo/stops", h.addStops)
	rg.DELETE("/trips/:id/days/:dayNo/stops/:seq", h.deleteStop)
	rg.PATCH("/trips/:id/days/:dayNo/reorder", h.reorder)
}

func (h *Handler) list(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	offset := queryInt(c, "offset")
	limit := clampLimit(queryInt(c, "limit"))

	res, err := h.svc.List(c.Request.Context(), userID, c.Query("status"), offset, limit)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newListResponse(res, h.svc.today(), offset, limit))
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

	t, err := h.svc.Create(c.Request.Context(), userID, &req)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusCreated, newTripResponse(t, h.svc.today()))
}

func (h *Handler) get(c *gin.Context) {
	userID, tripID, ok := h.userAndTripID(c)
	if !ok {
		return
	}

	t, err := h.svc.Get(c.Request.Context(), userID, tripID)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newTripResponse(t, h.svc.today()))
}

func (h *Handler) update(c *gin.Context) {
	userID, tripID, ok := h.userAndTripID(c)
	if !ok {
		return
	}

	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "요청 본문을 읽을 수 없습니다")
		return
	}

	t, err := h.svc.Update(c.Request.Context(), userID, tripID, &req)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newTripResponse(t, h.svc.today()))
}

func (h *Handler) remove(c *gin.Context) {
	userID, tripID, ok := h.userAndTripID(c)
	if !ok {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), userID, tripID); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) duplicate(c *gin.Context) {
	userID, tripID, ok := h.userAndTripID(c)
	if !ok {
		return
	}

	t, err := h.svc.Duplicate(c.Request.Context(), userID, tripID)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusCreated, newTripResponse(t, h.svc.today()))
}

func (h *Handler) addStops(c *gin.Context) {
	userID, tripID, ok := h.userAndTripID(c)
	if !ok {
		return
	}
	dayNo, ok := pathDayNo(c)
	if !ok {
		return
	}

	var req AddStopsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "placeIds 가 필요합니다")
		return
	}

	t, err := h.svc.AddStops(c.Request.Context(), userID, tripID, dayNo, req.PlaceIDs)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newTripResponse(t, h.svc.today()))
}

func (h *Handler) deleteStop(c *gin.Context) {
	userID, tripID, ok := h.userAndTripID(c)
	if !ok {
		return
	}
	dayNo, ok := pathDayNo(c)
	if !ok {
		return
	}
	seq, err := strconv.Atoi(c.Param("seq"))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "not_found", "일정을 찾을 수 없습니다")
		return
	}

	t, err := h.svc.DeleteStop(c.Request.Context(), userID, tripID, dayNo, seq)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newTripResponse(t, h.svc.today()))
}

func (h *Handler) reorder(c *gin.Context) {
	userID, tripID, ok := h.userAndTripID(c)
	if !ok {
		return
	}
	dayNo, ok := pathDayNo(c)
	if !ok {
		return
	}

	var req ReorderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "placeIds 가 필요합니다")
		return
	}

	t, err := h.svc.Reorder(c.Request.Context(), userID, tripID, dayNo, req.PlaceIDs)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newTripResponse(t, h.svc.today()))
}

func (h *Handler) userAndTripID(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}

	tripID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "not_found", "여행을 찾을 수 없습니다")
		return uuid.Nil, uuid.Nil, false
	}
	return userID, tripID, true
}

func pathDayNo(c *gin.Context) (int, bool) {
	n, err := strconv.Atoi(c.Param("dayNo"))
	if err != nil || n < 1 {
		httpx.Error(c, http.StatusNotFound, "not_found", "해당 일차가 없습니다")
		return 0, false
	}
	return n, true
}

func queryInt(c *gin.Context, key string) int {
	n, err := strconv.Atoi(c.Query(key))
	if err != nil || n < 0 {
		return 0
	}
	return n
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

	case errors.Is(err, ErrStopsOutsideRange):
		httpx.Error(c, http.StatusConflict, "stops_outside_range",
			"줄이려는 기간에 일정이 남아 있습니다. 해당 일정을 먼저 삭제해 주세요")

	case errors.Is(err, ErrDayOutOfRange):
		httpx.Error(c, http.StatusNotFound, "day_out_of_range", "여행 기간에 없는 일차입니다")

	case errors.Is(err, ErrForbidden):
		httpx.Error(c, http.StatusForbidden, "forbidden", "다른 사용자의 여행입니다")

	case errors.Is(err, ErrNotFound):
		httpx.Error(c, http.StatusNotFound, "not_found", "여행을 찾을 수 없습니다")

	default:
		slog.Error("여행 처리 중 오류", "path", c.FullPath(), "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	}
}
