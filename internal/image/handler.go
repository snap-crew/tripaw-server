package image

import (
	"errors"
	"io"
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
	rg.POST("/images", h.upload)
	rg.GET("/images/:id", h.serve)
}

func (h *Handler) upload(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBytes+1)
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "image_too_large",
			"이미지는 10MB 이하여야 합니다")
		return
	}

	id, err := h.svc.Upload(c.Request.Context(), userID, data)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusCreated, UploadResponse{ID: id.String(), URL: URLFor(id)})
}

func (h *Handler) serve(c *gin.Context) {
	if _, ok := auth.RequireUserID(c); !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "not_found", "이미지를 찾을 수 없습니다")
		return
	}

	img, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}

	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	c.Data(http.StatusOK, img.ContentType, img.Data)
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrEmpty):
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "이미지 본문이 비어 있습니다")
	case errors.Is(err, ErrTooLarge):
		httpx.Error(c, http.StatusRequestEntityTooLarge, "image_too_large",
			"이미지는 10MB 이하여야 합니다")
	case errors.Is(err, ErrUnsupported):
		httpx.Error(c, http.StatusUnsupportedMediaType, "unsupported_image_type",
			"jpeg, png, webp 만 올릴 수 있습니다")
	case errors.Is(err, ErrNotFound):
		httpx.Error(c, http.StatusNotFound, "not_found", "이미지를 찾을 수 없습니다")
	default:
		slog.Error("이미지 처리 중 오류", "path", c.FullPath(), "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	}
}
