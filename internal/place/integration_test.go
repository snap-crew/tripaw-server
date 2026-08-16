package place

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type seedPlace struct {
	name         string
	category     string
	lat, lng     float64
	status       string
	area         string
	muzzle       *bool
	muzzleDanger *bool
	sizeLimit    string
	maxWeightKg  *float64
	leash        *bool
	extraFee     *int
	parking      *bool
	image        bool
}

func f64(v float64) *float64 { return &v }
func boolp(v bool) *bool     { return &v }
func intp(v int) *int        { return &v }

func testSvc(t *testing.T, seeds ...seedPlace) (*Service, *pgxpool.Pool, uuid.UUID) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL 이 없어 통합 테스트를 건너뜁니다 (make test-db && make test)")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("테스트 DB 연결: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("테스트 DB 에 연결할 수 없습니다: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()

	for _, q := range []string{`TRUNCATE users CASCADE`, `TRUNCATE places CASCADE`,
		`TRUNCATE source_documents CASCADE`} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("테스트 DB 초기화(%s): %v", q, err)
		}
	}

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao','place-test') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("테스트 사용자 생성: %v", err)
	}

	for i, s := range seeds {
		seed(t, pool, i, s)
	}

	if _, err := pool.Exec(ctx, `REFRESH MATERIALIZED VIEW place_view`); err != nil {
		t.Fatalf("place_view 갱신: %v", err)
	}

	return NewService(NewRepository(pool)), pool, userID
}

func seed(t *testing.T, pool *pgxpool.Pool, i int, s seedPlace) {
	t.Helper()
	ctx := context.Background()

	var docID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO source_documents (source, source_ref, payload, source_dated_at)
		VALUES ('kcisa_csv', $1, '{}'::jsonb, CURRENT_DATE)
		RETURNING id`, fmt.Sprintf("test-doc-%d", i)).Scan(&docID)
	if err != nil {
		t.Fatalf("source_documents: %v", err)
	}

	var placeID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO places (name, norm_name, category, geom, road_address, parking_available)
		VALUES ($1, $1, $2::place_category,
		        ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5, $6)
		RETURNING id`,
		s.name, s.category, s.lng, s.lat, "제주시 테스트로 "+s.name, s.parking).Scan(&placeID)
	if err != nil {
		t.Fatalf("places: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO pet_policies (place_id, document_id, status, area, size_limit,
			max_weight_kg, leash_required, extra_fee_krw,
			evidence_text, method, confidence, needs_review,
			muzzle_required, muzzle_dangerous_only)
		VALUES ($1, $2, $3::allow_status, $4::allowed_area, $5::size_limit,
			$6, $7, $8, '테스트 근거', 'csv_column', 0.90, false, $9, $10)`,
		placeID, docID, s.status, s.area, s.sizeLimit,
		s.maxWeightKg, s.leash, s.extraFee, s.muzzle, s.muzzleDanger)
	if err != nil {
		t.Fatalf("pet_policies: %v", err)
	}

	if s.image {
		_, err = pool.Exec(ctx, `
			INSERT INTO place_images (place_id, document_id, url, role, sort_order)
			VALUES ($1, $2, $3, 'main', 0)`,
			placeID, docID, "https://cdn.test/"+s.name+".jpg")
		if err != nil {
			t.Fatalf("place_images: %v", err)
		}
	}
}

func jeju(name, category string, lat, lng float64) seedPlace {
	return seedPlace{
		name: name, category: category, lat: lat, lng: lng,
		status: "allowed", area: "outdoor", sizeLimit: "large",
	}
}

func placeIDByName(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM places WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatalf("장소 %q 조회: %v", name, err)
	}
	return id
}

func TestFilterPassesNullAsUnrestricted(t *testing.T) {
	heavy := seedPlace{
		name: "무게제한20", category: "cafe", lat: 33.50, lng: 126.53,
		status: "allowed", area: "indoor", sizeLimit: "large", maxWeightKg: f64(20),
	}
	light := seedPlace{
		name: "무게제한5", category: "cafe", lat: 33.51, lng: 126.54,
		status: "allowed", area: "indoor", sizeLimit: "small", maxWeightKg: f64(5),
	}
	unknown := seedPlace{
		name: "무게정보없음", category: "cafe", lat: 33.52, lng: 126.55,
		status: "allowed", area: "unknown", sizeLimit: "unknown",
	}

	svc, _, userID := testSvc(t, heavy, light, unknown)
	ctx := context.Background()

	items, _, _, err := svc.List(ctx, userID, &Filter{MaxWeightKg: f64(10)}, "", nil, nil, "", 50)
	if err != nil {
		t.Fatalf("무게 필터: %v", err)
	}

	got := names(items)
	if !contains(got, "무게제한20") {
		t.Error("20kg 허용 장소가 빠졌다")
	}
	if contains(got, "무게제한5") {
		t.Error("5kg 제한 장소가 10kg 필터를 통과했다")
	}
	if !contains(got, "무게정보없음") {
		t.Error("무게 정보가 없는 장소가 걸러졌다 — " +
			"NULL 은 '제한 없음' 으로 통과시켜야 한다")
	}
}

func TestFilterAreaTreatsUnknownAsPass(t *testing.T) {
	indoor := seedPlace{name: "실내", category: "cafe", lat: 33.50, lng: 126.53,
		status: "allowed", area: "indoor", sizeLimit: "large"}
	outdoor := seedPlace{name: "야외", category: "cafe", lat: 33.51, lng: 126.54,
		status: "allowed", area: "outdoor", sizeLimit: "large"}
	both := seedPlace{name: "실내외", category: "cafe", lat: 33.52, lng: 126.55,
		status: "allowed", area: "both", sizeLimit: "large"}
	unknown := seedPlace{name: "정보없음", category: "cafe", lat: 33.53, lng: 126.56,
		status: "allowed", area: "unknown", sizeLimit: "large"}

	svc, _, userID := testSvc(t, indoor, outdoor, both, unknown)

	items, _, _, err := svc.List(context.Background(), userID,
		&Filter{Area: "indoor"}, "", nil, nil, "", 50)
	if err != nil {
		t.Fatalf("실내 필터: %v", err)
	}

	got := names(items)
	for _, want := range []string{"실내", "실내외", "정보없음"} {
		if !contains(got, want) {
			t.Errorf("%q 가 실내 필터에서 빠졌다 (결과: %v)", want, got)
		}
	}
	if contains(got, "야외") {
		t.Error("야외 전용 장소가 실내 필터를 통과했다")
	}
}

func TestFilterFeeTreatsNullAsFree(t *testing.T) {
	free := seedPlace{name: "무료", category: "cafe", lat: 33.50, lng: 126.53,
		status: "allowed", area: "both", sizeLimit: "large", extraFee: intp(0)}
	paid := seedPlace{name: "유료2만", category: "cafe", lat: 33.51, lng: 126.54,
		status: "allowed", area: "both", sizeLimit: "large", extraFee: intp(20000)}
	unknown := seedPlace{name: "요금정보없음", category: "cafe", lat: 33.52, lng: 126.55,
		status: "allowed", area: "both", sizeLimit: "large"}

	svc, _, userID := testSvc(t, free, paid, unknown)

	items, _, _, err := svc.List(context.Background(), userID,
		&Filter{MaxFeeKrw: intp(10000)}, "", nil, nil, "", 50)
	if err != nil {
		t.Fatalf("요금 필터: %v", err)
	}

	got := names(items)
	if !contains(got, "무료") || !contains(got, "요금정보없음") {
		t.Errorf("무료·정보없음이 1만원 필터를 통과해야 한다 (결과: %v)", got)
	}
	if contains(got, "유료2만") {
		t.Error("2만원 장소가 1만원 필터를 통과했다")
	}
}

func TestFilterFacilityExcludesUnknown(t *testing.T) {
	yes := seedPlace{name: "주차가능", category: "cafe", lat: 33.50, lng: 126.53,
		status: "allowed", area: "both", sizeLimit: "large", parking: boolp(true)}
	no := seedPlace{name: "주차불가", category: "cafe", lat: 33.51, lng: 126.54,
		status: "allowed", area: "both", sizeLimit: "large", parking: boolp(false)}
	unknown := seedPlace{name: "주차정보없음", category: "cafe", lat: 33.52, lng: 126.55,
		status: "allowed", area: "both", sizeLimit: "large"}

	svc, _, userID := testSvc(t, yes, no, unknown)

	items, _, _, err := svc.List(context.Background(), userID,
		&Filter{Parking: true}, "", nil, nil, "", 50)
	if err != nil {
		t.Fatalf("주차 필터: %v", err)
	}

	got := names(items)
	if len(got) != 1 || got[0] != "주차가능" {
		t.Errorf("결과 = %v, 기대 [주차가능] — 확인된 곳만 나와야 한다", got)
	}
}

func TestMarkersRespectBBox(t *testing.T) {
	inside := jeju("제주시내", "cafe", 33.50, 126.53)
	outside := jeju("서귀포", "cafe", 33.25, 126.56)

	svc, _, userID := testSvc(t, inside, outside)

	b := BBox{MinLat: 33.45, MaxLat: 33.55, MinLng: 126.50, MaxLng: 126.60}
	markers, total, err := svc.Map(context.Background(), userID, b, 18, &Filter{})
	if err != nil {
		t.Fatalf("지도 조회: %v", err)
	}

	if total != 1 {
		t.Fatalf("total = %d, 기대 1 — BBox 밖 장소가 섞였다", total)
	}
	if len(markers) != 1 || markers[0].Name == nil || *markers[0].Name != "제주시내" {
		t.Errorf("마커 = %+v, 기대 제주시내 하나", markers)
	}
}

func TestMarkersClusterByZoom(t *testing.T) {
	a := jeju("가", "cafe", 33.5000, 126.5300)
	bb := jeju("나", "cafe", 33.5004, 126.5304)
	cc := jeju("다", "cafe", 33.5008, 126.5308)

	svc, _, userID := testSvc(t, a, bb, cc)
	ctx := context.Background()
	box := BBox{MinLat: 33.40, MaxLat: 33.60, MinLng: 126.40, MaxLng: 126.60}

	markers, total, err := svc.Map(ctx, userID, box, 11, &Filter{})
	if err != nil {
		t.Fatalf("줌 11: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, 기대 3", total)
	}
	if len(markers) != 1 || markers[0].Count != 3 {
		t.Errorf("줌 11 마커 = %d개(%+v), 기대 클러스터 1개(count 3)", len(markers), markers)
	}
	if markers[0].PlaceID != nil {
		t.Error("클러스터에는 개별 장소 정보가 없어야 한다")
	}

	markers, _, err = svc.Map(ctx, userID, box, 20, &Filter{})
	if err != nil {
		t.Fatalf("줌 20: %v", err)
	}
	if len(markers) != 3 {
		t.Errorf("줌 20 마커 = %d개, 기대 3개", len(markers))
	}
	for _, m := range markers {
		if m.Count != 1 || m.PlaceID == nil {
			t.Errorf("줌 20 에서 마커가 개별 장소여야 한다: %+v", m)
		}
	}
}

func TestSearchRanksPrefixFirst(t *testing.T) {
	prefix := jeju("협재 해수욕장", "beach", 33.39, 126.24)
	middle := jeju("금능협재로 카페", "cafe", 33.40, 126.25)
	other := jeju("성산일출봉", "attraction", 33.46, 126.94)

	svc, _, userID := testSvc(t, prefix, middle, other)

	items, err := svc.Search(context.Background(), userID, "협재", 20)
	if err != nil {
		t.Fatalf("검색: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("결과 %d건, 기대 2건 (%v)", len(items), names(items))
	}
	if items[0].Name != "협재 해수욕장" {
		t.Errorf("첫 결과 = %q, 기대 %q — 접두 일치가 먼저다",
			items[0].Name, "협재 해수욕장")
	}
}

func TestSearchEmptyIsNotError(t *testing.T) {
	svc, _, userID := testSvc(t, jeju("협재 해수욕장", "beach", 33.39, 126.24))
	ctx := context.Background()

	items, err := svc.Search(ctx, userID, "존재하지않는장소", 20)
	if err != nil {
		t.Fatalf("빈 검색: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("결과 %d건, 기대 0건", len(items))
	}

	if items, err = svc.Search(ctx, userID, "   ", 20); err != nil || len(items) != 0 {
		t.Errorf("공백 검색: items=%d err=%v", len(items), err)
	}
}

func TestListCursorCoversEveryRowOnce(t *testing.T) {
	var seeds []seedPlace
	for i := 0; i < 25; i++ {
		seeds = append(seeds, jeju(fmt.Sprintf("장소%02d", i), "cafe",
			33.40+float64(i)*0.001, 126.50+float64(i)*0.001))
	}

	svc, _, userID := testSvc(t, seeds...)
	ctx := context.Background()

	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 10; page++ {
		items, next, total, err := svc.List(ctx, userID, &Filter{}, "name", nil, nil, cursor, 10)
		if err != nil {
			t.Fatalf("%d페이지: %v", page, err)
		}
		if total != 25 {
			t.Errorf("total = %d, 기대 25", total)
		}
		for _, p := range items {
			seen[p.Name]++
		}
		if next == "" {
			break
		}
		cursor = next
	}

	if len(seen) != 25 {
		t.Errorf("본 장소 %d개, 기대 25개 — 누락이 있다", len(seen))
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("%q 가 %d번 나왔다 — 중복이다", name, n)
		}
	}
}

func TestListRejectsBadCursor(t *testing.T) {
	svc, _, userID := testSvc(t, jeju("가", "cafe", 33.5, 126.5))

	_, _, _, err := svc.List(context.Background(), userID,
		&Filter{}, "name", nil, nil, "!!!not-base64!!!", 10)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("err = %v, 기대 ErrInvalidCursor", err)
	}
}

func TestListDistanceSortWithoutCoordsFallsBack(t *testing.T) {
	svc, _, userID := testSvc(t, jeju("가", "cafe", 33.5, 126.5), jeju("나", "cafe", 33.6, 126.6))

	items, _, _, err := svc.List(context.Background(), userID,
		&Filter{}, "distance", nil, nil, "", 10)
	if err != nil {
		t.Fatalf("좌표 없는 거리순: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("결과 %d건, 기대 2건", len(items))
	}
}

func TestListSortsByDistance(t *testing.T) {
	near := jeju("가까운곳", "cafe", 33.5000, 126.5300)
	far := jeju("먼곳", "cafe", 33.2500, 126.5600)

	svc, _, userID := testSvc(t, far, near)

	lat, lng := 33.5001, 126.5301
	items, _, _, err := svc.List(context.Background(), userID,
		&Filter{}, "distance", &lat, &lng, "", 10)
	if err != nil {
		t.Fatalf("거리순: %v", err)
	}
	if items[0].Name != "가까운곳" {
		t.Errorf("첫 결과 = %q, 기대 가까운곳", items[0].Name)
	}
	if items[0].Distance == nil {
		t.Fatal("distance 가 채워지지 않았다")
	}
	if *items[0].Distance > 100 {
		t.Errorf("거리 = %.1fm, 기대 100m 이내", *items[0].Distance)
	}
}

func TestGetReturnsDetailWithImages(t *testing.T) {
	withImg := seedPlace{name: "사진있는곳", category: "beach", lat: 33.39, lng: 126.24,
		status: "allowed", area: "outdoor", sizeLimit: "large",
		leash: boolp(true), extraFee: intp(0), parking: boolp(true), image: true}

	svc, pool, userID := testSvc(t, withImg)
	id := placeIDByName(t, pool, "사진있는곳")

	p, images, err := svc.Get(context.Background(), userID, id)
	if err != nil {
		t.Fatalf("상세: %v", err)
	}
	if p.Name != "사진있는곳" {
		t.Errorf("이름 = %q", p.Name)
	}
	if len(images) != 1 {
		t.Errorf("사진 %d장, 기대 1장", len(images))
	}

	conds := petConditions(p)
	for _, want := range []string{"대형견", "리드줄 필수", "동반요금 없음", "주차 가능"} {
		if !contains(conds, want) {
			t.Errorf("%q 문구가 없다 (조건: %v)", want, conds)
		}
	}
}

func TestGetMissingPlace(t *testing.T) {
	svc, _, userID := testSvc(t)

	if _, _, err := svc.Get(context.Background(), userID, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, 기대 ErrNotFound", err)
	}
}

func TestSaveIsIdempotentAndReflectedInIsSaved(t *testing.T) {
	svc, pool, userID := testSvc(t, jeju("협재 해수욕장", "beach", 33.39, 126.24))
	ctx := context.Background()
	id := placeIDByName(t, pool, "협재 해수욕장")

	created, err := svc.Save(ctx, userID, id)
	if err != nil {
		t.Fatalf("저장: %v", err)
	}
	if !created {
		t.Error("첫 저장인데 created=false")
	}

	created, err = svc.Save(ctx, userID, id)
	if err != nil {
		t.Fatalf("재저장: %v", err)
	}
	if created {
		t.Error("이미 저장된 장소인데 created=true")
	}

	p, _, err := svc.Get(ctx, userID, id)
	if err != nil {
		t.Fatalf("상세: %v", err)
	}
	if !p.IsSaved {
		t.Error("isSaved 가 false — 저장 상태가 장소 조회에 반영되지 않는다")
	}

	if err := svc.Unsave(ctx, userID, id); err != nil {
		t.Fatalf("해제: %v", err)
	}
	if err := svc.Unsave(ctx, userID, id); err != nil {
		t.Fatalf("이미 해제된 것 재해제: %v", err)
	}

	if p, _, _ = svc.Get(ctx, userID, id); p.IsSaved {
		t.Error("해제 후에도 isSaved 가 true")
	}
}

func TestSaveMissingPlaceIsNotFound(t *testing.T) {
	svc, _, userID := testSvc(t)

	if _, err := svc.Save(context.Background(), userID, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, 기대 ErrNotFound", err)
	}
}

func TestListSavedNewestFirstAndCategories(t *testing.T) {
	svc, pool, userID := testSvc(t,
		jeju("카페1", "cafe", 33.50, 126.53),
		jeju("카페2", "cafe", 33.51, 126.54),
		jeju("해변1", "beach", 33.39, 126.24),
	)
	ctx := context.Background()

	for _, name := range []string{"카페1", "해변1", "카페2"} {
		if _, err := svc.Save(ctx, userID, placeIDByName(t, pool, name)); err != nil {
			t.Fatalf("%s 저장: %v", name, err)
		}
	}

	items, _, err := svc.ListSaved(ctx, userID, "", "", 20)
	if err != nil {
		t.Fatalf("저장 목록: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("%d건, 기대 3건", len(items))
	}
	if items[0].Name != "카페2" {
		t.Errorf("첫 항목 = %q, 기대 카페2 (저장 최신순)", items[0].Name)
	}

	cafes, _, err := svc.ListSaved(ctx, userID, "cafe", "", 20)
	if err != nil {
		t.Fatalf("카테고리 필터: %v", err)
	}
	if len(cafes) != 2 {
		t.Errorf("카페 %d건, 기대 2건", len(cafes))
	}

	cats, total, err := svc.SavedCategories(ctx, userID)
	if err != nil {
		t.Fatalf("카테고리 집계: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, 기대 3", total)
	}
	if len(cats) != 2 {
		t.Fatalf("카테고리 %d종, 기대 2종 (%+v)", len(cats), cats)
	}
	if cats[0].Category != "cafe" || cats[0].Count != 2 {
		t.Errorf("첫 카테고리 = %+v, 기대 cafe(2)", cats[0])
	}
}

func TestRecommendedPrefersVerifiedWithImage(t *testing.T) {
	withImg := seedPlace{name: "사진있음", category: "cafe", lat: 33.50, lng: 126.53,
		status: "allowed", area: "both", sizeLimit: "large", image: true}
	noImg := seedPlace{name: "사진없음", category: "cafe", lat: 33.51, lng: 126.54,
		status: "allowed", area: "both", sizeLimit: "large"}
	unknownStatus := seedPlace{name: "동반불확실", category: "cafe", lat: 33.52, lng: 126.55,
		status: "unknown", area: "both", sizeLimit: "large", image: true}

	svc, _, userID := testSvc(t, noImg, withImg, unknownStatus)

	items, err := svc.Recommended(context.Background(), userID, 10)
	if err != nil {
		t.Fatalf("추천: %v", err)
	}

	got := names(items)
	if contains(got, "동반불확실") {
		t.Error("동반 여부가 불확실한 장소가 추천에 들어갔다")
	}
	if len(got) == 0 || got[0] != "사진있음" {
		t.Errorf("추천 = %v, 첫 항목은 사진있음이어야 한다", got)
	}
}

func names(items []Place) []string {
	out := make([]string, 0, len(items))
	for _, p := range items {
		out = append(out, p.Name)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestMuzzleDangerousOnlyIsNotShownAsRequiredForEveryone(t *testing.T) {
	all := seedPlace{name: "전견종입마개", category: "cafe", lat: 33.50, lng: 126.53,
		status: "allowed", area: "both", sizeLimit: "large",
		muzzle: boolp(true), muzzleDanger: boolp(false)}
	dangerOnly := seedPlace{name: "맹견만입마개", category: "cafe", lat: 33.51, lng: 126.54,
		status: "allowed", area: "both", sizeLimit: "large",
		muzzle: boolp(true), muzzleDanger: boolp(true)}

	svc, pool, userID := testSvc(t, all, dangerOnly)
	ctx := context.Background()

	p, _, err := svc.Get(ctx, userID, placeIDByName(t, pool, "맹견만입마개"))
	if err != nil {
		t.Fatalf("상세: %v", err)
	}
	conds := petConditions(p)
	if contains(conds, "입마개 필수") {
		t.Errorf("맹견 한정인데 모든 개에게 입마개 필수로 표시됐다 (%v)", conds)
	}
	if !contains(conds, "맹견 입마개 필수") {
		t.Errorf("맹견 한정 문구가 없다 (%v)", conds)
	}

	p, _, err = svc.Get(ctx, userID, placeIDByName(t, pool, "전견종입마개"))
	if err != nil {
		t.Fatalf("상세: %v", err)
	}
	if !contains(petConditions(p), "입마개 필수") {
		t.Errorf("전 견종 입마개 필수가 표시되지 않았다 (%v)", petConditions(p))
	}

	yes := true
	items, _, _, err := svc.List(ctx, userID, &Filter{Muzzle: &yes}, "", nil, nil, "", 50)
	if err != nil {
		t.Fatalf("입마개 필터: %v", err)
	}
	got := names(items)
	if contains(got, "맹견만입마개") {
		t.Errorf("맹견 한정 장소가 '입마개 필수' 필터에 걸렸다 (%v)", got)
	}
	if !contains(got, "전견종입마개") {
		t.Errorf("전 견종 입마개 장소가 필터에서 빠졌다 (%v)", got)
	}
}

func TestRecommendedExcludesShop(t *testing.T) {
	shop := seedPlace{name: "아울렛매장", category: "shop", lat: 33.50, lng: 126.53,
		status: "allowed", area: "both", sizeLimit: "large", image: true}
	spot := seedPlace{name: "오름", category: "attraction", lat: 33.51, lng: 126.54,
		status: "allowed", area: "both", sizeLimit: "large", image: true}

	svc, _, userID := testSvc(t, shop, spot)

	items, err := svc.Recommended(context.Background(), userID, 10)
	if err != nil {
		t.Fatalf("추천: %v", err)
	}
	got := names(items)
	if contains(got, "아울렛매장") {
		t.Errorf("쇼핑 매장이 추천에 들어갔다 (%v)", got)
	}
	if !contains(got, "오름") {
		t.Errorf("관광지가 추천에서 빠졌다 (%v)", got)
	}
}
