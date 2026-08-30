package trip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func cands(ids ...int64) []Candidate {
	out := make([]Candidate, 0, len(ids))
	for _, id := range ids {
		out = append(out, Candidate{ID: id, Name: fmt.Sprintf("장소%d", id)})
	}
	return out
}

func at(id int64, category string, lat, lng float64) Candidate {
	return Candidate{ID: id, Name: fmt.Sprintf("장소%d", id), Category: category, Lat: lat, Lng: lng}
}

func TestParsePlanKeepsOnlyUsableStops(t *testing.T) {
	raw := []byte(`{"days":[
		{"dayNo":1,"placeIds":[1,999,2,2,3,4,5]},
		{"dayNo":2,"placeIds":[3,6]},
		{"dayNo":9,"placeIds":[7]}
	]}`)

	days := parsePlan(raw, cands(1, 2, 3, 4, 5, 6, 7), 2)

	want := [][]int64{{1, 2, 3, 4}, {6}}
	if fmt.Sprint(days) != fmt.Sprint(want) {
		t.Fatalf("days = %v, want %v", days, want)
	}
}

func TestParsePlanRejectsUnusableResponse(t *testing.T) {
	for name, raw := range map[string]string{
		"잘못된 JSON":   `{"days":`,
		"없는 placeId": `{"days":[{"dayNo":1,"placeIds":[404]}]}`,
		"빈 응답":       `{"days":[]}`,
	} {
		if days := parsePlan([]byte(raw), cands(1, 2), 2); days != nil {
			t.Errorf("%s: days = %v, 폴백해야 한다", name, days)
		}
	}
}

func TestFallbackDaysGroupsEachDayGeographically(t *testing.T) {
	// 동쪽 4곳 · 서쪽 4곳(약 65km 떨어짐). 랭킹 순서는 동서가 번갈아 나오게 섞어 둔다.
	cs := []Candidate{
		at(1, "attraction", 33.45, 126.90),
		at(2, "cafe", 33.25, 126.20),
		at(3, "restaurant", 33.46, 126.91),
		at(4, "culture", 33.24, 126.21),
		at(5, "cafe", 33.44, 126.92),
		at(6, "restaurant", 33.26, 126.19),
		at(7, "culture", 33.45, 126.89),
		at(8, "attraction", 33.25, 126.22),
	}

	days := fallbackDays(cs, 2)

	east := map[int64]bool{1: true, 3: true, 5: true, 7: true}
	for d, day := range days {
		if len(day) != stopsPerDay {
			t.Fatalf("%d일차 일정 %d개", d+1, len(day))
		}
		for _, id := range day {
			if east[id] != east[day[0]] {
				t.Errorf("%d일차 %v: 동쪽과 서쪽이 한 날에 섞였다", d+1, day)
				break
			}
		}
	}
}

func TestFallbackDaysAvoidsRepeatingCategoryWhenClose(t *testing.T) {
	// 같은 자리에 카페 2곳과 식당 1곳. 하루 3곳이면 카테고리가 갈려야 한다.
	cs := []Candidate{
		at(1, "cafe", 33.50, 126.50),
		at(2, "cafe", 33.50, 126.50),
		at(3, "restaurant", 33.50, 126.50),
	}

	day := fallbackDays(cs, 1)[0]
	if len(day) != 3 {
		t.Fatalf("일정 %d개", len(day))
	}
	if day[1] != 3 {
		t.Errorf("두 번째로 %d 를 골랐다. 거리가 같으면 다른 카테고리를 먼저 골라야 한다", day[1])
	}
}

func TestFallbackDaysStopsWhenCandidatesRunOut(t *testing.T) {
	days := fallbackDays(cands(1, 2), 3)

	got := 0
	for _, d := range days {
		got += len(d)
	}
	if got != 2 {
		t.Errorf("담긴 일정 %d개, 후보가 2개뿐이므로 2개여야 한다", got)
	}
}

func TestPetSizeTakesLargest(t *testing.T) {
	for _, tc := range []struct {
		pets []Pet
		want string
	}{
		{nil, "small"},
		{[]Pet{{Size: "medium"}, {Size: "large"}, {Size: "small"}}, "large"},
		{[]Pet{{Size: "small"}}, "small"},
	} {
		if got := petSize(tc.pets); got != tc.want {
			t.Errorf("petSize(%v) = %q, want %q", tc.pets, got, tc.want)
		}
	}
}

func seedCandidates(t *testing.T, pool *pgxpool.Pool, categories ...string) {
	t.Helper()
	ctx := context.Background()

	for i, category := range categories {
		var docID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO source_documents (source, source_ref, payload, source_dated_at)
			VALUES ('kcisa_csv', $1, '{}'::jsonb, CURRENT_DATE)
			RETURNING id`, fmt.Sprintf("gen-doc-%d", i)).Scan(&docID); err != nil {
			t.Fatalf("source_documents: %v", err)
		}

		var placeID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO places (name, norm_name, category, geom, road_address)
			VALUES ($1, $1, $2::place_category,
			        ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5)
			RETURNING id`,
			fmt.Sprintf("후보%d", i), category,
			126.50+float64(i)*0.01, 33.50+float64(i)*0.01,
			"제주시 테스트로 "+fmt.Sprint(i)).Scan(&placeID); err != nil {
			t.Fatalf("places: %v", err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO pet_policies (place_id, document_id, status, area, size_limit,
				evidence_text, method, confidence, needs_review)
			VALUES ($1, $2, 'allowed', 'outdoor', 'large',
				'테스트 근거', 'csv_column', 0.90, false)`,
			placeID, docID); err != nil {
			t.Fatalf("pet_policies: %v", err)
		}
	}

	if _, err := pool.Exec(ctx, `REFRESH MATERIALIZED VIEW place_view`); err != nil {
		t.Fatalf("place_view 갱신: %v", err)
	}
}

func TestGenerateFillsEveryDayFromCandidates(t *testing.T) {
	svc, pool, userID := testSvc(t)
	petID := seedPet(t, pool, userID, "보리")
	seedCandidates(t, pool,
		"attraction", "attraction", "attraction",
		"cafe", "cafe", "restaurant", "restaurant", "culture")

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주 1박 2일", StartDate: day(7), EndDate: day(8),
		PetIDs: []string{petID.String()}, Themes: []string{"nature"},
	})

	out, err := svc.Generate(context.Background(), userID, trip.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(out.Days) != 2 {
		t.Fatalf("일차 %d개", len(out.Days))
	}
	if n := len(out.Days[0].Stops); n != stopsPerDay {
		t.Errorf("1일차 일정 %d개, want %d", n, stopsPerDay)
	}
	if len(out.Days[1].Stops) == 0 {
		t.Error("2일차가 비어 있다")
	}

	seen := make(map[int64]bool)
	for _, d := range out.Days {
		for _, s := range d.Stops {
			if seen[s.PlaceID] {
				t.Errorf("장소 %d 가 중복으로 담겼다", s.PlaceID)
			}
			seen[s.PlaceID] = true
		}
	}

	var by []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT generated_by FROM routes WHERE id = $1`, trip.ID).Scan(&by); err != nil {
		t.Fatalf("generated_by 조회: %v", err)
	}
	var meta map[string]string
	if err := json.Unmarshal(by, &meta); err != nil {
		t.Fatalf("generated_by 파싱: %v", err)
	}
	if meta["source"] != "ranking" {
		t.Errorf("source = %q, AI 키 없이 돌았으므로 ranking 이어야 한다", meta["source"])
	}
}

func TestGenerateRejectsTripThatAlreadyHasStops(t *testing.T) {
	svc, pool, userID := testSvc(t)
	seedCandidates(t, pool, "attraction", "cafe")
	placeIDs := seedPlaces(t, pool, "직접 담은 곳")

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "당일치기", StartDate: day(7), EndDate: day(7),
	})
	if _, err := svc.AddStops(context.Background(), userID, trip.ID, 1, placeIDs); err != nil {
		t.Fatalf("AddStops: %v", err)
	}

	_, err := svc.Generate(context.Background(), userID, trip.ID)
	if !errors.Is(err, ErrTripNotEmpty) {
		t.Fatalf("err = %v, want ErrTripNotEmpty", err)
	}
}

func TestGenerateRejectsOtherUsersTrip(t *testing.T) {
	svc, pool, userID := testSvc(t)
	seedCandidates(t, pool, "attraction")

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "남의 여행", StartDate: day(7), EndDate: day(7),
	})

	_, err := svc.Generate(context.Background(), uuid.New(), trip.ID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}
