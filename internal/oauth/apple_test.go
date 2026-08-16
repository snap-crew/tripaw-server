package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("키 생성: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("PKCS8 인코딩: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func newTestAppleClient(t *testing.T) *AppleClient {
	t.Helper()

	c, err := NewAppleClient("com.tripaw.app", "TEAM123456", "KEY1234567", testPrivateKeyPEM(t), "")
	if err != nil {
		t.Fatalf("NewAppleClient: %v", err)
	}
	return c
}

type stubVerifier struct {
	claims jwt.MapClaims
	err    error
}

func (s stubVerifier) VerifyAndParse(context.Context, string) (jwt.MapClaims, error) {
	return s.claims, s.err
}

func TestClientSecretShape(t *testing.T) {
	c := newTestAppleClient(t)

	secret, err := c.clientSecret()
	if err != nil {
		t.Fatalf("clientSecret: %v", err)
	}

	parsed, _, err := jwt.NewParser().ParseUnverified(secret, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("client_secret 파싱: %v", err)
	}

	if got := parsed.Header["kid"]; got != "KEY1234567" {
		t.Errorf("kid = %v, want KEY1234567", got)
	}
	if got := parsed.Header["alg"]; got != "ES256" {
		t.Errorf("alg = %v, want ES256", got)
	}

	claims := parsed.Claims.(jwt.MapClaims)
	if got, _ := claims["iss"].(string); got != "TEAM123456" {
		t.Errorf("iss = %q, want TEAM123456 (팀 ID)", got)
	}
	if got, _ := claims["sub"].(string); got != "com.tripaw.app" {
		t.Errorf("sub = %q, want com.tripaw.app (클라이언트 ID)", got)
	}
	aud, err := claims.GetAudience()
	if err != nil || len(aud) != 1 || aud[0] != appleIssuer {
		t.Errorf("aud = %v, want [%s]", aud, appleIssuer)
	}
}

func TestExchangeCode(t *testing.T) {
	email := "someone@privaterelay.appleid.com"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("폼 파싱: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.FormValue("code"); got != "auth-code-123" {
			t.Errorf("code = %q", got)
		}
		if r.FormValue("client_secret") == "" {
			t.Error("client_secret 이 비어 있음")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"stub","refresh_token":"rt-1"}`))
	}))
	defer srv.Close()

	c := newTestAppleClient(t)
	c.SetEndpoints(srv.URL, srv.URL)
	c.SetVerifier(stubVerifier{claims: jwt.MapClaims{"sub": "apple-sub-1", "email": email}})

	user, err := c.ExchangeCode(context.Background(), "auth-code-123")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if user.Sub != "apple-sub-1" {
		t.Errorf("Sub = %q, want apple-sub-1", user.Sub)
	}
	if user.Email == nil || *user.Email != email {
		t.Errorf("Email = %v, want %q", user.Email, email)
	}
}

func TestExchangeCodeWithoutEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id_token":"stub"}`))
	}))
	defer srv.Close()

	c := newTestAppleClient(t)
	c.SetEndpoints(srv.URL, srv.URL)
	c.SetVerifier(stubVerifier{claims: jwt.MapClaims{"sub": "apple-sub-2"}})

	user, err := c.ExchangeCode(context.Background(), "code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if user.Sub != "apple-sub-2" {
		t.Errorf("Sub = %q", user.Sub)
	}
	if user.Email != nil {
		t.Errorf("Email = %v, want nil", *user.Email)
	}
}

func TestExchangeCodeAppleError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	c := newTestAppleClient(t)
	c.SetEndpoints(srv.URL, srv.URL)
	c.SetVerifier(stubVerifier{claims: jwt.MapClaims{"sub": "x"}})

	_, err := c.ExchangeCode(context.Background(), "used-code")
	if err == nil {
		t.Fatal("오류를 기대했지만 성공함")
	}

	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("애플 오류 사유가 빠짐: %v", err)
	}
}

func TestExchangeCodeRejectsBadIDToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id_token":"stub"}`))
	}))
	defer srv.Close()

	c := newTestAppleClient(t)
	c.SetEndpoints(srv.URL, srv.URL)
	c.SetVerifier(stubVerifier{err: errors.New("aud 불일치")})

	if _, err := c.ExchangeCode(context.Background(), "code"); err == nil {
		t.Fatal("검증 실패한 id_token 이 통과함")
	}
}

func TestExchangeCodeRejectsMissingSub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id_token":"stub"}`))
	}))
	defer srv.Close()

	c := newTestAppleClient(t)
	c.SetEndpoints(srv.URL, srv.URL)
	c.SetVerifier(stubVerifier{claims: jwt.MapClaims{"email": "a@b.c"}})

	if _, err := c.ExchangeCode(context.Background(), "code"); err == nil {
		t.Fatal("sub 없는 id_token 이 통과함")
	}
}

func TestRevokeByCode(t *testing.T) {
	var revoked bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("token_type_hint") == "refresh_token" {
			revoked = true
			if got := r.FormValue("token"); got != "rt-1" {
				t.Errorf("revoke token = %q, want rt-1", got)
			}
			return
		}
		_, _ = w.Write([]byte(`{"id_token":"stub","refresh_token":"rt-1"}`))
	}))
	defer srv.Close()

	c := newTestAppleClient(t)
	c.SetEndpoints(srv.URL, srv.URL)

	if err := c.RevokeByCode(context.Background(), "code"); err != nil {
		t.Fatalf("RevokeByCode: %v", err)
	}
	if !revoked {
		t.Error("revoke 엔드포인트가 호출되지 않음")
	}
}

func TestRevokeByCodeWithoutRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id_token":"stub"}`))
	}))
	defer srv.Close()

	c := newTestAppleClient(t)
	c.SetEndpoints(srv.URL, srv.URL)

	err := c.RevokeByCode(context.Background(), "code")
	if !errors.Is(err, ErrAppleNoRefreshToken) {
		t.Fatalf("err = %v, want ErrAppleNoRefreshToken", err)
	}
}

func TestParseECPrivateKey(t *testing.T) {
	valid := testPrivateKeyPEM(t)

	if _, err := parseECPrivateKey(valid); err != nil {
		t.Errorf("정상 PKCS8 키 파싱 실패: %v", err)
	}

	escaped := strings.ReplaceAll(valid, "\n", `\n`)
	if _, err := parseECPrivateKey(escaped); err != nil {
		t.Errorf("\\n 이스케이프된 키 파싱 실패: %v", err)
	}

	for name, in := range map[string]string{
		"빈 문자열":      "",
		"PEM 아님":     "not-a-pem",
		"본문이 깨진 PEM": "-----BEGIN PRIVATE KEY-----\nZm9v\n-----END PRIVATE KEY-----\n",
	} {
		if _, err := parseECPrivateKey(in); err == nil {
			t.Errorf("%s: 오류를 기대했지만 성공함", name)
		}
	}
}
