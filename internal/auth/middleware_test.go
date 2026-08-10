package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/daewon/tripaw-server/internal/httpx"
	"github.com/daewon/tripaw-server/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const mwSecret = "test-secret-at-least-32-bytes-long!!"

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// protectedRouter 는 RequireAuth 로 보호된 라우트 하나짜리 엔진을 만든다.
// 통과하면 미들웨어가 넣은 사용자 ID 를 그대로 돌려준다.
func protectedRouter(tokens *token.Manager) *gin.Engine {
	r := gin.New()
	r.GET("/protected", RequireAuth(tokens), func(c *gin.Context) {
		id, ok := UserID(c)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "사용자 ID 없음"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"userId": id.String()})
	})
	return r
}

func doRequest(r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequireAuthAcceptsAccessToken(t *testing.T) {
	tokens := token.NewManager(mwSecret, time.Hour, 720*time.Hour)
	userID := uuid.New()

	pair, err := tokens.GeneratePair(userID)
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	w := doRequest(protectedRouter(tokens), "Bearer "+pair.AccessToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body)
	}

	var body struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("응답 파싱: %v", err)
	}
	if body.UserID != userID.String() {
		t.Errorf("userId = %q, want %q", body.UserID, userID)
	}
}

// HTTP 계층에서도 리프레시 토큰이 막히는지 확인한다.
// token 패키지 테스트와 겹쳐 보이지만, 미들웨어가 ValidateAccess 대신
// 종류를 안 가리는 검증을 쓰는 실수를 잡아준다.
func TestRequireAuthRejectsRefreshToken(t *testing.T) {
	tokens := token.NewManager(mwSecret, time.Hour, 720*time.Hour)

	pair, err := tokens.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	w := doRequest(protectedRouter(tokens), "Bearer "+pair.RefreshToken)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("리프레시 토큰으로 보호된 라우트에 접근됨: status = %d", w.Code)
	}

	var p httpx.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("응답 파싱: %v", err)
	}
	if p.Code != "invalid_access_token" {
		t.Errorf("code = %q, want invalid_access_token", p.Code)
	}
}

func TestRequireAuthRejectsBadHeaders(t *testing.T) {
	tokens := token.NewManager(mwSecret, time.Hour, 720*time.Hour)
	pair, err := tokens.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	cases := map[string]string{
		"헤더 없음":       "",
		"스킴 없음":       pair.AccessToken,
		"엉뚱한 스킴":      "Basic " + pair.AccessToken,
		"토큰 없음":       "Bearer",
		"토큰이 공백":      "Bearer    ",
		"서명이 깨진 토큰":   "Bearer not.a.jwt",
		"다른 키로 만든 토큰": "Bearer " + otherKeyToken(t),
	}

	r := protectedRouter(tokens)
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			if w := doRequest(r, header); w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}

// 스킴 대소문자는 구분하지 않는다(RFC 7235).
func TestRequireAuthAcceptsLowercaseBearer(t *testing.T) {
	tokens := token.NewManager(mwSecret, time.Hour, 720*time.Hour)
	pair, err := tokens.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	if w := doRequest(protectedRouter(tokens), "bearer "+pair.AccessToken); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRequireAuthRejectsExpiredToken(t *testing.T) {
	expired := token.NewManager(mwSecret, -time.Hour, -time.Hour)
	pair, err := expired.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}

	valid := token.NewManager(mwSecret, time.Hour, 720*time.Hour)
	if w := doRequest(protectedRouter(valid), "Bearer "+pair.AccessToken); w.Code != http.StatusUnauthorized {
		t.Errorf("만료 토큰이 통과함: status = %d", w.Code)
	}
}

func otherKeyToken(t *testing.T) string {
	t.Helper()

	other := token.NewManager("전혀-다른-서명키-전혀-다른-서명키-32바이트", time.Hour, time.Hour)
	pair, err := other.GeneratePair(uuid.New())
	if err != nil {
		t.Fatalf("GeneratePair: %v", err)
	}
	return pair.AccessToken
}
