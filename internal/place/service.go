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

// petIDs 가 비어 있으면 비개인화다. 홈탭(최대 10개 가로 스크롤)이 그 경우다.
// 값이 있으면 그 반려동물들이 못 들어가는 곳을 뒤로 민다.
func (s *Service) Recommended(
	ctx context.Context, userID uuid.UUID, petIDs []uuid.UUID, limit int,
) ([]Place, error) {
	var size string
	maxKg := 0.0

	if petIDs != nil {
		sizes, err := s.repo.PetSizes(ctx, userID, petIDs)
		if err != nil {
			return nil, err
		}
		if len(petIDs) > 0 && len(sizes) != len(petIDs) {
			return nil, ErrUnknownPet
		}
		size = LargestSize(sizes)
		maxKg = petBandMaxKg[size]
	}

	return s.repo.Recommended(ctx, userID, size, maxKg, clampLimit(limit))
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID, placeID int64) (*Place, []string, error) {
	p, err := s.repo.FindByID(ctx, userID, placeID)
	if err != nil {
		return nil, nil, err
	}

	var images []string
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
