package image

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

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
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao','image-test') RETURNING id`,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("테스트 사용자 생성: %v", err)
	}

	return NewService(NewRepository(pool)), pool, userID
}

func jpeg() []byte {
	head := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0}
	return append(head, bytes.Repeat([]byte{0x20}, 128)...)
}

func png() []byte {
	head := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	return append(head, bytes.Repeat([]byte{0x00}, 128)...)
}

func webp() []byte {
	b := []byte("RIFF____WEBPVP8 ")
	return append(b, bytes.Repeat([]byte{0x00}, 128)...)
}

func TestUploadDetectsTypeFromBytes(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", jpeg(), "image/jpeg"},
		{"png", png(), "image/png"},
		{"webp", webp(), "image/webp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := svc.Upload(ctx, userID, tc.data)
			if err != nil {
				t.Fatalf("업로드: %v", err)
			}
			img, err := svc.Get(ctx, id)
			if err != nil {
				t.Fatalf("조회: %v", err)
			}
			if img.ContentType != tc.want {
				t.Errorf("contentType = %q, 기대 %q", img.ContentType, tc.want)
			}
			if !bytes.Equal(img.Data, tc.data) {
				t.Error("저장된 바이트가 올린 것과 다르다")
			}
			if img.ByteSize != len(tc.data) {
				t.Errorf("byteSize = %d, 기대 %d", img.ByteSize, len(tc.data))
			}
		})
	}
}

func TestUploadRejectsNonImage(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"HTML", []byte("<html><script>alert(1)</script></html>")},
		{"SVG", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`)},
		{"평문", []byte("그냥 텍스트입니다")},
		{"GIF", append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 64)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Upload(ctx, userID, tc.data); !errors.Is(err, ErrUnsupported) {
				t.Errorf("err = %v, 기대 ErrUnsupported", err)
			}
		})
	}
}

// 클라이언트가 Content-Type 을 뭐라고 주장하든 서버는 바이트만 본다.
// 이걸 신뢰하면 script 를 image/jpeg 로 올려 그대로 되돌려받을 수 있다.
func TestUploadIgnoresClaimedContentType(t *testing.T) {
	svc, _, userID := testSvc(t)

	html := []byte("<html><body>not an image</body></html>")
	if _, err := svc.Upload(context.Background(), userID, html); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, 기대 ErrUnsupported — 바이트를 보지 않고 통과시켰다", err)
	}
}

func TestUploadRejectsEmptyAndOversize(t *testing.T) {
	svc, _, userID := testSvc(t)
	ctx := context.Background()

	if _, err := svc.Upload(ctx, userID, nil); !errors.Is(err, ErrEmpty) {
		t.Errorf("빈 본문 err = %v, 기대 ErrEmpty", err)
	}

	big := append(jpeg(), bytes.Repeat([]byte{0x20}, MaxBytes)...)
	if _, err := svc.Upload(ctx, userID, big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("초과 err = %v, 기대 ErrTooLarge", err)
	}
}

func TestGetMissing(t *testing.T) {
	svc, _, _ := testSvc(t)

	if _, err := svc.Get(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, 기대 ErrNotFound", err)
	}
}

func TestImagesVanishWithUser(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	id, err := svc.Upload(ctx, userID, jpeg())
	if err != nil {
		t.Fatalf("업로드: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("사용자 삭제: %v", err)
	}

	if _, err := svc.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, 기대 ErrNotFound — 탈퇴해도 이미지가 남는다", err)
	}
}

func TestOwnedBy(t *testing.T) {
	svc, pool, userID := testSvc(t)
	ctx := context.Background()

	id, err := svc.Upload(ctx, userID, jpeg())
	if err != nil {
		t.Fatalf("업로드: %v", err)
	}

	var otherID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (provider, provider_sub) VALUES ('kakao','other') RETURNING id`,
	).Scan(&otherID); err != nil {
		t.Fatalf("다른 사용자: %v", err)
	}

	if ok, _ := svc.OwnedBy(ctx, id, userID); !ok {
		t.Error("소유자인데 false")
	}
	if ok, _ := svc.OwnedBy(ctx, id, otherID); ok {
		t.Error("남의 이미지인데 true")
	}
}

func TestURLForRoundTrips(t *testing.T) {
	id := uuid.New()
	u := URLFor(id)

	if !IsManagedURL(u) {
		t.Errorf("URLFor 가 만든 %q 를 IsManagedURL 이 거부한다", u)
	}
	for _, bad := range []string{
		"https://cdn.example.com/a.jpg",
		"/api/images/not-a-uuid",
		"/api/image/" + id.String(),
		"",
	} {
		if IsManagedURL(bad) {
			t.Errorf("%q 를 통과시켰다", bad)
		}
	}
}
