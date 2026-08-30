package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultBaseURL  = "https://generativelanguage.googleapis.com"
	maxOutputTokens = 2048
)

type Schema map[string]any

type Client struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

func New(apiKey, model string) *Client {
	if apiKey == "" {
		return nil
	}
	return &Client{
		apiKey:  apiKey,
		model:   model,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Model() string { return c.model }

func (c *Client) Structured(
	ctx context.Context, system, prompt string, schema Schema,
) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": system}},
		},
		"contents": []any{
			map[string]any{
				"role":  "user",
				"parts": []any{map[string]any{"text": prompt}},
			},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"responseSchema":   schema,
			"maxOutputTokens":  maxOutputTokens,
			"temperature":      0.3,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("요청 직렬화: %w", err)
	}

	endpoint := c.baseURL + "/v1beta/models/" + url.PathEscape(c.model) + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("요청 생성: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini 호출: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("gemini %d: %s", res.StatusCode, msg)
	}

	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("응답 파싱: %w", err)
	}

	if len(out.Candidates) == 0 {
		if reason := out.PromptFeedback.BlockReason; reason != "" {
			return nil, fmt.Errorf("gemini 가 요청을 차단했습니다: %s", reason)
		}
		return nil, fmt.Errorf("gemini 응답에 결과가 없습니다")
	}

	cand := out.Candidates[0]
	var text bytes.Buffer
	for _, p := range cand.Content.Parts {
		text.WriteString(p.Text)
	}
	if text.Len() == 0 {
		return nil, fmt.Errorf("gemini 응답이 비어 있습니다 (finishReason=%s)", cand.FinishReason)
	}
	if cand.FinishReason != "" && cand.FinishReason != "STOP" {
		return nil, fmt.Errorf("gemini 응답이 끊겼습니다 (finishReason=%s)", cand.FinishReason)
	}
	return json.RawMessage(text.Bytes()), nil
}
