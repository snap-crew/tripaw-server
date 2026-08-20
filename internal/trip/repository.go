package trip

import (
	"context"
	"fmt"
	"time"

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

const tripColumns = `r.id, r.user_id, r.title, r.start_date, r.end_date, r.themes,
	(SELECT count(*) FROM route_stops s WHERE s.route_id = r.id) AS place_count,
	r.created_at`

func (r *Repository) Featured(ctx context.Context, userID uuid.UUID, today time.Time) (*Trip, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+tripColumns+` FROM routes r
		WHERE r.user_id = $1 AND r.end_date >= $2::date
		ORDER BY (r.start_date <= $2::date) DESC, r.start_date ASC, r.id
		LIMIT 1`, userID, today)
	if err != nil {
		return nil, fmt.Errorf("대표 여행 조회: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("대표 여행 조회: %w", err)
		}
		return nil, ErrNotFound
	}
	return scanTrip(rows)
}

func (r *Repository) List(
	ctx context.Context, userID uuid.UUID, status string, today time.Time,
	excludeID *uuid.UUID, offset, limit int,
) ([]Trip, error) {
	where, order := `r.end_date >= $2::date`, `r.start_date ASC, r.id`
	if status == "past" {
		where, order = `r.end_date < $2::date`, `r.end_date DESC, r.id`
	}

	rows, err := r.pool.Query(ctx, `
		SELECT `+tripColumns+` FROM routes r
		WHERE r.user_id = $1 AND `+where+`
		  AND ($3::uuid IS NULL OR r.id <> $3)
		ORDER BY `+order+`
		OFFSET $4 LIMIT $5`,
		userID, today, excludeID, offset, limit)
	if err != nil {
		return nil, fmt.Errorf("여행 목록: %w", err)
	}
	defer rows.Close()

	var out []Trip
	for rows.Next() {
		t, err := scanTrip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*Trip, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+tripColumns+` FROM routes r WHERE r.id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("여행 조회: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("여행 조회: %w", err)
		}
		return nil, ErrNotFound
	}
	return scanTrip(rows)
}

func (r *Repository) LoadPets(ctx context.Context, trips []*Trip) error {
	if len(trips) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, 0, len(trips))
	byID := make(map[uuid.UUID]*Trip, len(trips))
	for _, t := range trips {
		ids = append(ids, t.ID)
		byID[t.ID] = t
	}

	rows, err := r.pool.Query(ctx, `
		SELECT rp.route_id, p.id, p.name
		FROM route_pets rp JOIN pet_profiles p ON p.id = rp.pet_profile_id
		WHERE rp.route_id = ANY($1)
		ORDER BY p.created_at`, ids)
	if err != nil {
		return fmt.Errorf("동반 반려동물 조회: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var routeID uuid.UUID
		var p Pet
		if err := rows.Scan(&routeID, &p.ID, &p.Name); err != nil {
			return fmt.Errorf("반려동물 스캔: %w", err)
		}
		if t := byID[routeID]; t != nil {
			t.Pets = append(t.Pets, p)
		}
	}
	return rows.Err()
}

func (r *Repository) LoadDays(ctx context.Context, t *Trip) error {
	if t.StartDate == nil || t.EndDate == nil {
		return nil
	}

	nights := int(t.EndDate.Sub(*t.StartDate).Hours() / 24)
	days := make([]Day, 0, nights+1)
	index := make(map[int]int, nights+1)
	for i := 0; i <= nights; i++ {
		index[i+1] = len(days)
		days = append(days, Day{
			DayNo: i + 1,
			Date:  t.StartDate.AddDate(0, 0, i),
			Stops: []Stop{},
		})
	}

	rows, err := r.pool.Query(ctx, `
		SELECT s.day_no, s.seq, s.place_id, p.name, p.category::text,
		       ST_Y(p.geom::geometry), ST_X(p.geom::geometry)
		FROM route_stops s JOIN places p ON p.id = s.place_id
		WHERE s.route_id = $1
		ORDER BY s.day_no, s.seq`, t.ID)
	if err != nil {
		return fmt.Errorf("일정 조회: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var dayNo int
		var s Stop
		if err := rows.Scan(&dayNo, &s.Seq, &s.PlaceID, &s.Name, &s.Category, &s.Lat, &s.Lng); err != nil {
			return fmt.Errorf("일정 스캔: %w", err)
		}

		if i, ok := index[dayNo]; ok {
			days[i].Stops = append(days[i].Stops, s)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	t.Days = days
	return nil
}

func (r *Repository) Create(
	ctx context.Context, userID uuid.UUID, title string,
	start, end time.Time, themes []string, petIDs []uuid.UUID,
) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("트랜잭션 시작: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lead *uuid.UUID
	if len(petIDs) > 0 {
		lead = &petIDs[0]
	}
	nights := int(end.Sub(start).Hours() / 24)

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO routes (user_id, pet_profile_id, title, nights, themes, start_date, end_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		userID, lead, title, nights, themes, start, end).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("여행 생성: %w", err)
	}

	if err := insertPets(ctx, tx, id, petIDs); err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("트랜잭션 커밋: %w", err)
	}
	return id, nil
}

func (r *Repository) Update(
	ctx context.Context, id uuid.UUID,
	title *string, start, end *time.Time, themes *[]string,
) error {
	var themeArg any
	if themes != nil {
		themeArg = *themes
	}

	_, err := r.pool.Exec(ctx, `
		UPDATE routes SET
			title      = COALESCE($2, title),
			start_date = COALESCE($3::date, start_date),
			end_date   = COALESCE($4::date, end_date),
			themes     = COALESCE($5::text[], themes),
			nights     = COALESCE($4::date, end_date) - COALESCE($3::date, start_date)
		WHERE id = $1`,
		id, title, start, end, themeArg)
	if err != nil {
		return fmt.Errorf("여행 수정: %w", err)
	}
	return nil
}

func (r *Repository) StopsBeyond(ctx context.Context, id uuid.UUID, maxDayNo int) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM route_stops WHERE route_id = $1 AND day_no > $2`,
		id, maxDayNo).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("기간 밖 일정 확인: %w", err)
	}
	return n, nil
}

func (r *Repository) Duplicate(ctx context.Context, src *Trip, newTitle string) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("트랜잭션 시작: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO routes (user_id, pet_profile_id, title, nights, transport,
		                    origin_geom, themes, start_date, end_date)
		SELECT user_id, pet_profile_id, $2, nights, transport,
		       origin_geom, themes, start_date, end_date
		FROM routes WHERE id = $1
		RETURNING id`, src.ID, newTitle).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("여행 복제: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO route_stops (route_id, day_no, seq, place_id, arrive_at,
		                         stay_minutes, travel_minutes_from_prev)
		SELECT $2, day_no, seq, place_id, arrive_at,
		       stay_minutes, travel_minutes_from_prev
		FROM route_stops WHERE route_id = $1`, src.ID, id); err != nil {
		return uuid.Nil, fmt.Errorf("일정 복제: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO route_pets (route_id, pet_profile_id)
		SELECT $2, pet_profile_id FROM route_pets WHERE route_id = $1`,
		src.ID, id); err != nil {
		return uuid.Nil, fmt.Errorf("동반 반려동물 복제: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("트랜잭션 커밋: %w", err)
	}
	return id, nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("여행 삭제: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) AddStops(
	ctx context.Context, tripID uuid.UUID, dayNo int, placeIDs []int64,
) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO route_stops (route_id, day_no, seq, place_id)
		SELECT $1, $2,
		       (SELECT COALESCE(max(seq), 0) FROM route_stops
		        WHERE route_id = $1 AND day_no = $2)
		         + row_number() OVER (ORDER BY p.ord),
		       p.place_id
		FROM unnest($3::bigint[]) WITH ORDINALITY AS p(place_id, ord)
		WHERE NOT EXISTS (
			SELECT 1 FROM route_stops s
			WHERE s.route_id = $1 AND s.day_no = $2 AND s.place_id = p.place_id
		)`, tripID, dayNo, placeIDs)
	if err != nil {
		return fmt.Errorf("일정 추가: %w", err)
	}
	return nil
}

func (r *Repository) DeleteStop(ctx context.Context, tripID uuid.UUID, dayNo, seq int) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM route_stops WHERE route_id = $1 AND day_no = $2 AND seq = $3`,
		tripID, dayNo, seq)
	if err != nil {
		return fmt.Errorf("일정 삭제: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ReorderDay(
	ctx context.Context, tripID uuid.UUID, dayNo int, placeIDs []int64,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("트랜잭션 시작: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current []int64
	rows, err := tx.Query(ctx,
		`SELECT place_id FROM route_stops WHERE route_id = $1 AND day_no = $2`,
		tripID, dayNo)
	if err != nil {
		return fmt.Errorf("기존 일정 조회: %w", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("일정 스캔: %w", err)
		}
		current = append(current, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if !sameSet(current, placeIDs) {
		return invalid("invalid_reorder",
			"순서 목록이 현재 일정과 다릅니다. 목록을 새로고침해 주세요")
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM route_stops WHERE route_id = $1 AND day_no = $2`,
		tripID, dayNo); err != nil {
		return fmt.Errorf("기존 일정 삭제: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO route_stops (route_id, day_no, seq, place_id)
		SELECT $1, $2, p.ord, p.place_id
		FROM unnest($3::bigint[]) WITH ORDINALITY AS p(place_id, ord)`,
		tripID, dayNo, placeIDs); err != nil {
		return fmt.Errorf("일정 재삽입: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("트랜잭션 커밋: %w", err)
	}
	return nil
}

func (r *Repository) PlacesExist(ctx context.Context, placeIDs []int64) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM places WHERE id = ANY($1)`, placeIDs).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("장소 확인: %w", err)
	}
	return n == len(placeIDs), nil
}

func (r *Repository) OwnedPetIDs(
	ctx context.Context, userID uuid.UUID, petIDs []uuid.UUID,
) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM pet_profiles WHERE user_id = $1 AND id = ANY($2)`,
		userID, petIDs)
	if err != nil {
		return nil, fmt.Errorf("반려동물 확인: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("반려동물 스캔: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func insertPets(ctx context.Context, tx pgx.Tx, tripID uuid.UUID, petIDs []uuid.UUID) error {
	if len(petIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO route_pets (route_id, pet_profile_id)
		SELECT $1, unnest($2::uuid[])
		ON CONFLICT DO NOTHING`, tripID, petIDs)
	if err != nil {
		return fmt.Errorf("동반 반려동물 연결: %w", err)
	}
	return nil
}

func (r *Repository) ReplacePets(ctx context.Context, tripID uuid.UUID, petIDs []uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("트랜잭션 시작: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM route_pets WHERE route_id = $1`, tripID); err != nil {
		return fmt.Errorf("기존 반려동물 해제: %w", err)
	}
	if err := insertPets(ctx, tx, tripID, petIDs); err != nil {
		return err
	}

	var lead *uuid.UUID
	if len(petIDs) > 0 {
		lead = &petIDs[0]
	}
	if _, err := tx.Exec(ctx,
		`UPDATE routes SET pet_profile_id = $2 WHERE id = $1`, tripID, lead); err != nil {
		return fmt.Errorf("대표 반려동물 갱신: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("트랜잭션 커밋: %w", err)
	}
	return nil
}

func scanTrip(row pgx.Row) (*Trip, error) {
	var t Trip
	err := row.Scan(&t.ID, &t.UserID, &t.Title, &t.StartDate, &t.EndDate,
		&t.Themes, &t.PlaceCount, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("여행 스캔: %w", err)
	}
	return &t, nil
}

func sameSet(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[int64]int, len(a))
	for _, v := range a {
		count[v]++
	}
	for _, v := range b {
		count[v]--
		if count[v] < 0 {
			return false
		}
	}
	return true
}
