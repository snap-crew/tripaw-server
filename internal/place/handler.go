package place

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
	rg.GET("/places/map", h.mapView)
	rg.GET("/places/search", h.search)
	rg.GET("/places/recommended", h.recommended)
	rg.GET("/places", h.list)
	rg.GET("/places/:id", h.get)

	rg.GET("/saved-places", h.listSaved)
	rg.GET("/saved-places/categories", h.savedCategories)
	rg.POST("/saved-places", h.save)
	rg.DELETE("/saved-places/:placeId", h.unsave)
}

func (h *Handler) mapView(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	b, err := parseBBox(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	zoom, err := strconv.Atoi(c.Query("zoom"))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "zoom 이 필요합니다")
		return
	}

	filter, err := parseFilter(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	markers, total, err := h.svc.Map(c.Request.Context(), userID, b, zoom, filter)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, newMapResponse(markers, total))
}

func (h *Handler) search(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	items, err := h.svc.Search(c.Request.Context(), userID, c.Query("q"), queryInt(c, "limit"))
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, SearchResponse{Items: newPlaceList(items)})
}

func (h *Handler) list(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	filter, err := parseFilter(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	lat, lng := queryFloatPtr(c, "lat"), queryFloatPtr(c, "lng")
	sort := c.DefaultQuery("sort", "distance")

	items, next, total, err := h.svc.List(
		c.Request.Context(), userID, filter, sort, lat, lng,
		c.Query("cursor"), queryInt(c, "limit"))
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, ListResponse{
		Items: newPlaceList(items), Total: total, NextCursor: cursorPtr(next),
	})
}

func (h *Handler) recommended(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	limit := queryInt(c, "limit")
	if limit == 0 {
		limit = 10
	}

	petIDs, err := queryPetIDs(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	items, err := h.svc.Recommended(c.Request.Context(), userID, petIDs, limit)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, SearchResponse{Items: newPlaceList(items)})
}

func (h *Handler) get(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}
	placeID, ok := pathPlaceID(c, "id")
	if !ok {
		return
	}

	p, images, err := h.svc.Get(c.Request.Context(), userID, placeID)
	if err != nil {
		writeError(c, err)
		return
	}

	httpx.OK(c, http.StatusOK, PlaceDetailResponse{
		PlaceResponse: newPlaceResponse(p),

		Images: append([]string{}, images...),
	})
}

func (h *Handler) listSaved(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	category := c.Query("category")
	if category != "" && !validCategory[category] {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "알 수 없는 카테고리입니다")
		return
	}

	items, next, err := h.svc.ListSaved(
		c.Request.Context(), userID, category, c.Query("cursor"), queryInt(c, "limit"))
	if err != nil {
		writeError(c, err)
		return
	}

	out := make([]SavedPlaceResponse, 0, len(items))
	for i := range items {
		out = append(out, SavedPlaceResponse{
			PlaceResponse: newPlaceResponse(&items[i].Place),
			SavedAt:       items[i].SavedAt,
		})
	}

	httpx.OK(c, http.StatusOK, SavedListResponse{Items: out, NextCursor: cursorPtr(next)})
}

func (h *Handler) savedCategories(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	items, total, err := h.svc.SavedCategories(c.Request.Context(), userID)
	if err != nil {
		writeError(c, err)
		return
	}

	out := make([]CategoryCountResponse, 0, len(items))
	for _, ci := range items {
		out = append(out, CategoryCountResponse{Category: ci.Category, Count: ci.Count})
	}

	httpx.OK(c, http.StatusOK, CategoriesResponse{Total: total, Items: out})
}

func (h *Handler) save(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}

	var req SaveRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlaceID <= 0 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "placeId 가 필요합니다")
		return
	}

	created, err := h.svc.Save(c.Request.Context(), userID, req.PlaceID)
	if err != nil {
		writeError(c, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.Status(status)
}

func (h *Handler) unsave(c *gin.Context) {
	userID, ok := auth.RequireUserID(c)
	if !ok {
		return
	}
	placeID, ok := pathPlaceID(c, "placeId")
	if !ok {
		return
	}

	if err := h.svc.Unsave(c.Request.Context(), userID, placeID); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func parseBBox(c *gin.Context) (BBox, error) {
	var b BBox
	for _, f := range []struct {
		key string
		dst *float64
	}{
		{"minLat", &b.MinLat}, {"maxLat", &b.MaxLat},
		{"minLng", &b.MinLng}, {"maxLng", &b.MaxLng},
	} {
		v, err := strconv.ParseFloat(c.Query(f.key), 64)
		if err != nil {
			return b, errors.New(f.key + " 이 필요합니다")
		}
		*f.dst = v
	}

	if b.MinLat > b.MaxLat || b.MinLng > b.MaxLng {
		return b, errors.New("영역의 최소/최대 좌표가 뒤집혔습니다")
	}
	return b, nil
}

func parseFilter(c *gin.Context) (*Filter, error) {
	f := &Filter{Category: c.Query("category")}
	if f.Category != "" && !validCategory[f.Category] {
		return nil, errors.New("알 수 없는 카테고리입니다")
	}

	f.MaxWeightKg = queryFloatPtr(c, "maxWeightKg")

	if area := c.Query("area"); area != "" {
		if area != "indoor" && area != "outdoor" {
			return nil, errors.New("area 는 indoor 또는 outdoor 여야 합니다")
		}
		f.Area = area
	}

	if v := c.Query("maxFeeKrw"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, errors.New("maxFeeKrw 가 숫자가 아닙니다")
		}
		f.MaxFeeKrw = &n
	}

	f.Leash = queryBoolPtr(c, "leash")
	f.Muzzle = queryBoolPtr(c, "muzzle")

	for _, name := range c.QueryArray("facilities") {
		for _, one := range strings.Split(name, ",") {
			switch one {
			case "parking":
				f.Parking = true
			case "wasteBag":
				f.WasteBag = true
			case "":
			default:
				return nil, errors.New("지원하지 않는 편의시설입니다: " + one)
			}
		}
	}

	return f, nil
}

// ?petIds=<uuid>,<uuid>  값이 없으면 비개인화(홈탭).
// ?petIds=me  는 등록한 반려동물 전체를 뜻한다.
func queryPetIDs(c *gin.Context) ([]uuid.UUID, error) {
	raw := strings.TrimSpace(c.Query("petIds"))
	if raw == "" {
		return nil, nil
	}
	if raw == "me" {
		return []uuid.UUID{}, nil
	}

	out := []uuid.UUID{}
	for _, one := range strings.Split(raw, ",") {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		id, err := uuid.Parse(one)
		if err != nil {
			return nil, errors.New("petIds 형식이 잘못되었습니다")
		}
		out = append(out, id)
	}
	return out, nil
}

func pathPlaceID(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(c, http.StatusNotFound, "not_found", "장소를 찾을 수 없습니다")
		return 0, false
	}
	return id, true
}

func queryInt(c *gin.Context, key string) int {
	n, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return 0
	}
	return n
}

func queryFloatPtr(c *gin.Context, key string) *float64 {
	v := c.Query(key)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &n
}

func queryBoolPtr(c *gin.Context, key string) *bool {
	v := c.Query(key)
	if v != "true" && v != "false" {
		return nil
	}
	b := v == "true"
	return &b
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrUnknownPet):
		httpx.Error(c, http.StatusUnprocessableEntity, "invalid_pets",
			"등록되지 않은 반려동물이 포함되어 있습니다")
	case errors.Is(err, ErrInvalidCursor):
		httpx.Error(c, http.StatusBadRequest, "invalid_cursor", "커서가 올바르지 않습니다")
	case errors.Is(err, ErrNotFound):
		httpx.Error(c, http.StatusNotFound, "not_found", "장소를 찾을 수 없습니다")
	default:
		slog.Error("장소 처리 중 오류", "path", c.FullPath(), "error", err)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "서버 오류")
	}
}
