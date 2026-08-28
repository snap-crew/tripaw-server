package pet

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/daewon/tripaw-server/internal/image"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

	if _, err := pool.Exec(context.Background(), `TRUNCATE users CASCADE`); err != nil {
		t.Fatalf("테스트 DB 초기화: %v", err)
	}

	var userID uuid.UUID
	err = pool.QueryRow(context.Background(),
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao','pet-test') RETURNING id`,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("테스트 사용자 생성: %v", err)
	}

	return NewService(NewRepository(pool)), pool, userID
}

func breedID(t *testing.T, pool *pgxpool.Pool, name string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM breeds WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatalf("품종 %q 조회: %v", name, err)
	}
	return id
}

func validPet(t *testing.T, pool *pgxpool.Pool, name string) *CreateRequest {
	t.Helper()
	id := breedID(t, pool, "보더콜리")
	return &CreateRequest{
		Name: name, Species: SpeciesDog, BreedID: &id,
		Size: "medium", Gender: "male", Neutered: "done",
		Traits: []string{"active", "social"},
	}
}

func f64(v float64) *float64 { return &v }

func codeOf(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func TestListBreedsSortsKorean(t *testing.T) {
	svc, _, _ := testSvc(t)

	items, err := svc.ListBreeds(context.Background(), SpeciesDog, "")
	if err != nil {
		t.Fatalf("품종 목록: %v", err)
	}
	if len(items) != 27 {
		t.Fatalf("강아지 품종 %d건, 기대 27건", len(items))
	}

	want := []string{"골든 리트리버", "뉴펀들랜드", "닥스훈트", "달마시안"}
	for i, name := range want {
		if items[i].Name != name {
			t.Errorf("%d번째 = %q, 기대 %q — COLLATE \"ko-KR-x-icu\" 가 빠졌다",
				i, items[i].Name, name)
		}
	}
}

func TestListBreedsFiltersBySpeciesAndQuery(t *testing.T) {
	svc, _, _ := testSvc(t)
	ctx := context.Background()

	cats, err := svc.ListBreeds(ctx, SpeciesCat, "")
	if err != nil {
		t.Fatalf("고양이 품종: %v", err)
	}
	if len(cats) != 30 {
		t.Errorf("고양이 품종 %d건, 기대 30건", len(cats))
	}

	found, err := svc.ListBreeds(ctx, SpeciesDog, "리트리버")
	if err != nil {
		t.Fatalf("품종 검색: %v", err)
	}
	if len(found) != 1 || found[0].Name != "골든 리트리버" {
		t.Errorf("검색 결과 = %v, 기대 [골든 리트리버]", found)
	}

	none, err := svc.ListBreeds(ctx, SpeciesDog, "존재하지않는품종")
	if err != nil {
		t.Fatalf("빈 검색: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("결과 %d건, 기대 0건", len(none))
	}

	if _, err := svc.ListBreeds(ctx, "", ""); codeOf(err) != "invalid_request" {
		t.Errorf("species 누락 code = %q, 기대 invalid_request", codeOf(err))
	}
}

func TestCreateRejectsInvalidFields(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		mutate   func(*CreateRequest)
		wantCode string
	}{
		{"이름 13자", func(r *CreateRequest) { r.Name = strings.Repeat("가", 13) }, "invalid_pet_name"},
		{"이름 공백만", func(r *CreateRequest) { r.Name = "   " }, "invalid_pet_name"},
		{"이름 이모지", func(r *CreateRequest) { r.Name = "보리🐶" }, "invalid_pet_name"},
		{"이름 특수문자", func(r *CreateRequest) { r.Name = "보리!" }, "invalid_pet_name"},
		{"종 이상값", func(r *CreateRequest) { r.Species = "bird" }, "invalid_species"},
		{"크기 unknown", func(r *CreateRequest) { r.Size = "unknown" }, "invalid_size"},
		{"성별 이상값", func(r *CreateRequest) { r.Gender = "other" }, "invalid_gender"},
		{"중성화 이상값", func(r *CreateRequest) { r.Neutered = "maybe" }, "invalid_neutered"},
		{"성향 4개", func(r *CreateRequest) {
			r.Traits = []string{"active", "calm", "social", "timid"}
		}, "invalid_traits"},
		{"성향 중복", func(r *CreateRequest) { r.Traits = []string{"active", "active"} }, "invalid_traits"},
		{"성향 이상값", func(r *CreateRequest) { r.Traits = []string{"lazy"} }, "invalid_traits"},
		{"품종 누락", func(r *CreateRequest) { r.BreedID = nil }, "invalid_breed"},
		{"없는 품종", func(r *CreateRequest) { id := 99999; r.BreedID = &id }, "invalid_breed"},
		{"사진 상대경로", func(r *CreateRequest) { s := "/pets/a.jpg"; r.PhotoURL = &s }, "invalid_photo_url"},
		{"외부 CDN 주소", func(r *CreateRequest) {
			s := "https://evil.example.com/a.jpg"
			r.PhotoURL = &s
		}, "invalid_photo_url"},
		{"이미지 아닌 경로", func(r *CreateRequest) {
			s := "/api/images/not-a-uuid"
			r.PhotoURL = &s
		}, "invalid_photo_url"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validPet(t, pool, "보리")
			tc.mutate(req)

			_, err := svc.Create(ctx, userID, req)
			if got := codeOf(err); got != tc.wantCode {
				t.Errorf("code = %q (err=%v), 기대 %q", got, err, tc.wantCode)
			}
		})
	}
}

func TestCreateRejectsBreedFromOtherSpecies(t *testing.T) {
	svc, pool, userID := testSvc(t)

	req := validPet(t, pool, "보리")
	catBreed := breedID(t, pool, "페르시안")
	req.BreedID = &catBreed

	_, err := svc.Create(context.Background(), userID, req)
	if codeOf(err) != "invalid_breed" {
		t.Fatalf("code = %q (err=%v), 기대 invalid_breed — "+
			"고양이 품종이 강아지 프로필에 붙었다", codeOf(err), err)
	}
}

func TestCreateTrimsNameAndAcceptsBoundaries(t *testing.T) {
	svc, pool, userID := testSvc(t)

	req := validPet(t, pool, "  보리  ")
	p, err := svc.Create(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("등록: %v", err)
	}
	if p.Name != "보리" {
		t.Errorf("이름 = %q, 기대 %q — 앞뒤 공백이 제거되지 않았다", p.Name, "보리")
	}

	req12 := validPet(t, pool, strings.Repeat("가", 12))
	if _, err := svc.Create(context.Background(), userID, req12); err != nil {
		t.Errorf("12자 이름이 거부됨: %v", err)
	}
}

func TestCreateDeletesDraft(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	if err := svc.SaveDraft(ctx, userID, 1, map[string]any{"name": "보리"}); err != nil {
		t.Fatalf("임시 저장: %v", err)
	}
	if _, _, _, err := svc.FindDraft(ctx, userID); err != nil {
		t.Fatalf("임시 저장 확인: %v", err)
	}

	if _, err := svc.Create(ctx, userID, validPet(t, pool, "보리")); err != nil {
		t.Fatalf("등록: %v", err)
	}

	if _, _, _, err := svc.FindDraft(ctx, userID); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("err = %v, 기대 ErrDraftNotFound — 등록 후에도 draft 가 남아 있다", err)
	}
}

func TestCreateEnforcesMaxPets(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	for i := 0; i < MaxPets; i++ {
		if _, err := svc.Create(ctx, userID, validPet(t, pool, "보리")); err != nil {
			t.Fatalf("%d번째 등록: %v", i+1, err)
		}
	}

	_, err := svc.Create(ctx, userID, validPet(t, pool, "여섯째"))
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("err = %v, 기대 ErrLimitExceeded", err)
	}

	items, err := svc.List(ctx, userID)
	if err != nil {
		t.Fatalf("목록: %v", err)
	}
	if len(items) != MaxPets {
		t.Errorf("등록된 마리 수 = %d, 기대 %d", len(items), MaxPets)
	}
}

func TestListReturnsRegistrationOrderWithBreedName(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	for _, name := range []string{"첫째", "둘째", "셋째"} {
		if _, err := svc.Create(ctx, userID, validPet(t, pool, name)); err != nil {
			t.Fatalf("%s 등록: %v", name, err)
		}
	}

	items, err := svc.List(ctx, userID)
	if err != nil {
		t.Fatalf("목록: %v", err)
	}

	want := []string{"첫째", "둘째", "셋째"}
	for i, name := range want {
		if items[i].Name != name {
			t.Errorf("%d번째 = %q, 기대 %q (등록순)", i, items[i].Name, name)
		}
	}

	if items[0].BreedName == nil || *items[0].BreedName != "보더콜리" {
		t.Errorf("품종명 = %v, 기대 보더콜리", items[0].BreedName)
	}
}

func TestGetAndUpdateRejectOtherUsersPet(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	mine, err := svc.Create(ctx, userID, validPet(t, pool, "보리"))
	if err != nil {
		t.Fatalf("등록: %v", err)
	}

	var otherID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao','other') RETURNING id`,
	).Scan(&otherID); err != nil {
		t.Fatalf("다른 사용자 생성: %v", err)
	}

	if _, err := svc.Get(ctx, otherID, mine.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("조회 err = %v, 기대 ErrForbidden", err)
	}

	name := "탈취"
	if _, err := svc.Update(ctx, otherID, mine.ID, &UpdateRequest{Name: &name}); !errors.Is(err, ErrForbidden) {
		t.Errorf("수정 err = %v, 기대 ErrForbidden", err)
	}

	if err := svc.Delete(ctx, otherID, mine.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("삭제 err = %v, 기대 ErrForbidden", err)
	}

	if _, err := svc.Get(ctx, userID, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 ID err = %v, 기대 ErrNotFound", err)
	}
}

func TestUpdateOnlyChangesSentFields(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, userID, validPet(t, pool, "보리"))
	if err != nil {
		t.Fatalf("등록: %v", err)
	}

	newName := "초코"
	updated, err := svc.Update(ctx, userID, created.ID, &UpdateRequest{Name: &newName})
	if err != nil {
		t.Fatalf("수정: %v", err)
	}

	if updated.Name != "초코" {
		t.Errorf("이름 = %q, 기대 초코", updated.Name)
	}
	if updated.Size != created.Size {
		t.Errorf("크기가 바뀜: %q → %q", created.Size, updated.Size)
	}
	if updated.Gender != created.Gender {
		t.Errorf("성별이 바뀜: %q → %q", created.Gender, updated.Gender)
	}
	if len(updated.Traits) != len(created.Traits) {
		t.Errorf("성향이 바뀜: %v → %v", created.Traits, updated.Traits)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Error("updated_at 이 갱신되지 않음")
	}
}

func TestUpdateClearsTraitsWithEmptyArray(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, userID, validPet(t, pool, "보리"))
	if err != nil {
		t.Fatalf("등록: %v", err)
	}
	if len(created.Traits) == 0 {
		t.Fatal("사전 조건: 성향이 있어야 한다")
	}

	empty := []string{}
	updated, err := svc.Update(ctx, userID, created.ID, &UpdateRequest{Traits: &empty})
	if err != nil {
		t.Fatalf("성향 해제: %v", err)
	}
	if len(updated.Traits) != 0 {
		t.Errorf("성향 = %v, 기대 빈 배열", updated.Traits)
	}
}

func TestUpdateRejectsSpeciesChangeWithoutBreed(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, userID, validPet(t, pool, "보리"))
	if err != nil {
		t.Fatalf("등록: %v", err)
	}

	cat := SpeciesCat
	_, err = svc.Update(ctx, userID, created.ID, &UpdateRequest{Species: &cat})
	if codeOf(err) != "invalid_breed" {
		t.Fatalf("code = %q (err=%v), 기대 invalid_breed", codeOf(err), err)
	}

	persian := breedID(t, pool, "페르시안")
	updated, err := svc.Update(ctx, userID, created.ID,
		&UpdateRequest{Species: &cat, BreedID: &persian})
	if err != nil {
		t.Fatalf("종+품종 동시 수정: %v", err)
	}
	if updated.Species != SpeciesCat || *updated.BreedName != "페르시안" {
		t.Errorf("종=%q 품종=%v, 기대 cat/페르시안", updated.Species, updated.BreedName)
	}
}

func TestSaveDraftAcceptsPartialInputAndUpserts(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	err := svc.SaveDraft(ctx, userID, 1, map[string]any{
		"name": "", "species": nil, "breedId": nil, "traits": []any{},
	})
	if err != nil {
		t.Fatalf("부분 입력 임시 저장: %v", err)
	}

	if err := svc.SaveDraft(ctx, userID, 2, map[string]any{"name": "보리"}); err != nil {
		t.Fatalf("두 번째 임시 저장: %v", err)
	}

	step, payload, _, err := svc.FindDraft(ctx, userID)
	if err != nil {
		t.Fatalf("복원: %v", err)
	}
	if step != 2 {
		t.Errorf("step = %d, 기대 2", step)
	}
	if payload["name"] != "보리" {
		t.Errorf("payload[name] = %v, 기대 보리", payload["name"])
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pet_profile_drafts WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("draft 건수: %v", err)
	}
	if n != 1 {
		t.Errorf("draft 행 %d개, 기대 1개", n)
	}
}

func TestSaveDraftRejectsBadStepAndUnknownKey(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	if err := svc.SaveDraft(ctx, userID, 3, map[string]any{}); codeOf(err) != "invalid_request" {
		t.Errorf("step=3 code = %q, 기대 invalid_request", codeOf(err))
	}

	err := svc.SaveDraft(ctx, userID, 1, map[string]any{"nmae": "오타"})
	if codeOf(err) != "invalid_request" {
		t.Errorf("오타 키 code = %q, 기대 invalid_request", codeOf(err))
	}
}

func TestFindDraftMissing(t *testing.T) {
	svc, _, userID := testSvc(t)

	if _, _, _, err := svc.FindDraft(context.Background(), userID); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("err = %v, 기대 ErrDraftNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, userID, validPet(t, pool, "보리"))
	if err != nil {
		t.Fatalf("등록: %v", err)
	}

	if err := svc.Delete(ctx, userID, created.ID); err != nil {
		t.Fatalf("삭제: %v", err)
	}

	if _, err := svc.Get(ctx, userID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("삭제 후 조회 err = %v, 기대 ErrNotFound", err)
	}
}

func TestUpdatePhotoURLThreeStates(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	req := validPet(t, pool, "보리")
	photo := image.URLFor(uuid.New())
	req.PhotoURL = &photo

	created, err := svc.Create(ctx, userID, req)
	if err != nil {
		t.Fatalf("등록: %v", err)
	}
	if created.PhotoURL == nil || *created.PhotoURL != photo {
		t.Fatalf("등록 직후 photoUrl = %v, 기대 %q", created.PhotoURL, photo)
	}

	decode := func(body string) *UpdateRequest {
		t.Helper()
		var u UpdateRequest
		if err := json.Unmarshal([]byte(body), &u); err != nil {
			t.Fatalf("본문 파싱 %s: %v", body, err)
		}
		return &u
	}

	got, err := svc.Update(ctx, userID, created.ID, decode(`{"name":"초코"}`))
	if err != nil {
		t.Fatalf("이름만 수정: %v", err)
	}
	if got.PhotoURL == nil || *got.PhotoURL != photo {
		t.Errorf("키 없음: photoUrl = %v, 기대 %q (건드리면 안 된다)", got.PhotoURL, photo)
	}

	next := image.URLFor(uuid.New())
	got, err = svc.Update(ctx, userID, created.ID, decode(`{"photoUrl":"`+next+`"}`))
	if err != nil {
		t.Fatalf("사진 교체: %v", err)
	}
	if got.PhotoURL == nil || *got.PhotoURL != next {
		t.Errorf("교체: photoUrl = %v, 기대 %q", got.PhotoURL, next)
	}

	got, err = svc.Update(ctx, userID, created.ID, decode(`{"photoUrl":null}`))
	if err != nil {
		t.Fatalf("사진 삭제: %v", err)
	}
	if got.PhotoURL != nil {
		t.Errorf("삭제: photoUrl = %v, 기대 nil", *got.PhotoURL)
	}
}

func TestCreateStoresWeight(t *testing.T) {
	svc, pool, userID := testSvc(t)

	req := validPet(t, pool, "보리")
	req.WeightKg = f64(12.5)

	p, err := svc.Create(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("등록: %v", err)
	}
	if p.WeightKg == nil || *p.WeightKg != 12.5 {
		t.Fatalf("weightKg = %v, want 12.5", p.WeightKg)
	}

	// 몸무게는 선택값이다. 안 보내도 등록된다.
	req2 := validPet(t, pool, "초코")
	p2, err := svc.Create(context.Background(), userID, req2)
	if err != nil {
		t.Fatalf("몸무게 없이 등록: %v", err)
	}
	if p2.WeightKg != nil {
		t.Errorf("weightKg = %v, 안 보냈으면 비어 있어야 한다", *p2.WeightKg)
	}
}

func TestCreateRejectsWeightOutOfRange(t *testing.T) {
	svc, pool, userID := testSvc(t)

	for name, w := range map[string]float64{
		"0kg":      0,
		"음수":       -1,
		"150kg 초과": 150.1,
		"오타(1200)": 1200,
	} {
		req := validPet(t, pool, "보리")
		req.WeightKg = f64(w)

		_, err := svc.Create(context.Background(), userID, req)
		if codeOf(err) != "invalid_weight" {
			t.Errorf("%s: code = %q, want invalid_weight", name, codeOf(err))
		}
	}
}

func TestUpdateWeightAloneIsValidated(t *testing.T) {
	svc, pool, userID := testSvc(t)

	p, err := svc.Create(context.Background(), userID, validPet(t, pool, "보리"))
	if err != nil {
		t.Fatalf("등록: %v", err)
	}

	// 크기를 함께 보내지 않아도 몸무게 검증이 걸려야 한다.
	_, err = svc.Update(context.Background(), userID, p.ID, &UpdateRequest{WeightKg: f64(900)})
	if codeOf(err) != "invalid_weight" {
		t.Fatalf("code = %q, want invalid_weight", codeOf(err))
	}

	updated, err := svc.Update(context.Background(), userID, p.ID, &UpdateRequest{WeightKg: f64(8.2)})
	if err != nil {
		t.Fatalf("수정: %v", err)
	}
	if updated.WeightKg == nil || *updated.WeightKg != 8.2 {
		t.Fatalf("weightKg = %v, want 8.2", updated.WeightKg)
	}
	if updated.Size != "medium" {
		t.Errorf("size = %q, 몸무게만 보냈으므로 그대로여야 한다", updated.Size)
	}
}

func TestSaveDraftAcceptsWeight(t *testing.T) {
	svc, _, userID := testSvc(t)

	err := svc.SaveDraft(context.Background(), userID, 2,
		map[string]any{"name": "보리", "weightKg": 12.5})
	if err != nil {
		t.Fatalf("몸무게가 담긴 임시 저장이 거절됐다: %v", err)
	}
}
