package image

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func testRouter(t *testing.T) (*gin.Engine, uuid.UUID) {
	t.Helper()
	svc, _, userID := testSvc(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) { c.Set("userID", userID) })
	NewHandler(svc).RegisterProtected(api)
	return r, userID
}

func TestUploadThenServeRoundTrip(t *testing.T) {
	r, _ := testRouter(t)
	body := jpeg()

	req := httptest.NewRequest(http.MethodPost, "/api/images", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("업로드 status = %d, 기대 201 (%s)", w.Code, w.Body.String())
	}

	var up UploadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &up); err != nil {
		t.Fatalf("응답 파싱: %v", err)
	}
	if !IsManagedURL(up.URL) {
		t.Fatalf("url = %q, 관리 주소 형식이 아니다", up.URL)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, up.URL, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("조회 status = %d, 기대 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, 기대 image/jpeg", ct)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, 기대 nosniff — 브라우저가 타입을 추측하면 안 된다", got)
	}
	if !bytes.Equal(w.Body.Bytes(), body) {
		t.Error("돌려받은 바이트가 올린 것과 다르다")
	}
}

func TestServeRejectsUnknownAndMalformed(t *testing.T) {
	r, _ := testRouter(t)

	for _, path := range []string{
		"/api/images/" + uuid.NewString(),
		"/api/images/not-a-uuid",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, 기대 404", path, w.Code)
		}
	}
}

func TestUploadRejectsNonImageOverHTTP(t *testing.T) {
	r, _ := testRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/images",
		bytes.NewReader([]byte("<html><script>alert(1)</script></html>")))
	req.Header.Set("Content-Type", "image/jpeg")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, 기대 415 — 클라이언트가 주장한 타입을 믿으면 안 된다", w.Code)
	}
}

func TestUploadRejectsOversizeOverHTTP(t *testing.T) {
	r, _ := testRouter(t)

	big := append(jpeg(), bytes.Repeat([]byte{0x20}, MaxBytes)...)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/images", bytes.NewReader(big)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, 기대 413", w.Code)
	}
}
