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
