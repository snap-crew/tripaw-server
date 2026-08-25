package image

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

func (s *Service) Upload(ctx context.Context, userID uuid.UUID, data []byte) (uuid.UUID, error) {
	if len(data) == 0 {
		return uuid.Nil, ErrEmpty
	}
	if len(data) > MaxBytes {
		return uuid.Nil, ErrTooLarge
	}

	contentType, err := detectContentType(data)
	if err != nil {
		return uuid.Nil, err
	}

	return s.repo.Create(ctx, &Image{
		UserID:      userID,
		ContentType: contentType,
		Data:        data,
		ByteSize:    len(data),
	})
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Image, error) {
	return s.repo.Find(ctx, id)
}

func (s *Service) OwnedBy(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	return s.repo.Exists(ctx, id, userID)
}
