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

	"github.com/daewon/tripaw-server/internal/httpx"
	"github.com/daewon/tripaw-server/internal/oauth"
	"github.com/daewon/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const flowSecret = "flow-test-secret-at-least-32-bytes!!"

// appleStubVerifier 는 실제 애플 JWKS 대신 고정된 sub 를 돌려준다.
type appleStubVerifier struct{ sub, email string }

func (s appleStubVerifier) VerifyAndParse(context.Context, string) (jwt.MapClaims, error) {
	c := jwt.MapClaims{"sub": s.sub}
	if s.email != "" {
		c["email"] = s.email
	}
	return c, nil
}

// applePEM 은 애플 클라이언트 생성에 필요한 ES256 개인키를 만든다.
// client_secret 서명에만 쓰이고 스텁 서버는 검증하지 않으므로 임의 키면 된다.
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

// flowEnv 는 라우트가 등록된 엔진과 저장소를 함께 돌려준다.
// 공급자 두 곳은 httptest 서버로 대체하고, DB 는 진짜를 쓴다.
type flowEnv struct {
	engine *gin.Engine
	repo   *Repository
}

func newFlowEnv(t *testing.T, appleSub, appleEmail string) *flowEnv {
	t.Helper()

	repo, _ := testRepo(t)

	// 카카오: 토큰 출처 확인 + 사용자 정보
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

	// 애플: 코드 교환
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

// 로그인부터 로그아웃까지 클라이언트가 실제로 밟는 순서를 그대로 훑는다.
func TestKakaoLoginFlow(t *testing.T) {
	env := newFlowEnv(t, "apple-flow", "")

	// 1. 로그인
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

	// 2. 내 정보 조회
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

	// 3. 재발급
	w = env.do(t, http.MethodPost, "/api/auth/refresh", "",
		RefreshRequest{RefreshToken: login.RefreshToken})
	if w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d (body: %s)", w.Code, w.Body)
	}
	refreshed := decodeTokens(t, w)
	if refreshed.RefreshToken == login.RefreshToken {
		t.Error("리프레시 토큰이 교체되지 않음")
	}

	// 4. 방금 쓴 리프레시 토큰은 더 이상 통하지 않아야 한다
	w = env.do(t, http.MethodPost, "/api/auth/refresh", "",
		RefreshRequest{RefreshToken: login.RefreshToken})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("사용된 리프레시 토큰 재사용 status = %d, want 401", w.Code)
	}
	if code := problemCode(t, w); code != "invalid_refresh_token" {
		t.Errorf("code = %q", code)
	}

	// 5. 새 액세스 토큰은 정상 동작
	if w := env.do(t, http.MethodGet, "/api/auth/me", refreshed.AccessToken, nil); w.Code != http.StatusOK {
		t.Errorf("새 액세스 토큰으로 me status = %d", w.Code)
	}

	// 6. 로그아웃
	if w := env.do(t, http.MethodPost, "/api/auth/logout", refreshed.AccessToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d (body: %s)", w.Code, w.Body)
	}

	// 7. 로그아웃 후에는 재발급이 막힌다
	w = env.do(t, http.MethodPost, "/api/auth/refresh", "",
		RefreshRequest{RefreshToken: refreshed.RefreshToken})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("로그아웃 후 refresh status = %d, want 401", w.Code)
	}
}

// 같은 계정으로 다시 로그인하면 사용자가 새로 생기지 않아야 한다.
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

	name := "김대원"
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
	if login.User.Nickname == nil || *login.User.Nickname != "김대원" {
		t.Errorf("Nickname = %v, want 김대원", login.User.Nickname)
	}
}

// 애플은 이름을 최초 인증 때 한 번만 준다. 두 번째 로그인에는 이름이 없는데,
// 그때 처음 저장한 이름이 지워지면 되찾을 방법이 없다.
func TestAppleNamePersistsAcrossLogins(t *testing.T) {
	env := newFlowEnv(t, "apple-name-once", "")

	name := "김대원"
	first := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-1", Nickname: &name}))
	if first.User.Nickname == nil {
		t.Fatal("첫 로그인에 이름이 저장되지 않음")
	}

	// 두 번째 로그인 — 클라이언트가 이름을 보내지 않는다
	second := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-2"}))

	if second.User.Nickname == nil {
		t.Fatal("재로그인에서 이름이 지워짐")
	}
	if *second.User.Nickname != "김대원" {
		t.Errorf("Nickname = %q, want 김대원", *second.User.Nickname)
	}
}

// 사용자가 이름 제공에 동의하지 않으면 fullName 이 빈 문자열로 조립되어 온다.
// 이걸 저장해 버리면 "이름 있음" 이 되어 나중에 진짜 이름이 와도 덮이지 않는다.
func TestAppleBlankNameIsNotStored(t *testing.T) {
	env := newFlowEnv(t, "apple-blank-name", "")

	blank := "   "
	first := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-1", Nickname: &blank}))
	if first.User.Nickname != nil {
		t.Errorf("공백 이름이 저장됨: %q", *first.User.Nickname)
	}

	// 나중에 진짜 이름이 오면 채워져야 한다
	name := "김대원"
	second := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-2", Nickname: &name}))
	if second.User.Nickname == nil || *second.User.Nickname != "김대원" {
		t.Errorf("Nickname = %v, want 김대원", second.User.Nickname)
	}
}

// 카카오는 이메일·닉네임을 API 에서 직접 받아온다.
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

// 애플 사용자는 탈퇴에 authorization code 가 필요하다.
func TestDeleteAppleAccountRequiresCode(t *testing.T) {
	env := newFlowEnv(t, "apple-delete", "")

	login := decodeTokens(t, env.do(t, http.MethodPost, "/api/auth/apple", "",
		AppleLoginRequest{Code: "code-1"}))

	// code 없이 탈퇴 시도
	w := env.do(t, http.MethodDelete, "/api/auth/account", login.AccessToken, DeleteAccountRequest{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body)
	}
	if code := problemCode(t, w); code != "apple_code_required" {
		t.Errorf("code = %q, want apple_code_required", code)
	}

	// code 를 주면 성공 (애플 스텁이 refresh_token 을 돌려준다)
	w = env.do(t, http.MethodDelete, "/api/auth/account", login.AccessToken,
		DeleteAccountRequest{Code: "revoke-code"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", w.Code, w.Body)
	}

	// 지워진 사용자의 토큰으로는 조회가 안 된다
	if w := env.do(t, http.MethodGet, "/api/auth/me", login.AccessToken, nil); w.Code != http.StatusNotFound {
		t.Errorf("탈퇴 후 me status = %d, want 404", w.Code)
	}
}

// 카카오 사용자는 code 없이 탈퇴할 수 있다.
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

// 보호된 라우트는 토큰 없이 접근할 수 없다.
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
