package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/snap-crew/tripaw-server/internal/httpx"
	"github.com/snap-crew/tripaw-server/internal/oauth"
	"github.com/snap-crew/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const flowSecret = "flow-test-secret-at-least-32-bytes!!"

type appleStubVerifier struct{ sub, email string }

func (s appleStubVerifier) VerifyAndParse(context.Context, string) (jwt.MapClaims, error) {
	c := jwt.MapClaims{"sub": s.sub}
	if s.email != "" {
		c["email"] = s.email
	}
	return c, nil
}

func applePEM(t *testing.T) string {
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

type flowEnv struct {
	engine *gin.Engine
	repo   *Repository
}

func newFlowEnv(t *testing.T, appleSub, appleEmail string) *flowEnv {
	t.Helper()

	repo, _ := testRepo(t)

	kakaoMux := http.NewServeMux()
	kakaoMux.HandleFunc("/v1/user/access_token_info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"app_id":777,"expires_in":21599}`))
	})
	kakaoMux.HandleFunc("/v2/user/me", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":55501,"kakao_account":{"email":"k@ex.com",
			"is_email_verified":true,"profile":{"nickname":"카카오유저"}}}`))
	})
	kakaoSrv := httptest.NewServer(kakaoMux)
	t.Cleanup(kakaoSrv.Close)

	kakaoClient := oauth.NewKakaoClient(777)
	kakaoClient.SetEndpoints(kakaoSrv.URL+"/v2/user/me", kakaoSrv.URL+"/v1/user/access_token_info")

	appleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id_token":"stub","refresh_token":"apple-rt"}`))
	}))
	t.Cleanup(appleSrv.Close)

	appleClient, err := oauth.NewAppleClient("com.tripaw.app", "TEAM1", "KEY1", applePEM(t), "")
	if err != nil {
		t.Fatalf("NewAppleClient: %v", err)
	}
	appleClient.SetEndpoints(appleSrv.URL, appleSrv.URL)
	appleClient.SetVerifier(appleStubVerifier{sub: appleSub, email: appleEmail})

	tokens := token.NewManager(flowSecret, time.Hour, 720*time.Hour)
	handler := NewHandler(NewService(repo, tokens, appleClient, kakaoClient))

	r := gin.New()
	api := r.Group("/api")
	handler.RegisterPublic(api)
	protected := api.Group("")
	protected.Use(RequireAuth(tokens))
	handler.RegisterProtected(protected)

	return &flowEnv{engine: r, repo: repo}
}

func (e *flowEnv) do(t *testing.T, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("요청 본문 인코딩: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func decodeTokens(t *testing.T, w *httptest.ResponseRecorder) TokenResponse {
	t.Helper()

	var resp TokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("응답 파싱 실패 (body: %s): %v", w.Body, err)
	}
	return resp
}

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()

	var p httpx.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("오류 응답 파싱 실패 (body: %s): %v", w.Body, err)
	}
	return p.Code
}

func TestKakaoLoginFlow(t *testing.T) {
	env := newFlowEnv(t, "apple-flow", "")

	w := env.do(t, http.MethodPost, "/api/auth/kakao", "",
		KakaoLoginRequest{AccessToken: "sdk-token"})
	if w.Code != http.StatusOK {
		t.Fatalf("로그인 status = %d (body: %s)", w.Code, w.Body)
	}

	login := decodeTokens(t, w)
	if login.AccessToken == "" || login.RefreshToken == "" {
		t.Fatal("토큰이 비어 있음")
	}
	if login.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn = %d, want 3600", login.ExpiresIn)
	}
	if login.User == nil || login.User.Provider != "kakao" {
		t.Fatalf("User = %+v", login.User)
	}
	if login.User.Nickname == nil || *login.User.Nickname != "카카오유저" {
		t.Errorf("Nickname = %v", login.User.Nickname)
	}

	w = env.do(t, http.MethodGet, "/api/auth/me", login.AccessToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("me status = %d (body: %s)", w.Code, w.Body)
	}
	var me UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
		t.Fatalf("me 파싱: %v", err)
	}
	if me.ID != login.User.ID {
		t.Errorf("me.ID = %q, want %q", me.ID, login.User.ID)
	}

	w = env.do(t, http.MethodPost, "/api/auth/refresh", "",
		RefreshRequest{RefreshToken: login.RefreshToken})
	if w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d (body: %s)", w.Code, w.Body)
	}
	refreshed := decodeTokens(t, w)
	if refreshed.RefreshToken == login.RefreshToken {
		t.Error("리프레시 토큰이 교체되지 않음")
	}

	w = env.do(t, http.MethodPost, "/api/auth/refresh", "",
		RefreshRequest{RefreshToken: login.RefreshToken})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("사용된 리프레시 토큰 재사용 status = %d, want 401", w.Code)
	}
	if code := problemCode(t, w); code != "invalid_refresh_token" {
		t.Errorf("code = %q", code)
	}

	if w := env.do(t, http.MethodGet, "/api/auth/me", refreshed.AccessToken, nil); w.Code != http.StatusOK {
		t.Errorf("새 액세스 토큰으로 me status = %d", w.Code)
	}

	if w := env.do(t, http.MethodPost, "/api/auth/logout", refreshed.AccessToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d (body: %s)", w.Code, w.Body)
	}

	w = env.do(t, http.MethodPost, "/api/auth/refresh", "",
		RefreshRequest{RefreshToken: refreshed.RefreshToken})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("로그아웃 후 refresh status = %d, want 401", w.Code)
	}
}

func TestLoginTwiceReusesAccount(t *testing.T) {
	env := newFlowEnv(t, "apple-flow", "")

	first := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/kakao", "",
		KakaoLoginRequest{AccessToken: "sdk-token"}))
	second := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/kakao", "",
		KakaoLoginRequest{AccessToken: "sdk-token"}))

	if first.User.ID != second.User.ID {
		t.Errorf("재로그인에 새 계정이 생성됨: %s → %s", first.User.ID, second.User.ID)
	}
	if first.RefreshToken == second.RefreshToken {
		t.Error("재로그인인데 같은 리프레시 토큰이 발급됨")
	}
}

func TestAppleLoginFlow(t *testing.T) {
	env := newFlowEnv(t, "apple-sub-flow", "apple@privaterelay.appleid.com")

	name := "홍길동"
	w := env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-1", Nickname: &name})
	if w.Code != http.StatusOK {
		t.Fatalf("애플 로그인 status = %d (body: %s)", w.Code, w.Body)
	}

	login := decodeTokens(t, w)
	if login.User.Provider != "apple" {
		t.Errorf("Provider = %q", login.User.Provider)
	}
	if login.User.Email == nil || *login.User.Email != "apple@privaterelay.appleid.com" {
		t.Errorf("Email = %v", login.User.Email)
	}
	if login.User.Nickname == nil || *login.User.Nickname != "홍길동" {
		t.Errorf("Nickname = %v, want 홍길동", login.User.Nickname)
	}
}

func TestAppleNamePersistsAcrossLogins(t *testing.T) {
	env := newFlowEnv(t, "apple-name-once", "")

	name := "홍길동"
	first := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-1", Nickname: &name}))
	if first.User.Nickname == nil {
		t.Fatal("첫 로그인에 이름이 저장되지 않음")
	}

	second := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-2"}))

	if second.User.Nickname == nil {
		t.Fatal("재로그인에서 이름이 지워짐")
	}
	if *second.User.Nickname != "홍길동" {
		t.Errorf("Nickname = %q, want 홍길동", *second.User.Nickname)
	}
}

func TestAppleBlankNameIsNotStored(t *testing.T) {
	env := newFlowEnv(t, "apple-blank-name", "")

	blank := "   "
	first := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-1", Nickname: &blank}))
	if first.User.Nickname != nil {
		t.Errorf("공백 이름이 저장됨: %q", *first.User.Nickname)
	}

	name := "홍길동"
	second := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-2", Nickname: &name}))
	if second.User.Nickname == nil || *second.User.Nickname != "홍길동" {
		t.Errorf("Nickname = %v, want 홍길동", second.User.Nickname)
	}
}

func TestKakaoLoginStoresEmailAndNickname(t *testing.T) {
	env := newFlowEnv(t, "unused", "")

	login := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/kakao", "",
		KakaoLoginRequest{AccessToken: "sdk-token"}))

	if login.User.Email == nil || *login.User.Email != "k@ex.com" {
		t.Errorf("Email = %v", login.User.Email)
	}
	if login.User.Nickname == nil || *login.User.Nickname != "카카오유저" {
		t.Errorf("Nickname = %v", login.User.Nickname)
	}
}

func TestDeleteAppleAccountRequiresCode(t *testing.T) {
	env := newFlowEnv(t, "apple-delete", "")

	login := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-1"}))

	w := env.do(t, http.MethodDelete, "/api/auth/account", login.AccessToken, DeleteAccountRequest{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body)
	}
	if code := problemCode(t, w); code != "apple_code_required" {
		t.Errorf("code = %q, want apple_code_required", code)
	}

	w = env.do(t, http.MethodDelete, "/api/auth/account", login.AccessToken,
		DeleteAccountRequest{Code: "revoke-code"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", w.Code, w.Body)
	}

	if w := env.do(t, http.MethodGet, "/api/auth/me", login.AccessToken, nil); w.Code != http.StatusNotFound {
		t.Errorf("탈퇴 후 me status = %d, want 404", w.Code)
	}
}

func TestDeleteKakaoAccount(t *testing.T) {
	env := newFlowEnv(t, "unused", "")

	login := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/kakao", "",
		KakaoLoginRequest{AccessToken: "sdk-token"}))

	if w := env.do(t, http.MethodDelete, "/api/auth/account", login.AccessToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", w.Code, w.Body)
	}
}

func TestLoginRejectsMissingFields(t *testing.T) {
	env := newFlowEnv(t, "unused", "")

	cases := []struct {
		name, path string
		body       any
	}{
		{"애플 code 없음", "/api/auth/apple", map[string]string{}},
		{"카카오 accessToken 없음", "/api/auth/kakao", map[string]string{}},
		{"refreshToken 없음", "/api/auth/refresh", map[string]string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := env.do(t, http.MethodPost, tc.path, "", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body: %s)", w.Code, w.Body)
			}
		})
	}
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	env := newFlowEnv(t, "unused", "")

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodDelete, "/api/auth/account"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if w := env.do(t, tc.method, tc.path, "", nil); w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}
