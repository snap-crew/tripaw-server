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

type IDTokenVerifier interface {
	VerifyAndParse(ctx context.Context, idToken string) (jwt.MapClaims, error)
}

type appleJWKSVerifier struct {
	keys     keyfunc.Keyfunc
	clientID string
}

func NewAppleJWKSVerifier(ctx context.Context, clientID string) (IDTokenVerifier, error) {
	keys, err := keyfunc.NewDefaultCtx(ctx, []string{appleJWKSURL})
	if err != nil {
		return nil, fmt.Errorf("애플 JWKS 조회: %w", err)
	}
	return &appleJWKSVerifier{keys: keys, clientID: clientID}, nil
}

func (v *appleJWKSVerifier) VerifyAndParse(ctx context.Context, idToken string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(idToken, v.keys.KeyfuncCtx(ctx),

		jwt.WithValidMethods([]string{"RS256", "ES256"}),

		jwt.WithAudience(v.clientID),

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
