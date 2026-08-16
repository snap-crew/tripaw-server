package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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

func strptr(s string) *string { return &s }

func TestUpsertCreatesThenReturnsSameUser(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	first, err := repo.UpsertOnLogin(ctx, &User{
		Provider:    ProviderApple,
		ProviderSub: "apple-sub-1",
		Email:       strptr("first@ex.com"),
	})
	if err != nil {
		t.Fatalf("첫 로그인: %v", err)
	}
	if first.ID == uuid.Nil {
		t.Fatal("ID 가 생성되지 않음")
	}
	if first.LastLoginAt == nil {
		t.Error("last_login_at 이 비어 있음")
	}

	second, err := repo.UpsertOnLogin(ctx, &User{
		Provider:    ProviderApple,
		ProviderSub: "apple-sub-1",
	})
	if err != nil {
		t.Fatalf("두 번째 로그인: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("같은 계정인데 사용자가 새로 생성됨: %v → %v", first.ID, second.ID)
	}
}

func TestUpsertKeepsEmailWhenProviderOmitsIt(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	if _, err := repo.UpsertOnLogin(ctx, &User{
		Provider:    ProviderApple,
		ProviderSub: "apple-sub-2",
		Email:       strptr("kept@ex.com"),
	}); err != nil {
		t.Fatalf("첫 로그인: %v", err)
	}

	again, err := repo.UpsertOnLogin(ctx, &User{
		Provider:    ProviderApple,
		ProviderSub: "apple-sub-2",
	})
	if err != nil {
		t.Fatalf("재로그인: %v", err)
	}

	if again.Email == nil {
		t.Fatal("처음 받아둔 이메일이 지워짐")
	}
	if *again.Email != "kept@ex.com" {
		t.Errorf("Email = %q, want kept@ex.com", *again.Email)
	}
}

func TestUpsertUpdatesChangedProfile(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	if _, err := repo.UpsertOnLogin(ctx, &User{
		Provider:    ProviderKakao,
		ProviderSub: "kakao-1",
		Nickname:    strptr("이전닉"),
	}); err != nil {
		t.Fatalf("첫 로그인: %v", err)
	}

	updated, err := repo.UpsertOnLogin(ctx, &User{
		Provider:     ProviderKakao,
		ProviderSub:  "kakao-1",
		Nickname:     strptr("새닉"),
		ProfileImage: strptr("https://img/new.jpg"),
	})
	if err != nil {
		t.Fatalf("재로그인: %v", err)
	}

	if updated.Nickname == nil || *updated.Nickname != "새닉" {
		t.Errorf("Nickname = %v, want 새닉", updated.Nickname)
	}
	if updated.ProfileImage == nil || *updated.ProfileImage != "https://img/new.jpg" {
		t.Errorf("ProfileImage = %v", updated.ProfileImage)
	}
}

func TestUpsertAllowsSameEmailAcrossProviders(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	appleUser, err := repo.UpsertOnLogin(ctx, &User{
		Provider: ProviderApple, ProviderSub: "a-1", Email: strptr("dup@ex.com"),
	})
	if err != nil {
		t.Fatalf("애플 로그인: %v", err)
	}

	kakaoUser, err := repo.UpsertOnLogin(ctx, &User{
		Provider: ProviderKakao, ProviderSub: "k-1", Email: strptr("dup@ex.com"),
	})
	if err != nil {
		t.Fatalf("카카오 로그인: %v", err)
	}

	if appleUser.ID == kakaoUser.ID {
		t.Error("공급자가 다른데 같은 사용자로 합쳐짐")
	}
}

func TestUpsertAllowsMultipleUsersWithoutEmail(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	for _, sub := range []string{"hidden-1", "hidden-2", "hidden-3"} {
		if _, err := repo.UpsertOnLogin(ctx, &User{
			Provider: ProviderApple, ProviderSub: sub,
		}); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
	}
}

func TestFindUserByID(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	created, err := repo.UpsertOnLogin(ctx, &User{
		Provider: ProviderKakao, ProviderSub: "find-me", Nickname: strptr("테스트유저"),
	})
	if err != nil {
		t.Fatalf("생성: %v", err)
	}

	found, err := repo.FindUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("조회: %v", err)
	}
	if found.ProviderSub != "find-me" {
		t.Errorf("ProviderSub = %q", found.ProviderSub)
	}
	if found.Provider != ProviderKakao {
		t.Errorf("Provider = %q", found.Provider)
	}

	if _, err := repo.FindUserByID(ctx, uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("없는 사용자 조회 err = %v, want ErrUserNotFound", err)
	}
}

func TestConsumeRefreshTokenIsSingleUse(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	user, err := repo.UpsertOnLogin(ctx, &User{Provider: ProviderApple, ProviderSub: "rt-1"})
	if err != nil {
		t.Fatalf("사용자 생성: %v", err)
	}

	const hash = "hash-aaa"
	if err := repo.StoreRefreshToken(ctx, user.ID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("토큰 저장: %v", err)
	}

	got, err := repo.ConsumeRefreshToken(ctx, hash)
	if err != nil {
		t.Fatalf("첫 사용: %v", err)
	}
	if got != user.ID {
		t.Errorf("userID = %v, want %v", got, user.ID)
	}

	if _, err := repo.ConsumeRefreshToken(ctx, hash); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("같은 토큰이 두 번 사용됨: err = %v", err)
	}
}

func TestConsumeRefreshTokenRejectsExpired(t *testing.T) {
	repo, _ := testRepo(t)
	ctx := context.Background()

	user, err := repo.UpsertOnLogin(ctx, &User{Provider: ProviderApple, ProviderSub: "rt-2"})
	if err != nil {
		t.Fatalf("사용자 생성: %v", err)
	}

	const hash = "hash-expired"
	if err := repo.StoreRefreshToken(ctx, user.ID, hash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("토큰 저장: %v", err)
	}

	if _, err := repo.ConsumeRefreshToken(ctx, hash); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("만료 토큰이 사용됨: err = %v", err)
	}
}

func TestConsumeRefreshTokenRejectsUnknown(t *testing.T) {
	repo, _ := testRepo(t)

	if _, err := repo.ConsumeRefreshToken(context.Background(), "없는-해시"); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("err = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestDeleteRefreshTokensByUser(t *testing.T) {
	repo, pool := testRepo(t)
	ctx := context.Background()

	user, err := repo.UpsertOnLogin(ctx, &User{Provider: ProviderKakao, ProviderSub: "multi-device"})
	if err != nil {
		t.Fatalf("사용자 생성: %v", err)
	}

	for _, h := range []string{"d1", "d2", "d3"} {
		if err := repo.StoreRefreshToken(ctx, user.ID, h, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("토큰 저장: %v", err)
		}
	}

	if err := repo.DeleteRefreshTokensByUser(ctx, user.ID); err != nil {
		t.Fatalf("일괄 삭제: %v", err)
	}

	if n := countTokens(t, pool, user.ID); n != 0 {
		t.Errorf("남은 토큰 = %d, want 0", n)
	}
}

func TestDeleteUserCascadesTokens(t *testing.T) {
	repo, pool := testRepo(t)
	ctx := context.Background()

	user, err := repo.UpsertOnLogin(ctx, &User{Provider: ProviderApple, ProviderSub: "to-delete"})
	if err != nil {
		t.Fatalf("사용자 생성: %v", err)
	}
	if err := repo.StoreRefreshToken(ctx, user.ID, "cascade-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("토큰 저장: %v", err)
	}

	if err := repo.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("사용자 삭제: %v", err)
	}

	if n := countTokens(t, pool, user.ID); n != 0 {
		t.Errorf("사용자를 지웠는데 토큰이 %d개 남음", n)
	}
	if _, err := repo.FindUserByID(ctx, user.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("삭제된 사용자가 조회됨: err = %v", err)
	}
	if err := repo.DeleteUser(ctx, user.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("없는 사용자 삭제 err = %v, want ErrUserNotFound", err)
	}
}

func TestDeleteExpiredRefreshTokens(t *testing.T) {
	repo, pool := testRepo(t)
	ctx := context.Background()

	user, err := repo.UpsertOnLogin(ctx, &User{Provider: ProviderKakao, ProviderSub: "cleanup"})
	if err != nil {
		t.Fatalf("사용자 생성: %v", err)
	}

	if err := repo.StoreRefreshToken(ctx, user.ID, "live", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("토큰 저장: %v", err)
	}
	for _, h := range []string{"dead-1", "dead-2"} {
		if err := repo.StoreRefreshToken(ctx, user.ID, h, time.Now().Add(-time.Hour)); err != nil {
			t.Fatalf("토큰 저장: %v", err)
		}
	}

	n, err := repo.DeleteExpiredRefreshTokens(ctx)
	if err != nil {
		t.Fatalf("정리: %v", err)
	}
	if n != 2 {
		t.Errorf("지운 개수 = %d, want 2", n)
	}
	if got := countTokens(t, pool, user.ID); got != 1 {
		t.Errorf("남은 토큰 = %d, want 1 (살아있는 것만)", got)
	}
}

func countTokens(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()

	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, userID).Scan(&n)
	if err != nil {
		t.Fatalf("토큰 개수 조회: %v", err)
	}
	return n
}
