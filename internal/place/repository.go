package place

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const placeColumns = `v.id, v.name, v.category, v.road_address, v.tel, v.lat, v.lng,
	v.image_url, v.image_count,
	v.status, v.area, v.size_limit, v.max_weight_kg,
	v.leash_required, v.muzzle_required, v.muzzle_dangerous_only,
	v.crate_required, v.waste_bag_required,
	v.extra_fee_krw, v.parking_available, v.needs_verification, v.open_time,
	pl.homepage_url,
	(s.user_id IS NOT NULL) AS is_saved`

const placeFrom = `
	FROM place_view v
	JOIN places pl ON pl.id = v.id
	LEFT JOIN saved_places s ON s.place_id = v.id AND s.user_id = $1`

func whereFilters(f *Filter, args *[]any) string {
	var conds []string
	add := func(v any) string {
		*args = append(*args, v)
		return "$" + strconv.Itoa(len(*args))
	}

	if f.Category != "" {
		conds = append(conds, "v.category = "+add(f.Category)+"::place_category")
	}
	if f.MaxWeightKg != nil {
		conds = append(conds,
			"(v.max_weight_kg IS NULL OR v.max_weight_kg >= "+add(*f.MaxWeightKg)+")")
	}
	if f.Area != "" {
		conds = append(conds,
			"(v.area IS NULL OR v.area = 'unknown' OR v.area IN ("+add(f.Area)+"::allowed_area, 'both'))")
	}
	if f.MaxFeeKrw != nil {
		conds = append(conds, "COALESCE(v.extra_fee_krw, 0) <= "+add(*f.MaxFeeKrw))
	}
	if f.Leash != nil {
		conds = append(conds,
			"(v.leash_required IS NULL OR v.leash_required = "+add(*f.Leash)+")")
	}
	if f.Muzzle != nil {
		conds = append(conds,
			"(v.muzzle_required IS NULL OR "+
				"(v.muzzle_required AND v.muzzle_dangerous_only IS NOT TRUE) = "+add(*f.Muzzle)+")")
	}
	if f.Parking {
		conds = append(conds, "v.parking_available IS TRUE")
	}
	if f.WasteBag {
		conds = append(conds, "v.waste_bag_required IS TRUE")
	}

	if len(conds) == 0 {
		return ""
	}
	return " AND " + strings.Join(conds, " AND ")
}

func (r *Repository) Markers(
	ctx context.Context, userID uuid.UUID, b BBox, zoom int, f *Filter,
) ([]Marker, int, error) {
	args := []any{userID, b.MinLng, b.MinLat, b.MaxLng, b.MaxLat}
	where := whereFilters(f, &args)
	cell := clusterCellDeg(zoom)
	args = append(args, cell)
	cellArg := "$" + strconv.Itoa(len(args))

	rows, err := r.pool.Query(ctx, `
		WITH f AS (
			SELECT v.id, v.name, v.category::text AS category, v.lat, v.lng
			`+placeFrom+`
			WHERE v.geom && ST_MakeEnvelope($2, $3, $4, $5, 4326)::geography
			`+where+`
		)
		SELECT count(*) AS n,
		       avg(lat) AS lat, avg(lng) AS lng,
		       (array_agg(id       ORDER BY id))[1] AS place_id,
		       (array_agg(name     ORDER BY id))[1] AS name,
		       (array_agg(category ORDER BY id))[1] AS category
		FROM f
		GROUP BY floor(lat / `+cellArg+`), floor(lng / `+cellArg+`)`,
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("지도 마커 조회: %w", err)
	}
	defer rows.Close()

	var out []Marker
	total := 0
	for rows.Next() {
		var m Marker
		var n int64
		if err := rows.Scan(&n, &m.Lat, &m.Lng, &m.PlaceID, &m.Name, &m.Category); err != nil {
			return nil, 0, fmt.Errorf("마커 스캔: %w", err)
		}
		m.Count = int(n)
		total += m.Count

		if m.Count > 1 {
			m.PlaceID, m.Name, m.Category = nil, nil, nil
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (r *Repository) Search(
	ctx context.Context, userID uuid.UUID, q string, limit int,
) ([]Place, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+placeColumns+`, NULL::float8 AS distance`+placeFrom+`
		WHERE v.name ILIKE '%' || $2 || '%'
		ORDER BY (v.name ILIKE $2 || '%') DESC, char_length(v.name), v.id
		LIMIT $3`,
		userID, q, limit)
	if err != nil {
		return nil, fmt.Errorf("장소 검색: %w", err)
	}
	return collectPlaces(rows)
}

func (r *Repository) List(
	ctx context.Context, userID uuid.UUID, f *Filter,
	sort string, lat, lng *float64, cur *cursor, limit int,
) ([]Place, error) {
	args := []any{userID}
	where := whereFilters(f, &args)

	distExpr := "NULL::float8"
	if lat != nil && lng != nil {
		args = append(args, *lng, *lat)
		distExpr = fmt.Sprintf(
			"ST_Distance(v.geom, ST_SetSRID(ST_MakePoint($%d, $%d), 4326)::geography)",
			len(args)-1, len(args))
	}

	var orderBy, keyExpr string
	switch sort {
	case "distance":
		orderBy, keyExpr = distExpr+" ASC, v.id ASC", distExpr
	case "name":
		orderBy = `v.name COLLATE "ko-KR-x-icu" ASC, v.id ASC`
		keyExpr = "v.name"
	default:
		orderBy, keyExpr = "v.id ASC", "NULL"
	}

	if cur != nil {
		switch sort {
		case "distance":
			args = append(args, cur.Num, cur.ID)
			where += fmt.Sprintf(" AND (%s, v.id) > ($%d, $%d)",
				distExpr, len(args)-1, len(args))
		case "name":
			args = append(args, cur.Str, cur.ID)
			where += fmt.Sprintf(
				` AND (v.name COLLATE "ko-KR-x-icu", v.id) > ($%d COLLATE "ko-KR-x-icu", $%d)`,
				len(args)-1, len(args))
		default:
			args = append(args, cur.ID)
			where += fmt.Sprintf(" AND v.id > $%d", len(args))
		}
	}

	args = append(args, limit)
	rows, err := r.pool.Query(ctx, `
		SELECT `+placeColumns+`, `+distExpr+` AS distance, `+keyExpr+` AS sort_key
		`+placeFrom+`
		WHERE true`+where+`
		ORDER BY `+orderBy+`
		LIMIT $`+strconv.Itoa(len(args)),
		args...)
	if err != nil {
		return nil, fmt.Errorf("장소 목록: %w", err)
	}
	defer rows.Close()

	var out []Place
	for rows.Next() {
		p, err := scanPlaceWithKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *Repository) Count(ctx context.Context, userID uuid.UUID, f *Filter) (int, error) {
	args := []any{userID}
	where := whereFilters(f, &args)

	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*)`+placeFrom+` WHERE true`+where, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("장소 개수: %w", err)
	}
	return n, nil
}

// 홈탭 추천 기준. 개인화하지 않는다.
//
// 뽑는 대상 — 동반 가능하고(status=allowed), 정책 확인이 끝났고(needs_verification=false),
// 대표 사진이 있는 곳. 사진 없이는 카드가 비어 보인다.
// 카테고리는 화이트리스트다. 숙소·쇼핑은 홈탭에서 찾는 것이 아니고,
// 동물병원(vet)과 애견미용실(other)은 여행지가 아니다.
//
// 순위 — 조회수·평점 같은 인기 데이터가 아직 없어서 다음 순서로 대신한다.
//  1. 카테고리 라운드로빈. 그냥 줄세우면 10칸이 전부 오름으로 찬다
//  2. 동반 정책 신뢰도 순 (관광공사 0.90 > 비짓제주 태그 0.75 > 소개글 마이닝 0.70)
//  3. 사진 수, id 순
func (r *Repository) Recommended(ctx context.Context, userID uuid.UUID, limit int) ([]Place, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+placeColumns+`, NULL::float8 AS distance`+placeFrom+`
		WHERE v.status = 'allowed' AND NOT v.needs_verification
		  AND v.category IN ('attraction', 'cafe', 'restaurant', 'culture', 'leisure', 'park', 'beach')
		  AND v.image_url IS NOT NULL
		ORDER BY row_number() OVER (PARTITION BY v.category ORDER BY `+recommendRank+`),
		         `+recommendRank+`
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("추천 장소: %w", err)
	}
	return collectPlaces(rows)
}

const recommendRank = `v.confidence DESC NULLS LAST, v.image_count DESC, v.id`

func (r *Repository) FindByID(ctx context.Context, userID uuid.UUID, placeID int64) (*Place, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+placeColumns+`, NULL::float8 AS distance`+placeFrom+` WHERE v.id = $2`,
		userID, placeID)
	if err != nil {
		return nil, fmt.Errorf("장소 조회: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("장소 조회: %w", err)
		}
		return nil, ErrNotFound
	}
	return scanPlace(rows)
}

func (r *Repository) Images(ctx context.Context, placeID int64) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT url FROM place_images WHERE place_id = $1
		ORDER BY (role <> 'main'), sort_order, id`, placeID)
	if err != nil {
		return nil, fmt.Errorf("장소 사진: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("사진 스캔: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repository) ListSaved(
	ctx context.Context, userID uuid.UUID, category string, cur *cursor, limit int,
) ([]SavedPlace, error) {
	args := []any{userID}
	where := ""
	if category != "" {
		args = append(args, category)
		where += fmt.Sprintf(" AND v.category = $%d::place_category", len(args))
	}
	if cur != nil {
		args = append(args, cur.Str, cur.ID)
		where += fmt.Sprintf(" AND (s.saved_at, v.id) < ($%d::timestamptz, $%d)",
			len(args)-1, len(args))
	}
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, `
		SELECT `+placeColumns+`, NULL::float8 AS distance, s.saved_at::text
		`+placeFrom+`
		WHERE s.user_id IS NOT NULL`+where+`
		ORDER BY s.saved_at DESC, v.id DESC
		LIMIT $`+strconv.Itoa(len(args)),
		args...)
	if err != nil {
		return nil, fmt.Errorf("저장한 장소 목록: %w", err)
	}
	defer rows.Close()

	var out []SavedPlace
	for rows.Next() {
		var sp SavedPlace
		dest := append(placeScanDest(&sp.Place), new(any), &sp.SavedAt)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("저장한 장소 스캔: %w", err)
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (r *Repository) SavedCategories(ctx context.Context, userID uuid.UUID) ([]CategoryCount, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT v.category::text, count(*)
		FROM saved_places s JOIN place_view v ON v.id = s.place_id
		WHERE s.user_id = $1
		GROUP BY v.category
		ORDER BY count(*) DESC, v.category::text`, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("저장 카테고리 집계: %w", err)
	}
	defer rows.Close()

	var out []CategoryCount
	total := 0
	for rows.Next() {
		var c CategoryCount
		if err := rows.Scan(&c.Category, &c.Count); err != nil {
			return nil, 0, fmt.Errorf("카테고리 스캔: %w", err)
		}
		total += c.Count
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (r *Repository) Save(ctx context.Context, userID uuid.UUID, placeID int64) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO saved_places (user_id, place_id) VALUES ($1, $2)
		ON CONFLICT (user_id, place_id) DO NOTHING`, userID, placeID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("장소 저장: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repository) Unsave(ctx context.Context, userID uuid.UUID, placeID int64) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM saved_places WHERE user_id = $1 AND place_id = $2`,
		userID, placeID); err != nil {
		return fmt.Errorf("저장 해제: %w", err)
	}
	return nil
}

func placeScanDest(p *Place) []any {
	return []any{
		&p.ID, &p.Name, &p.Category, &p.RoadAddress, &p.Tel, &p.Lat, &p.Lng,
		&p.ImageURL, &p.ImageCount,
		&p.Status, &p.Area, &p.SizeLimit, &p.MaxWeightKg,
		&p.LeashRequired, &p.MuzzleRequired, &p.MuzzleDangerousOnly,
		&p.CrateRequired, &p.WasteBagRequired,
		&p.ExtraFeeKrw, &p.ParkingAvailable, &p.NeedsVerification, &p.OpenTime,
		&p.HomepageURL,
		&p.IsSaved,
	}
}

func scanPlace(row pgx.Row) (*Place, error) {
	var p Place
	dest := append(placeScanDest(&p), &p.Distance)
	if err := row.Scan(dest...); err != nil {
		return nil, fmt.Errorf("장소 스캔: %w", err)
	}
	return &p, nil
}

func scanPlaceWithKey(row pgx.Row) (*Place, error) {
	var p Place
	var key any
	dest := append(placeScanDest(&p), &p.Distance, &key)
	if err := row.Scan(dest...); err != nil {
		return nil, fmt.Errorf("장소 스캔: %w", err)
	}
	p.sortKey = key
	return &p, nil
}

func collectPlaces(rows pgx.Rows) ([]Place, error) {
	defer rows.Close()

	var out []Place
	for rows.Next() {
		p, err := scanPlace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
