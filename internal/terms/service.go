package terms

import (
	"context"

	"github.com/google/uuid"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]Term, error) {
	return s.repo.ListLatest(ctx)
}

func (s *Service) Agree(ctx context.Context, userID uuid.UUID, items []Agreement) error {
	required, err := s.repo.RequiredTermIDs(ctx)
	if err != nil {
		return err
	}

	submitted := make(map[int]bool, len(items))
	for _, a := range items {
		submitted[a.TermID] = a.Agreed
	}

	for _, id := range required {
		if !submitted[id] {
			return ErrRequiredNotAgreed
		}
	}

	return s.repo.SaveAgreements(ctx, userID, items)
}
