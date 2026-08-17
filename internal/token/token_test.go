package token

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-secret-at-least-32-bytes-long!!"

func newTestManager() *Manager {
	return NewManager(testSecret, time.Hour, 720*time.Hour)
}

func TestGeneratePairRoundTrip(t *testing.T) {
	m := newTestManager()
	userID := uuid.New()

	pair, err := m.GeneratePair(userID)
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	got, err := m.ValidateAccess(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccess: %v", err)
	}
	if got != userID {
		t.Errorf("액세스 토큰 sub = %v, want %v", got, userID)
	}

	got, err = m.ValidateRefresh(pair.RefreshToken)
	if err != nil {
		t.Fatalf("ValidateRefresh: %v", err)
	}
	if got != userID {
		t.Errorf("리프레시 토큰 sub = %v, want %v", got, userID)
	}

	if pair.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn = %d, want 3600", pair.ExpiresIn)
	}
}

func TestRefreshTokenIsRejectedAsAccessToken(t *testing.T) {
	m := newTestManager()
	pair, err := m.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	if _, err := m.ValidateAccess(pair.RefreshToken); !errors.Is(err, ErrWrongTokenType) {
		t.Fatalf("리프레시 토큰이 액세스 토큰으로 통과함: err = %v", err)
	}
}

func TestAccessTokenIsRejectedAsRefreshToken(t *testing.T) {
	m := newTestManager()
	pair, err := m.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	if _, err := m.ValidateRefresh(pair.AccessToken); !errors.Is(err, ErrWrongTokenType) {
		t.Fatalf("액세스 토큰이 리프레시 토큰으로 통과함: err = %v", err)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	m := NewManager(testSecret, -time.Hour, -time.Hour)
	pair, err := m.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	if _, err := m.ValidateAccess(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("만료 토큰이 통과함: err = %v", err)
	}
}

func TestWrongSecretIsRejected(t *testing.T) {
	pair, err := newTestManager().GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	other := NewManager("완전히-다른-서명키-완전히-다른-서명키", time.Hour, 720*time.Hour)
	if _, err := other.ValidateAccess(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("다른 키로 서명한 토큰이 통과함: err = %v", err)
	}
}

func TestNoneAlgorithmIsRejected(t *testing.T) {
	c := claims{
		Typ: typeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, c).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("서명 없는 토큰 생성: %v", err)
	}

	if _, err := newTestManager().ValidateAccess(unsigned); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("alg=none 토큰이 통과함: err = %v", err)
	}
}

func TestTokenWithoutExpiryIsRejected(t *testing.T) {
	c := claims{
		Typ: typeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:       uuid.NewString(),
			Subject:  uuid.NewString(),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("토큰 생성: %v", err)
	}

	if _, err := newTestManager().ValidateAccess(signed); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("exp 없는 토큰이 통과함: err = %v", err)
	}
}

func TestTokensAreUniquePerCall(t *testing.T) {
	m := newTestManager()
	userID := uuid.New()

	first, err := m.GeneratePair(userID)
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}
	second, err := m.GeneratePair(userID)
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	if first.RefreshToken == second.RefreshToken {
		t.Error("같은 사용자의 연속 로그인에서 리프레시 토큰이 동일함")
	}
	if first.AccessToken == second.AccessToken {
		t.Error("같은 사용자의 연속 로그인에서 액세스 토큰이 동일함")
	}
}

func TestHashRefresh(t *testing.T) {
	h := HashRefresh("some.jwt.token")

	if len(h) != 64 {
		t.Errorf("해시 길이 = %d, want 64 (sha256 hex)", len(h))
	}
	if h == "some.jwt.token" {
		t.Error("원문이 그대로 반환됨")
	}
	if HashRefresh("some.jwt.token") != h {
		t.Error("같은 입력에 다른 해시")
	}
	if HashRefresh("other.jwt.token") == h {
		t.Error("다른 입력에 같은 해시")
	}
}

func TestMalformedTokenIsRejected(t *testing.T) {
	m := newTestManager()
	for _, s := range []string{"", "not-a-jwt", "a.b.c", "Bearer x.y.z"} {
		if _, err := m.ValidateAccess(s); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q 가 통과함: err = %v", s, err)
		}
	}
}
