// Package oauth 는 소셜 로그인 공급자(애플/카카오)와 통신한다.
//
// 여기서 하는 일은 "앱이 보낸 자격증명이 진짜인지 공급자에게 확인하고,
// 그 사람이 누구인지 알아내는 것" 까지다. 우리 서비스 토큰 발급은 internal/token,
// 사용자 저장은 internal/auth 담당이다.
package oauth

import (
	"io"
	"net/http"
	"strings"
)

// readErrBody 는 오류 응답 본문을 로그에 남길 수 있는 짧은 문자열로 만든다.
// 공급자가 주는 원인(invalid_grant 등)을 놓치지 않으려고 읽는다.
func readErrBody(resp *http.Response) string {
	const limit = 512
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil || len(b) == 0 {
		return "(본문 없음)"
	}
	return strings.TrimSpace(string(b))
}
