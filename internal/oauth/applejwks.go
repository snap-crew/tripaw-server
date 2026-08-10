package oauth

import (
	"context"
	"fmt"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

const (
	appleJWKSURL = "https://appleid.apple.com/auth/keys"
	appleIssuer  = "https://appleid.apple.com"
)

// IDTokenVerifier 는 애플이 준 id_token 을 검증하고 클레임을 돌려준다.
// 테스트에서 가짜 구현으로 갈아끼울 수 있게 인터페이스로 둔다.
type IDTokenVerifier interface {
	VerifyAndParse(ctx context.Context, idToken string) (jwt.MapClaims, error)
}

// appleJWKSVerifier 는 애플이 공개한 키 묶음(JWKS)으로 서명을 확인한다.
// 키는 keyfunc 가 내부에서 캐시하고 주기적으로 갱신한다.
type appleJWKSVerifier struct {
	keys     keyfunc.Keyfunc
	clientID string
}

// NewAppleJWKSVerifier 는 애플 공개키를 받아 검증기를 만든다.
// 네트워크를 타므로 실패할 수 있다. 호출부에서 지연 생성/재시도를 한다.
func NewAppleJWKSVerifier(ctx context.Context, clientID string) (IDTokenVerifier, error) {
	keys, err := keyfunc.NewDefaultCtx(ctx, []string{appleJWKSURL})
	if err != nil {
		return nil, fmt.Errorf("애플 JWKS 조회: %w", err)
	}
	return &appleJWKSVerifier{keys: keys, clientID: clientID}, nil
}

func (v *appleJWKSVerifier) VerifyAndParse(ctx context.Context, idToken string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(idToken, v.keys.KeyfuncCtx(ctx),
		// 애플은 RS256 으로 서명한다. 알고리즘을 고정하지 않으면
		// alg 를 바꿔치기하는 공격이 가능하다.
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
		// aud 는 이 토큰이 "우리 앱"용으로 발급됐음을 뜻한다.
		// 이걸 확인하지 않으면 다른 앱용으로 발급된 애플 토큰도 받아들이게 된다.
		jwt.WithAudience(v.clientID),
		// iss 는 발급자가 애플인지 확인한다.
		jwt.WithIssuer(appleIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("id_token 검증: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("id_token 클레임을 읽을 수 없음")
	}
	return claims, nil
}
