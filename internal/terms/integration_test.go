package terms

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testRepo(t *testing.T) (*Repository, *pgxpool.Pool) {
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

	return NewRepository(pool), pool
}

func newUser(t *testing.T, pool *pgxpool.Pool, sub string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao', $1) RETURNING id`,
		sub).Scan(&id)
	if err != nil {
		t.Fatalf("테스트 사용자 생성: %v", err)
	}
	return id
}

func TestListLatestReturnsScreenOrder(t *testing.T) {
	repo, _ := testRepo(t)

	items, err := repo.ListLatest(context.Background())
	if err != nil {
		t.Fatalf("약관 목록: %v", err)
	}

	want := []string{"service", "privacy", "location", "marketing"}
	if len(items) != len(want) {
		t.Fatalf("약관 %d건, 기대 %d건", len(items), len(want))
	}
	for i, code := range want {
		if items[i].Code != code {
			t.Errorf("%d번째 = %q, 기대 %q (화면 노출 순서)", i, items[i].Code, code)
		}
	}

	for _, it := range items {
		wantRequired := it.Code != "marketing"
		if it.Required != wantRequired {
			t.Errorf("%s: required = %v, 기대 %v", it.Code, it.Required, wantRequired)
		}
	}
}

func TestListLatestPicksByEffectiveFromNotVersionString(t *testing.T) {
	repo, pool := testRepo(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO terms (code, version, required, title, content_url, effective_from)
		VALUES ('service', '1.10', true, '(필수) 서비스 이용약관 개정판',
		        'https://trippaw.app/terms/service-1.10', now() + interval '1 day')`)
	if err != nil {
		t.Fatalf("개정 약관 삽입: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM terms WHERE version = '1.10'`)
	})

	items, err := repo.ListLatest(ctx)
	if err != nil {
		t.Fatalf("약관 목록: %v", err)
	}

	for _, it := range items {
		if it.Code == "service" {
			if it.Version != "1.10" {
				t.Errorf("service 버전 = %q, 기대 %q — effective_from 이 아니라 "+
					"version 문자열로 정렬하면 '1.9' 가 뽑힌다", it.Version, "1.10")
			}
			return
		}
	}
	t.Fatal("service 약관이 목록에 없음")
}

func TestAgreeRejectsWhenRequiredMissing(t *testing.T) {
	repo, pool := testRepo(t)
	ctx := context.Background()
	svc := NewService(repo)
	userID := newUser(t, pool, "terms-missing")

	items, err := repo.ListLatest(ctx)
	if err != nil {
		t.Fatalf("약관 목록: %v", err)
	}

	var marketing Term
	for _, it := range items {
		if it.Code == "marketing" {
			marketing = it
		}
	}

	err = svc.Agree(ctx, userID, []Agreement{{TermID: marketing.ID, Agreed: true}})
	if !errors.Is(err, ErrRequiredNotAgreed) {
		t.Fatalf("err = %v, 기대 ErrRequiredNotAgreed", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_term_agreements WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("동의 건수 조회: %v", err)
	}
	if n != 0 {
		t.Errorf("저장된 동의 %d건, 기대 0건 — 검증 실패인데 일부가 저장됐다", n)
	}
}

func TestAgreeRejectsWhenRequiredDeclined(t *testing.T) {
	repo, pool := testRepo(t)
	ctx := context.Background()
	svc := NewService(repo)
	userID := newUser(t, pool, "terms-declined")

	items, err := repo.ListLatest(ctx)
	if err != nil {
		t.Fatalf("약관 목록: %v", err)
	}

	var reqs []Agreement
	for _, it := range items {
		reqs = append(reqs, Agreement{TermID: it.ID, Agreed: it.Code != "location"})
	}

	if err := svc.Agree(ctx, userID, reqs); !errors.Is(err, ErrRequiredNotAgreed) {
		t.Fatalf("err = %v, 기대 ErrRequiredNotAgreed", err)
	}
}

func TestAgreeStoresAndAllowsWithdrawal(t *testing.T) {
	repo, pool := testRepo(t)
	ctx := context.Background()
	svc := NewService(repo)
	userID := newUser(t, pool, "terms-ok")

	items, err := repo.ListLatest(ctx)
	if err != nil {
		t.Fatalf("약관 목록: %v", err)
	}

	var reqs []Agreement
	for _, it := range items {
		reqs = append(reqs, Agreement{TermID: it.ID, Agreed: true})
	}
	if err := svc.Agree(ctx, userID, reqs); err != nil {
		t.Fatalf("전체 동의: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_term_agreements WHERE user_id = $1 AND agreed`,
		userID).Scan(&n); err != nil {
		t.Fatalf("동의 건수: %v", err)
	}
	if n != len(items) {
		t.Fatalf("동의 %d건, 기대 %d건", n, len(items))
	}

	var marketingID int
	for _, it := range items {
		if it.Code == "marketing" {
			marketingID = it.ID
		}
	}
	var before, after string
	if err := pool.QueryRow(ctx,
		`SELECT agreed_at::text FROM user_term_agreements WHERE user_id=$1 AND term_id=$2`,
		userID, marketingID).Scan(&before); err != nil {
		t.Fatalf("철회 전 시각: %v", err)
	}

	withdrawn := make([]Agreement, len(reqs))
	copy(withdrawn, reqs)
	for i := range withdrawn {
		if withdrawn[i].TermID == marketingID {
			withdrawn[i].Agreed = false
		}
	}
	if err := svc.Agree(ctx, userID, withdrawn); err != nil {
		t.Fatalf("마케팅 철회: %v", err)
	}

	var agreed bool
	if err := pool.QueryRow(ctx,
		`SELECT agreed, agreed_at::text FROM user_term_agreements WHERE user_id=$1 AND term_id=$2`,
		userID, marketingID).Scan(&agreed, &after); err != nil {
		t.Fatalf("철회 후 조회: %v", err)
	}
	if agreed {
		t.Error("마케팅 동의가 여전히 true")
	}
	if before == after {
		t.Error("agreed_at 이 갱신되지 않음 — 철회 시점이 기록되지 않는다")
	}

	var total int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_term_agreements WHERE user_id = $1`, userID).Scan(&total); err != nil {
		t.Fatalf("전체 건수: %v", err)
	}
	if total != len(items) {
		t.Errorf("동의 행 %d개, 기대 %d개 — upsert 가 아니라 중복 삽입되고 있다", total, len(items))
	}
}
