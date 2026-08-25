package image

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, img *Image) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO images (user_id, content_type, data, byte_size)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		img.UserID, img.ContentType, img.Data, img.ByteSize,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("이미지 저장: %w", err)
	}
	return id, nil
}

func (r *Repository) Find(ctx context.Context, id uuid.UUID) (*Image, error) {
	var img Image
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, content_type, data, byte_size, created_at
		FROM images WHERE id = $1`, id,
	).Scan(&img.ID, &img.UserID, &img.ContentType, &img.Data, &img.ByteSize, &img.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("이미지 조회: %w", err)
	}
	return &img, nil
}

func (r *Repository) Exists(ctx context.Context, id uuid.UUID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM images WHERE id = $1 AND user_id = $2)`,
		id, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("이미지 확인: %w", err)
	}
	return ok, nil
}
