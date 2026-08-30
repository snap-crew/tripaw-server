package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStructuredSendsSchemaAndReturnsJSON(t *testing.T) {
	var got map[string]any
	var path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("x-goog-api-key = %q", r.Header.Get("x-goog-api-key"))
		}

		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("요청 본문 파싱: %v", err)
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"finishReason":"STOP","content":{"parts":[
			{"text":"{\"days\":["},{"text":"{\"dayNo\":1}]}"}
		]}}]}`))
	}))
	defer srv.Close()

	c := New("test-key", "gemini-test")
	c.baseURL = srv.URL

	raw, err := c.Structured(context.Background(), "시스템", "프롬프트",
		Schema{"type": "OBJECT"})
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	if string(raw) != `{"days":[{"dayNo":1}]}` {
		t.Errorf("결과 = %s, 여러 part 를 이어붙여야 한다", raw)
	}

	if !strings.HasSuffix(path, "/v1beta/models/gemini-test:generateContent") {
		t.Errorf("경로 = %q", path)
	}

	cfg, _ := got["generationConfig"].(map[string]any)
	if cfg["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType = %v, JSON 을 강제하지 않았다", cfg["responseMimeType"])
	}
	if cfg["responseSchema"] == nil {
		t.Error("responseSchema 가 실리지 않았다")
	}
}

func TestStructuredFailsOnErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer srv.Close()

	c := New("test-key", "gemini-test")
	c.baseURL = srv.URL

	_, err := c.Structured(context.Background(), "", "", Schema{})
	if err == nil {
		t.Fatal("429 인데 에러가 없다. 무료 플랜에서 폴백이 안 걸린다")
	}
}

func TestStructuredFailsOnTruncatedOrBlocked(t *testing.T) {
	for name, payload := range map[string]string{
		"토큰 초과로 잘림": `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"{\"days\""}]}}]}`,
		"안전 필터 차단":  `{"candidates":[],"promptFeedback":{"blockReason":"SAFETY"}}`,
		"후보 없음":     `{"candidates":[]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(payload))
		}))

		c := New("test-key", "gemini-test")
		c.baseURL = srv.URL

		if _, err := c.Structured(context.Background(), "", "", Schema{}); err == nil {
			t.Errorf("%s: 에러가 없다", name)
		}
		srv.Close()
	}
}

func TestNewWithoutKeyReturnsNil(t *testing.T) {
	if New("", "gemini-test") != nil {
		t.Error("키가 없으면 nil 이어야 폴백으로 동작한다")
	}
}
