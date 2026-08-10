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

// userColumns 는 User 를 채우는 데 필요한 컬럼 목록이다.
// SELECT 와 RETURNING 에서 같은 순서를 써야 scanUser 가 맞는다.
const userColumns = `id, provider, provider_sub, email, nickname, profile_image,
	created_at, updated_at, last_login_at`

// Repository 는 사용자와 리프레시 토큰을 저장한다.
type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// UpsertOnLogin 은 로그인한 사용자를 찾거나 없으면 만든다.
//
// 찾기와 만들기를 따로 하면, 같은 사람이 두 기기에서 동시에 첫 로그인을 할 때
// 양쪽 다 "없음" 을 보고 둘 다 INSERT 를 시도해 하나가 UNIQUE 위반으로 실패한다.
// ON CONFLICT 로 한 문장에 처리하면 그 경우가 없다.
//
// 이미 있는 사용자면 프로필을 갱신하는데, COALESCE 로 "새 값이 있을 때만" 덮는다.
// 애플은 두 번째 로그인부터 이메일을 주지 않으므로, 그냥 덮으면 처음에 받아둔
// 이메일이 NULL 로 지워진다.
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

// FindUserByID 는 사용자를 찾는다. 없으면 ErrUserNotFound.
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

// DeleteUser 는 사용자를 지운다.
// pet_profiles / saved_places / routes / refresh_tokens 는 ON DELETE CASCADE 로 함께 지워진다.
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

// StoreRefreshToken 은 발급한 리프레시 토큰을 저장한다.
// tokenHash 는 원문이 아니라 해시여야 한다(internal/token.HashRefresh).
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

// ConsumeRefreshToken 은 리프레시 토큰을 쓰면서 동시에 없앤다.
//
// 조회와 삭제를 한 문장으로 하는 게 중요하다. 따로 하면 같은 토큰으로 동시에
// 두 번 재발급 요청이 왔을 때 둘 다 통과해 토큰이 두 벌 생긴다.
// DELETE ... RETURNING 은 실제로 지운 행만 돌려주므로 딱 한 번만 성공한다.
//
// 만료됐거나, 없거나, 이미 쓴 토큰이면 ErrInvalidRefreshToken.
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

// DeleteRefreshTokensByUser 는 그 사용자의 모든 리프레시 토큰을 지운다.
// 로그아웃과 탈퇴에서 쓴다. 모든 기기에서 로그아웃되는 셈이다.
func (r *Repository) DeleteRefreshTokensByUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("리프레시 토큰 일괄 삭제: %w", err)
	}
	return nil
}

// DeleteExpiredRefreshTokens 는 만료된 토큰을 청소하고 지운 개수를 돌려준다.
// 쓰이지 않는 행이 쌓이기만 하므로 주기적으로 돌린다.
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
