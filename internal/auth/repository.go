package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const userColumns = `id, provider, provider_sub, email, nickname, profile_image,
	created_at, updated_at, last_login_at`

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) UpsertOnLogin(ctx context.Context, u *User) (*User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (provider, provider_sub, email, nickname, profile_image, last_login_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (provider, provider_sub) DO UPDATE SET
			email         = COALESCE(EXCLUDED.email,         users.email),
			nickname      = COALESCE(EXCLUDED.nickname,      users.nickname),
			profile_image = COALESCE(EXCLUDED.profile_image, users.profile_image),
			last_login_at = now(),
			updated_at    = now()
		RETURNING `+userColumns,
		u.Provider, u.ProviderSub, u.Email, u.Nickname, u.ProfileImage,
	)

	out, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("사용자 upsert: %w", err)
	}
	return out, nil
}

func (r *Repository) FindUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)

	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("사용자 조회: %w", err)
	}
	return u, nil
}

func (r *Repository) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("사용자 삭제: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *Repository) StoreRefreshToken(
	ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time,
) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("리프레시 토큰 저장: %w", err)
	}
	return nil
}

func (r *Repository) ConsumeRefreshToken(ctx context.Context, tokenHash string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		DELETE FROM refresh_tokens
		WHERE token_hash = $1 AND expires_at > now()
		RETURNING user_id`,
		tokenHash,
	).Scan(&userID)

	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidRefreshToken
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("리프레시 토큰 사용: %w", err)
	}
	return userID, nil
}

func (r *Repository) DeleteRefreshTokensByUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("리프레시 토큰 일괄 삭제: %w", err)
	}
	return nil
}

func (r *Repository) DeleteExpiredRefreshTokens(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("만료 토큰 정리: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Provider, &u.ProviderSub, &u.Email, &u.Nickname, &u.ProfileImage,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
