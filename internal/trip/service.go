package trip

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

type Service struct {
	repo *Repository

	now func() time.Time
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) today() time.Time {
	n := s.now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

type ListResult struct {
	Featured *Trip
	Items    []Trip
	HasMore  bool
}

func (s *Service) List(
	ctx context.Context, userID uuid.UUID, status string, offset, limit int,
) (*ListResult, error) {
	if status != "past" {
		status = "upcoming"
	}
	limit = clampLimit(limit)

	var featured *Trip
	var excludeID *uuid.UUID
	if status == "upcoming" && offset == 0 {
		f, err := s.repo.Featured(ctx, userID, s.today())
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if f != nil {
			featured, excludeID = f, &f.ID
		}
	} else if status == "upcoming" {
		f, err := s.repo.Featured(ctx, userID, s.today())
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if f != nil {
			excludeID = &f.ID
		}
	}

	items, err := s.repo.List(ctx, userID, status, s.today(), excludeID, offset, limit+1)
	if err != nil {
		return nil, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	refs := make([]*Trip, 0, len(items)+1)
	if featured != nil {
		refs = append(refs, featured)
	}
	for i := range items {
		refs = append(refs, &items[i])
	}
	if err := s.repo.LoadPets(ctx, refs); err != nil {
		return nil, err
	}

	return &ListResult{Featured: featured, Items: items, HasMore: hasMore}, nil
}

func (s *Service) Get(ctx context.Context, userID, tripID uuid.UUID) (*Trip, error) {
	t, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if t.UserID != userID {
		return nil, ErrForbidden
	}

	if err := s.repo.LoadPets(ctx, []*Trip{t}); err != nil {
		return nil, err
	}
	if err := s.repo.LoadDays(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) Create(ctx context.Context, userID uuid.UUID, req *CreateRequest) (*Trip, error) {
	title, err := validateTitle(req.Title)
	if err != nil {
		return nil, err
	}

	start, end, err := parseRange(req.StartDate, req.EndDate)
	if err != nil {
		return nil, err
	}

	petIDs, err := s.validatePets(ctx, userID, req.PetIDs)
	if err != nil {
		return nil, err
	}

	themes := req.Themes
	if themes == nil {
		themes = []string{}
	}

	id, err := s.repo.Create(ctx, userID, title, start, end, themes, petIDs)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, id)
}

func (s *Service) Update(
	ctx context.Context, userID, tripID uuid.UUID, req *UpdateRequest,
) (*Trip, error) {
	current, err := s.Get(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}

	var title *string
	if req.Title != nil {
		v, err := validateTitle(*req.Title)
		if err != nil {
			return nil, err
		}
		title = &v
	}

	start, end := current.StartDate, current.EndDate
	if req.StartDate != nil {
		v, err := parseDate(*req.StartDate, "invalid_date_range")
		if err != nil {
			return nil, err
		}
		start = &v
	}
	if req.EndDate != nil {
		v, err := parseDate(*req.EndDate, "invalid_date_range")
		if err != nil {
			return nil, err
		}
		end = &v
	}
	if start != nil && end != nil && end.Before(*start) {
		return nil, invalid("invalid_date_range", "종료일은 시작일 이후여야 합니다")
	}

	if start != nil && end != nil {
		newDays := int(end.Sub(*start).Hours()/24) + 1
		n, err := s.repo.StopsBeyond(ctx, tripID, newDays)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrStopsOutsideRange
		}
	}

	var startArg, endArg *time.Time
	if req.StartDate != nil {
		startArg = start
	}
	if req.EndDate != nil {
		endArg = end
	}

	if err := s.repo.Update(ctx, tripID, title, startArg, endArg, req.Themes); err != nil {
		return nil, err
	}

	if req.PetIDs != nil {
		petIDs, err := s.validatePets(ctx, userID, *req.PetIDs)
		if err != nil {
			return nil, err
		}
		if err := s.repo.ReplacePets(ctx, tripID, petIDs); err != nil {
			return nil, err
		}
	}

	return s.Get(ctx, userID, tripID)
}

func (s *Service) Duplicate(ctx context.Context, userID, tripID uuid.UUID) (*Trip, error) {
	src, err := s.Get(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}

	base := ""
	if src.Title != nil {
		base = *src.Title
	}

	newTitle := truncateTitle(base + DuplicateSuffix)

	id, err := s.repo.Duplicate(ctx, src, newTitle)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, id)
}

func (s *Service) Delete(ctx context.Context, userID, tripID uuid.UUID) error {
	if _, err := s.Get(ctx, userID, tripID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, tripID)
}

func (s *Service) AddStops(
	ctx context.Context, userID, tripID uuid.UUID, dayNo int, placeIDs []int64,
) (*Trip, error) {
	t, err := s.Get(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	if err := checkDay(t, dayNo); err != nil {
		return nil, err
	}
	if len(placeIDs) == 0 {
		return nil, invalid("invalid_request", "담을 장소가 없습니다")
	}

	ok, err := s.repo.PlacesExist(ctx, placeIDs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, invalid("invalid_place", "존재하지 않는 장소가 포함되어 있습니다")
	}

	if err := s.repo.AddStops(ctx, tripID, dayNo, placeIDs); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, tripID)
}

func (s *Service) DeleteStop(
	ctx context.Context, userID, tripID uuid.UUID, dayNo, seq int,
) (*Trip, error) {
	if _, err := s.Get(ctx, userID, tripID); err != nil {
		return nil, err
	}
	if err := s.repo.DeleteStop(ctx, tripID, dayNo, seq); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, tripID)
}

func (s *Service) Reorder(
	ctx context.Context, userID, tripID uuid.UUID, dayNo int, placeIDs []int64,
) (*Trip, error) {
	t, err := s.Get(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	if err := checkDay(t, dayNo); err != nil {
		return nil, err
	}
	if err := s.repo.ReorderDay(ctx, tripID, dayNo, placeIDs); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, tripID)
}

func (s *Service) validatePets(
	ctx context.Context, userID uuid.UUID, raw []string,
) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(raw))
	seen := make(map[uuid.UUID]bool, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, invalid("invalid_pets", "반려동물 ID 형식이 잘못되었습니다")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}

	owned, err := s.repo.OwnedPetIDs(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	if len(owned) != len(ids) {
		return nil, invalid("invalid_pets", "등록되지 않은 반려동물이 포함되어 있습니다")
	}

	return ids, nil
}

func validateTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", invalid("invalid_trip_title", "여행 제목을 입력해 주세요")
	}
	if len([]rune(title)) > MaxTitleLen {
		return "", invalid("invalid_trip_title", "여행 제목은 20자 이하여야 합니다")
	}
	return title, nil
}

func parseRange(startRaw, endRaw string) (time.Time, time.Time, error) {
	start, err := parseDate(startRaw, "invalid_date_range")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parseDate(endRaw, "invalid_date_range")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if end.Before(start) {
		return time.Time{}, time.Time{},
			invalid("invalid_date_range", "종료일은 시작일 이후여야 합니다")
	}
	return start, end, nil
}

func parseDate(raw, code string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, invalid(code, "날짜는 YYYY-MM-DD 형식이어야 합니다")
	}
	return t, nil
}

func checkDay(t *Trip, dayNo int) error {
	if dayNo < 1 || dayNo > len(t.Days) {
		return ErrDayOutOfRange
	}
	return nil
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
