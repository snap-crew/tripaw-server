package oauth

import (
	"io"
	"net/http"
	"strings"
)

func readErrBody(resp *http.Response) string {
	const limit = 512
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil || len(b) == 0 {
		return "(본문 없음)"
	}
	return strings.TrimSpace(string(b))
}
