package terms

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const latestTermsSQL = `
	SELECT DISTINCT ON (code) id, code, version, required, title, content_url
	FROM terms
	ORDER BY code, effective_from DESC`

func (r *Repository) ListLatest(ctx context.Context) ([]Term, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, code, version, required, title, content_url FROM (`+latestTermsSQL+`) t
		ORDER BY CASE code
			WHEN 'service'   THEN 1
			WHEN 'privacy'   THEN 2
			WHEN 'location'  THEN 3
			WHEN 'marketing' THEN 4
			ELSE 5
		END, code`)
	if err != nil {
		return nil, fmt.Errorf("약관 목록 조회: %w", err)
	}
	defer rows.Close()

	var out []Term
	for rows.Next() {
		var t Term
		if err := rows.Scan(&t.ID, &t.Code, &t.Version, &t.Required, &t.Title, &t.ContentURL); err != nil {
			return nil, fmt.Errorf("약관 스캔: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) RequiredTermIDs(ctx context.Context) ([]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM (`+latestTermsSQL+`) t WHERE required`)
	if err != nil {
		return nil, fmt.Errorf("필수 약관 조회: %w", err)
	}
	defer rows.Close()

	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("필수 약관 스캔: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Repository) SaveAgreements(ctx context.Context, userID uuid.UUID, items []Agreement) error {
	ids := make([]int, len(items))
	agreed := make([]bool, len(items))
	for i, a := range items {
		ids[i] = a.TermID
		agreed[i] = a.Agreed
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_term_agreements (user_id, term_id, agreed, agreed_at)
		SELECT $1, t.id, t.agreed, now()
		FROM unnest($2::int[], $3::boolean[]) AS t(id, agreed)
		ON CONFLICT (user_id, term_id) DO UPDATE SET
			agreed    = EXCLUDED.agreed,
			agreed_at = now()`,
		userID, ids, agreed,
	)
	if err != nil {
		return fmt.Errorf("약관 동의 저장: %w", err)
	}
	return nil
}
