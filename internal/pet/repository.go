package pet

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

const petColumns = `p.id, p.user_id, p.name, p.species, p.breed_id, b.name,
	p.size, p.traits,
	p.created_at, p.updated_at`

const petFrom = ` FROM pet_profiles p LEFT JOIN breeds b ON b.id = p.breed_id`

func (r *Repository) ListBreeds(ctx context.Context, species, q string) ([]Breed, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, image_url FROM breeds
		WHERE species = $1
		  AND ($2 = '' OR name ILIKE '%' || $2 || '%')
		ORDER BY name COLLATE "ko-KR-x-icu"`,
		species, q)
	if err != nil {
		return nil, fmt.Errorf("품종 조회: %w", err)
	}
	defer rows.Close()

	var out []Breed
	for rows.Next() {
		var b Breed
		if err := rows.Scan(&b.ID, &b.Name, &b.ImageURL); err != nil {
			return nil, fmt.Errorf("품종 스캔: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *Repository) BreedSpecies(ctx context.Context, breedID int) (string, error) {
	var species string
	err := r.pool.QueryRow(ctx, `SELECT species FROM breeds WHERE id = $1`, breedID).Scan(&species)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("품종 종 조회: %w", err)
	}
	return species, nil
}

func (r *Repository) SaveDraft(ctx context.Context, userID uuid.UUID, step int, payload []byte) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO pet_profile_drafts (user_id, step, payload, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id) DO UPDATE SET
			step       = EXCLUDED.step,
			payload    = EXCLUDED.payload,
			updated_at = now()`,
		userID, step, payload)
	if err != nil {
		return fmt.Errorf("임시 저장: %w", err)
	}
	return nil
}

func (r *Repository) FindDraft(ctx context.Context, userID uuid.UUID) (*Draft, error) {
	var d Draft
	err := r.pool.QueryRow(ctx,
		`SELECT step, payload, updated_at FROM pet_profile_drafts WHERE user_id = $1`,
		userID).Scan(&d.Step, &d.Payload, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDraftNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("임시 저장 조회: %w", err)
	}
	return &d, nil
}

func (r *Repository) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM pet_profiles WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("반려동물 수 조회: %w", err)
	}
	return n, nil
}

func (r *Repository) Create(ctx context.Context, p *Pet) (*Pet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("트랜잭션 시작: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO pet_profiles
			(user_id, name, species, breed_id, size, traits)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		p.UserID, p.Name, p.Species, p.BreedID, p.Size, p.Traits,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("반려동물 등록: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM pet_profile_drafts WHERE user_id = $1`, p.UserID); err != nil {
		return nil, fmt.Errorf("임시 저장 삭제: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("트랜잭션 커밋: %w", err)
	}

	return r.FindByID(ctx, id)
}

func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Pet, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+petColumns+petFrom+` WHERE p.user_id = $1 ORDER BY p.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("반려동물 목록: %w", err)
	}
	defer rows.Close()

	var out []Pet
	for rows.Next() {
		p, err := scanPet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*Pet, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+petColumns+petFrom+` WHERE p.id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("반려동물 조회: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("반려동물 조회: %w", err)
		}
		return nil, ErrNotFound
	}
	return scanPet(rows)
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, u *petUpdate) (*Pet, error) {
	_, err := r.pool.Exec(ctx, `
		UPDATE pet_profiles SET
			name       = COALESCE($2, name),
			species    = COALESCE($3::pet_species, species),
			breed_id   = COALESCE($4, breed_id),
			size       = COALESCE($5::size_limit, size),
			traits     = COALESCE($6::pet_trait[], traits),
			updated_at = now()
		WHERE id = $1`,
		id, u.Name, u.Species, u.BreedID, u.Size, u.Traits,
	)
	if err != nil {
		return nil, fmt.Errorf("반려동물 수정: %w", err)
	}
	return r.FindByID(ctx, id)
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM pet_profiles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("반려동물 삭제: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type petUpdate struct {
	Name    *string
	Species *string
	BreedID *int
	Size    *string

	Traits []string
}

func scanPet(row pgx.Row) (*Pet, error) {
	var p Pet
	err := row.Scan(
		&p.ID, &p.UserID, &p.Name, &p.Species, &p.BreedID, &p.BreedName,
		&p.Size, &p.Traits,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("반려동물 스캔: %w", err)
	}
	return &p, nil
}
