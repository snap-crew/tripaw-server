package place

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Map(
	ctx context.Context, userID uuid.UUID, b BBox, zoom int, f *Filter,
) ([]Marker, int, error) {
	return s.repo.Markers(ctx, userID, b, zoom, f)
}

func (s *Service) Search(ctx context.Context, userID uuid.UUID, q string, limit int) ([]Place, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	return s.repo.Search(ctx, userID, q, clampLimit(limit))
}

func (s *Service) List(
	ctx context.Context, userID uuid.UUID, f *Filter,
	sort string, lat, lng *float64, rawCursor string, limit int,
) ([]Place, string, int, error) {
	cur, err := decodeCursor(rawCursor)
	if err != nil {
		return nil, "", 0, err
	}

	if sort == "distance" && (lat == nil || lng == nil) {
		sort = ""
	}

	limit = clampLimit(limit)
	items, err := s.repo.List(ctx, userID, f, sort, lat, lng, cur, limit)
	if err != nil {
		return nil, "", 0, err
	}

	total, err := s.repo.Count(ctx, userID, f)
	if err != nil {
		return nil, "", 0, err
	}

	return items, nextCursor(items, limit), total, nil
}

func (s *Service) Recommended(ctx context.Context, userID uuid.UUID, limit int) ([]Place, error) {
	return s.repo.Recommended(ctx, userID, clampLimit(limit))
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID, placeID int64) (*Place, []Image, error) {
	p, err := s.repo.FindByID(ctx, userID, placeID)
	if err != nil {
		return nil, nil, err
	}

	var images []Image
	if p.ImageCount > 0 {
		if images, err = s.repo.Images(ctx, placeID); err != nil {
			return nil, nil, err
		}
	}
	return p, images, nil
}

func (s *Service) ListSaved(
	ctx context.Context, userID uuid.UUID, category, rawCursor string, limit int,
) ([]SavedPlace, string, error) {
	cur, err := decodeCursor(rawCursor)
	if err != nil {
		return nil, "", err
	}

	limit = clampLimit(limit)
	items, err := s.repo.ListSaved(ctx, userID, category, cur, limit)
	if err != nil {
		return nil, "", err
	}
	return items, nextSavedCursor(items, limit), nil
}

func (s *Service) SavedCategories(ctx context.Context, userID uuid.UUID) ([]CategoryCount, int, error) {
	return s.repo.SavedCategories(ctx, userID)
}

func (s *Service) Save(ctx context.Context, userID uuid.UUID, placeID int64) (bool, error) {
	return s.repo.Save(ctx, userID, placeID)
}

func (s *Service) Unsave(ctx context.Context, userID uuid.UUID, placeID int64) error {
	return s.repo.Unsave(ctx, userID, placeID)
}

func clampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}
