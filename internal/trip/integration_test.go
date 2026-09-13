package trip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var fixedToday = time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)

func testSvc(t *testing.T) (*Service, *pgxpool.Pool, uuid.UUID) {
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
			t.Fatalf("테스트 DB 초기화: %v", err)
		}
	}

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao','trip-test') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("테스트 사용자 생성: %v", err)
	}

	svc := NewService(NewRepository(pool), nil)
	svc.now = func() time.Time { return fixedToday }
	return svc, pool, userID
}

func seedPlaces(t *testing.T, pool *pgxpool.Pool, names ...string) []int64 {
	t.Helper()
	ctx := context.Background()

	var out []int64
	for i, name := range names {
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO places (name, norm_name, category, geom)
			VALUES ($1, $1, 'cafe',
			        ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)
			RETURNING id`,
			name, 126.50+float64(i)*0.01, 33.50+float64(i)*0.01).Scan(&id)
		if err != nil {
			t.Fatalf("장소 %q 생성: %v", name, err)
		}
		out = append(out, id)
	}
	return out
}

func seedPet(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO pet_profiles (user_id, name, species, size)
		VALUES ($1, $2, 'dog', 'medium') RETURNING id`,
		userID, name).Scan(&id)
	if err != nil {
		t.Fatalf("반려동물 %q 생성: %v", name, err)
	}
	return id
}

func day(offset int) string {
	return fixedToday.AddDate(0, 0, offset).Format("2006-01-02")
}

func mustCreate(t *testing.T, svc *Service, userID uuid.UUID, req *CreateRequest) *Trip {
	t.Helper()
	trip, err := svc.Create(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("여행 생성(%s): %v", req.Title, err)
	}
	return trip
}

func codeOf(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func TestCreateBuildsEveryDayIncludingEmpty(t *testing.T) {
	svc, pool, userID := testSvc(t)
	petID := seedPet(t, pool, userID, "보리")

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주 3박 4일", StartDate: day(7), EndDate: day(10),
		PetIDs: []string{petID.String()}, Themes: []string{"nature", "cafe"},
	})

	if len(trip.Days) != 4 {
		t.Fatalf("Day %d개, 기대 4개 (3박 4일)", len(trip.Days))
	}
	for i, d := range trip.Days {
		if d.DayNo != i+1 {
			t.Errorf("%d번째 dayNo = %d", i, d.DayNo)
		}
		if len(d.Stops) != 0 {
			t.Errorf("Day %d 에 일정이 있다", d.DayNo)
		}
	}

	if len(trip.Pets) != 1 || trip.Pets[0].Name != "보리" {
		t.Errorf("동반 반려동물 = %+v, 기대 보리", trip.Pets)
	}

	if d := trip.DDay(fixedToday); d == nil || *d != -7 {
		t.Errorf("dDay = %v, 기대 -7", d)
	}
}

func TestCreateSameDayTripIsOneDay(t *testing.T) {
	svc, _, userID := testSvc(t)

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "당일치기", StartDate: day(3), EndDate: day(3),
	})
	if len(trip.Days) != 1 {
		t.Errorf("Day %d개, 기대 1개 (당일치기)", len(trip.Days))
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		req      CreateRequest
		wantCode string
	}{
		{"제목 없음", CreateRequest{Title: "  ", StartDate: day(1), EndDate: day(2)},
			"invalid_trip_title"},
		{"제목 21자", CreateRequest{Title: strings.Repeat("가", 21),
			StartDate: day(1), EndDate: day(2)}, "invalid_trip_title"},
		{"종료일이 앞", CreateRequest{Title: "거꾸로", StartDate: day(5), EndDate: day(2)},
			"invalid_date_range"},
		{"날짜 형식", CreateRequest{Title: "형식", StartDate: "2026/08/16", EndDate: day(2)},
			"invalid_date_range"},
		{"펫 ID 형식", CreateRequest{Title: "펫", StartDate: day(1), EndDate: day(2),
			PetIDs: []string{"not-a-uuid"}}, "invalid_pets"},
		{"남의 펫", CreateRequest{Title: "펫", StartDate: day(1), EndDate: day(2),
			PetIDs: []string{uuid.New().String()}}, "invalid_pets"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, userID, &tc.req)
			if got := codeOf(err); got != tc.wantCode {
				t.Errorf("code = %q (err=%v), 기대 %q", got, err, tc.wantCode)
			}
		})
	}

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: strings.Repeat("가", 20), StartDate: day(1), EndDate: day(2),
	})
	if len(trip.Themes) != 0 {
		t.Errorf("themes = %v, 기대 빈 배열", trip.Themes)
	}
	_ = pool
}

func TestListSplitsUpcomingAndPastAtEndDate(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	mustCreate(t, svc, userID, &CreateRequest{Title: "오늘끝남", StartDate: day(-3), EndDate: day(0)})
	mustCreate(t, svc, userID, &CreateRequest{Title: "어제끝남", StartDate: day(-5), EndDate: day(-1)})
	mustCreate(t, svc, userID, &CreateRequest{Title: "다음주", StartDate: day(7), EndDate: day(9)})

	up, err := svc.List(ctx, userID, "upcoming", 0, 20)
	if err != nil {
		t.Fatalf("다가오는: %v", err)
	}
	upNames := append(summaryNames(up.Items), featuredName(up))
	if !contains(upNames, "오늘끝남") {
		t.Errorf("오늘 끝나는 여행이 다가오는에 없다 (%v)", upNames)
	}
	if contains(upNames, "어제끝남") {
		t.Error("어제 끝난 여행이 다가오는에 있다")
	}

	past, err := svc.List(ctx, userID, "past", 0, 20)
	if err != nil {
		t.Fatalf("지난: %v", err)
	}
	if past.Featured != nil {
		t.Error("지난 탭에는 대표 카드가 없어야 한다")
	}
	pastNames := summaryNames(past.Items)
	if len(pastNames) != 1 || pastNames[0] != "어제끝남" {
		t.Errorf("지난 = %v, 기대 [어제끝남]", pastNames)
	}
}

func TestListFeaturedPrefersInProgressAndIsExcludedFromItems(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	mustCreate(t, svc, userID, &CreateRequest{Title: "진행중", StartDate: day(-1), EndDate: day(2)})
	mustCreate(t, svc, userID, &CreateRequest{Title: "곧출발", StartDate: day(3), EndDate: day(5)})
	mustCreate(t, svc, userID, &CreateRequest{Title: "나중에", StartDate: day(30), EndDate: day(32)})

	res, err := svc.List(ctx, userID, "upcoming", 0, 20)
	if err != nil {
		t.Fatalf("목록: %v", err)
	}

	if res.Featured == nil || *res.Featured.Title != "진행중" {
		t.Fatalf("대표 = %v, 기대 진행중", featuredName(res))
	}
	items := summaryNames(res.Items)
	if contains(items, "진행중") {
		t.Errorf("대표 여행이 목록에도 나왔다 (%v)", items)
	}
	if len(items) != 2 {
		t.Errorf("목록 %d건, 기대 2건 (%v)", len(items), items)
	}

	if items[0] != "곧출발" {
		t.Errorf("첫 항목 = %q, 기대 곧출발", items[0])
	}

	svc.now = func() time.Time { return fixedToday.AddDate(0, 0, 10) }
	res, err = svc.List(ctx, userID, "upcoming", 0, 20)
	if err != nil {
		t.Fatalf("10일 뒤 목록: %v", err)
	}
	if res.Featured == nil || *res.Featured.Title != "나중에" {
		t.Errorf("10일 뒤 대표 = %v, 기대 나중에", featuredName(res))
	}
}

func TestListEmptyIsNotError(t *testing.T) {
	svc, _, userID := testSvc(t)

	res, err := svc.List(context.Background(), userID, "upcoming", 0, 20)
	if err != nil {
		t.Fatalf("빈 목록: %v", err)
	}
	if res.Featured != nil || len(res.Items) != 0 {
		t.Errorf("결과 = %+v, 기대 비어 있음", res)
	}
}

func TestAddStopsIsIdempotentAndNumbersSeq(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	places := seedPlaces(t, pool, "협재", "금능", "곽지")
	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주", StartDate: day(1), EndDate: day(3),
	})

	got, err := svc.AddStops(ctx, userID, trip.ID, 1, places[:2])
	if err != nil {
		t.Fatalf("일정 담기: %v", err)
	}
	if len(got.Days[0].Stops) != 2 {
		t.Fatalf("Day1 일정 %d개, 기대 2개", len(got.Days[0].Stops))
	}
	if got.Days[0].Stops[0].Seq != 1 || got.Days[0].Stops[1].Seq != 2 {
		t.Errorf("seq = %d,%d, 기대 1,2",
			got.Days[0].Stops[0].Seq, got.Days[0].Stops[1].Seq)
	}

	got, err = svc.AddStops(ctx, userID, trip.ID, 1, places[:2])
	if err != nil {
		t.Fatalf("중복 담기: %v", err)
	}
	if len(got.Days[0].Stops) != 2 {
		t.Errorf("중복 담기 후 %d개, 기대 2개 — 멱등이 아니다", len(got.Days[0].Stops))
	}

	got, err = svc.AddStops(ctx, userID, trip.ID, 1, []int64{places[2]})
	if err != nil {
		t.Fatalf("추가 담기: %v", err)
	}
	stops := got.Days[0].Stops
	if len(stops) != 3 {
		t.Fatalf("일정 %d개, 기대 3개", len(stops))
	}
	if stops[2].Seq <= stops[1].Seq {
		t.Errorf("새 일정 seq = %d, 기존 마지막 %d 보다 커야 한다", stops[2].Seq, stops[1].Seq)
	}

	got, err = svc.AddStops(ctx, userID, trip.ID, 2, []int64{places[0]})
	if err != nil {
		t.Fatalf("다른 일차 담기: %v", err)
	}
	if len(got.Days[1].Stops) != 1 {
		t.Errorf("Day2 일정 %d개, 기대 1개", len(got.Days[1].Stops))
	}
	if got.PlaceCount != 4 {
		t.Errorf("totalPlaceCount = %d, 기대 4", got.PlaceCount)
	}
}

func TestAddStopsRejectsBadDayAndPlace(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	places := seedPlaces(t, pool, "협재")
	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주", StartDate: day(1), EndDate: day(2),
	})

	if _, err := svc.AddStops(ctx, userID, trip.ID, 3, places); !errors.Is(err, ErrDayOutOfRange) {
		t.Errorf("3일차 err = %v, 기대 ErrDayOutOfRange", err)
	}
	if _, err := svc.AddStops(ctx, userID, trip.ID, 1, []int64{999999}); codeOf(err) != "invalid_place" {
		t.Errorf("없는 장소 code = %q, 기대 invalid_place", codeOf(err))
	}
}

func TestDeleteStopAndReorder(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	places := seedPlaces(t, pool, "가", "나", "다")
	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주", StartDate: day(1), EndDate: day(2),
	})
	got, err := svc.AddStops(ctx, userID, trip.ID, 1, places)
	if err != nil {
		t.Fatalf("담기: %v", err)
	}

	reversed := []int64{places[2], places[1], places[0]}
	got, err = svc.Reorder(ctx, userID, trip.ID, 1, reversed)
	if err != nil {
		t.Fatalf("순서 변경: %v", err)
	}
	stops := got.Days[0].Stops
	if len(stops) != 3 {
		t.Fatalf("일정 %d개, 기대 3개", len(stops))
	}
	if stops[0].Name != "다" || stops[2].Name != "가" {
		t.Errorf("순서 = %v, 기대 [다 나 가]", stopNames(stops))
	}
	for i, s := range stops {
		if s.Seq != i+1 {
			t.Errorf("%d번째 seq = %d, 기대 %d", i, s.Seq, i+1)
		}
	}

	if _, err := svc.Reorder(ctx, userID, trip.ID, 1, places[:2]); codeOf(err) != "invalid_reorder" {
		t.Errorf("낡은 목록 code = %q, 기대 invalid_reorder", codeOf(err))
	}

	got, err = svc.DeleteStop(ctx, userID, trip.ID, 1, stops[0].Seq)
	if err != nil {
		t.Fatalf("일정 삭제: %v", err)
	}
	if len(got.Days[0].Stops) != 2 {
		t.Errorf("삭제 후 %d개, 기대 2개", len(got.Days[0].Stops))
	}

	if _, err := svc.DeleteStop(ctx, userID, trip.ID, 1, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 seq err = %v, 기대 ErrNotFound", err)
	}
}

func TestUpdateOnlyChangesSentFields(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()
	petID := seedPet(t, pool, userID, "보리")

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "원래제목", StartDate: day(7), EndDate: day(10),
		PetIDs: []string{petID.String()}, Themes: []string{"nature"},
	})

	newTitle := "바뀐제목"
	got, err := svc.Update(ctx, userID, trip.ID, &UpdateRequest{Title: &newTitle})
	if err != nil {
		t.Fatalf("수정: %v", err)
	}

	if *got.Title != "바뀐제목" {
		t.Errorf("제목 = %q", *got.Title)
	}
	if got.StartDate.Format("2006-01-02") != day(7) {
		t.Errorf("시작일이 바뀜: %v", got.StartDate)
	}
	if len(got.Themes) != 1 || got.Themes[0] != "nature" {
		t.Errorf("테마가 바뀜: %v", got.Themes)
	}
	if len(got.Pets) != 1 {
		t.Errorf("반려동물이 바뀜: %v", got.Pets)
	}
}

func TestUpdateRejectsShrinkThatWouldDropStops(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	places := seedPlaces(t, pool, "협재")
	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주", StartDate: day(1), EndDate: day(4),
	})
	if _, err := svc.AddStops(ctx, userID, trip.ID, 4, places); err != nil {
		t.Fatalf("4일차에 담기: %v", err)
	}

	shorter := day(2)
	_, err := svc.Update(ctx, userID, trip.ID, &UpdateRequest{EndDate: &shorter})
	if !errors.Is(err, ErrStopsOutsideRange) {
		t.Fatalf("err = %v, 기대 ErrStopsOutsideRange", err)
	}

	after, err := svc.Get(ctx, userID, trip.ID)
	if err != nil {
		t.Fatalf("조회: %v", err)
	}
	if _, err := svc.DeleteStop(ctx, userID, trip.ID, 4, after.Days[3].Stops[0].Seq); err != nil {
		t.Fatalf("일정 삭제: %v", err)
	}
	got, err := svc.Update(ctx, userID, trip.ID, &UpdateRequest{EndDate: &shorter})
	if err != nil {
		t.Fatalf("기간 축소: %v", err)
	}
	if len(got.Days) != 2 {
		t.Errorf("Day %d개, 기대 2개", len(got.Days))
	}

	longer := day(6)
	got, err = svc.Update(ctx, userID, trip.ID, &UpdateRequest{EndDate: &longer})
	if err != nil {
		t.Fatalf("기간 연장: %v", err)
	}
	if len(got.Days) != 6 {
		t.Errorf("Day %d개, 기대 6개", len(got.Days))
	}
}

func TestUpdateRejectsInvertedRange(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주", StartDate: day(5), EndDate: day(8),
	})

	late := day(10)
	if _, err := svc.Update(ctx, userID, trip.ID,
		&UpdateRequest{StartDate: &late}); codeOf(err) != "invalid_date_range" {
		t.Fatalf("code = %q, 기대 invalid_date_range", codeOf(err))
	}
}

func TestDuplicateCopiesEverything(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	places := seedPlaces(t, pool, "협재", "금능")
	petID := seedPet(t, pool, userID, "보리")

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주 여행", StartDate: day(7), EndDate: day(9),
		PetIDs: []string{petID.String()}, Themes: []string{"nature"},
	})
	if _, err := svc.AddStops(ctx, userID, trip.ID, 1, places); err != nil {
		t.Fatalf("담기: %v", err)
	}

	dup, err := svc.Duplicate(ctx, userID, trip.ID)
	if err != nil {
		t.Fatalf("복제: %v", err)
	}

	if dup.ID == trip.ID {
		t.Fatal("복제본이 원본과 같은 ID 다")
	}
	if *dup.Title != "제주 여행"+DuplicateSuffix {
		t.Errorf("제목 = %q, 기대 %q", *dup.Title, "제주 여행"+DuplicateSuffix)
	}
	if dup.PlaceCount != 2 {
		t.Errorf("일정 %d개, 기대 2개 — 복제 범위는 전체다", dup.PlaceCount)
	}
	if len(dup.Days) != 3 {
		t.Errorf("Day %d개, 기대 3개", len(dup.Days))
	}
	if len(dup.Pets) != 1 || dup.Pets[0].Name != "보리" {
		t.Errorf("반려동물 = %+v, 기대 보리", dup.Pets)
	}

	src, err := svc.Get(ctx, userID, trip.ID)
	if err != nil {
		t.Fatalf("원본 조회: %v", err)
	}
	if *src.Title != "제주 여행" || src.PlaceCount != 2 {
		t.Errorf("원본이 바뀜: %q, %d개", *src.Title, src.PlaceCount)
	}
}

func TestDuplicateTruncatesLongTitle(t *testing.T) {
	svc, _, userID := testSvc(t)

	long := strings.Repeat("가", 18)
	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: long, StartDate: day(1), EndDate: day(2),
	})

	dup, err := svc.Duplicate(context.Background(), userID, trip.ID)
	if err != nil {
		t.Fatalf("긴 제목 복제: %v", err)
	}
	if n := len([]rune(*dup.Title)); n > MaxTitleLen {
		t.Errorf("복제본 제목 %d자(%q), 최대 %d자", n, *dup.Title, MaxTitleLen)
	}
}

func TestOtherUserCannotTouchTrip(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "내여행", StartDate: day(1), EndDate: day(2),
	})

	var otherID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao','other') RETURNING id`,
	).Scan(&otherID); err != nil {
		t.Fatalf("다른 사용자: %v", err)
	}

	if _, err := svc.Get(ctx, otherID, trip.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("조회 err = %v, 기대 ErrForbidden", err)
	}
	title := "탈취"
	if _, err := svc.Update(ctx, otherID, trip.ID, &UpdateRequest{Title: &title}); !errors.Is(err, ErrForbidden) {
		t.Errorf("수정 err = %v, 기대 ErrForbidden", err)
	}
	if err := svc.Delete(ctx, otherID, trip.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("삭제 err = %v, 기대 ErrForbidden", err)
	}
	if _, err := svc.Duplicate(ctx, otherID, trip.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("복제 err = %v, 기대 ErrForbidden", err)
	}

	res, err := svc.List(ctx, otherID, "upcoming", 0, 20)
	if err != nil {
		t.Fatalf("목록: %v", err)
	}
	if res.Featured != nil || len(res.Items) != 0 {
		t.Error("다른 사용자에게 여행이 보인다")
	}

	if _, err := svc.Get(ctx, userID, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 여행 err = %v, 기대 ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "지울여행", StartDate: day(1), EndDate: day(2),
	})

	if err := svc.Delete(ctx, userID, trip.ID); err != nil {
		t.Fatalf("삭제: %v", err)
	}
	if _, err := svc.Get(ctx, userID, trip.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("삭제 후 err = %v, 기대 ErrNotFound", err)
	}
}

func TestListPaginationCoversEveryTripOnce(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	for i := 0; i < 12; i++ {
		mustCreate(t, svc, userID, &CreateRequest{
			Title:     fmt.Sprintf("여행%02d", i),
			StartDate: day(i + 1), EndDate: day(i + 2),
		})
	}

	seen := map[string]int{}

	res, err := svc.List(ctx, userID, "upcoming", 0, 5)
	if err != nil {
		t.Fatalf("1페이지: %v", err)
	}
	if res.Featured == nil {
		t.Fatal("대표 카드가 없다")
	}
	seen[*res.Featured.Title]++

	const limit = 5
	for offset, page := 0, 0; page < 10; page++ {
		res, err := svc.List(ctx, userID, "upcoming", offset, limit)
		if err != nil {
			t.Fatalf("%d페이지: %v", page, err)
		}
		for _, it := range res.Items {
			seen[*it.Title]++
		}
		if !res.HasMore {
			break
		}
		offset += limit
	}

	if len(seen) != 12 {
		t.Errorf("본 여행 %d개, 기대 12개 — 누락이 있다", len(seen))
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("%q 가 %d번 나왔다", name, n)
		}
	}
}

func summaryNames(items []Trip) []string {
	out := make([]string, 0, len(items))
	for _, t := range items {
		if t.Title != nil {
			out = append(out, *t.Title)
		}
	}
	return out
}

func featuredName(r *ListResult) string {
	if r.Featured == nil || r.Featured.Title == nil {
		return ""
	}
	return *r.Featured.Title
}

func stopNames(stops []Stop) []string {
	out := make([]string, 0, len(stops))
	for _, s := range stops {
		out = append(out, s.Name)
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

func TestCreateStoresAndReturnsOrigin(t *testing.T) {
	svc, _, userID := testSvc(t)
	lat, lng, name := 33.5070, 126.4930, "제주국제공항"

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "제주 1박 2일", StartDate: day(7), EndDate: day(8),
		Origin: &OriginRequest{Lat: &lat, Lng: &lng, Name: &name},
	})

	if trip.Origin == nil {
		t.Fatal("출발지가 저장되지 않았다")
	}
	if trip.Origin.Lat != lat || trip.Origin.Lng != lng {
		t.Errorf("좌표 = %v,%v want %v,%v", trip.Origin.Lat, trip.Origin.Lng, lat, lng)
	}
	if trip.Origin.Name == nil || *trip.Origin.Name != name {
		t.Errorf("이름 = %v, want %q", trip.Origin.Name, name)
	}

	// 안 보내면 그대로 둔다
	updated, err := svc.Update(context.Background(), userID, trip.ID,
		&UpdateRequest{Title: strPtr("이름만 변경")})
	if err != nil {
		t.Fatalf("수정: %v", err)
	}
	if updated.Origin == nil || updated.Origin.Lat != lat {
		t.Errorf("출발지가 사라졌다: %v", updated.Origin)
	}

	// 보내면 바뀐다
	lat2, lng2 := 33.2400, 126.5600
	moved, err := svc.Update(context.Background(), userID, trip.ID,
		&UpdateRequest{Origin: &OriginRequest{Lat: &lat2, Lng: &lng2}})
	if err != nil {
		t.Fatalf("출발지 변경: %v", err)
	}
	if moved.Origin.Lat != lat2 {
		t.Errorf("좌표가 안 바뀜: %v", moved.Origin)
	}
}

func TestCreateWithoutOriginIsNull(t *testing.T) {
	svc, _, userID := testSvc(t)

	trip := mustCreate(t, svc, userID, &CreateRequest{
		Title: "출발지 없음", StartDate: day(7), EndDate: day(7),
	})
	if trip.Origin != nil {
		t.Errorf("출발지 = %v, 안 보냈으면 비어 있어야 한다", trip.Origin)
	}
}

func strPtr(v string) *string { return &v }
