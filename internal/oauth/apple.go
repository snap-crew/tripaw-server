package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	appleTokenURL  = "https://appleid.apple.com/auth/token"
	appleRevokeURL = "https://appleid.apple.com/auth/revoke"
)

var ErrAppleNoRefreshToken = errors.New("애플이 refresh token 을 주지 않음")

type AppleUser struct {
	Sub string

	Email *string
}

type AppleClient struct {
	clientID    string
	teamID      string
	keyID       string
	privateKey  *ecdsa.PrivateKey
	redirectURI string

	tokenURL   string
	revokeURL  string
	httpClient *http.Client

	mu       sync.Mutex
	verifier IDTokenVerifier
}

func NewAppleClient(clientID, teamID, keyID, privateKeyPEM, redirectURI string) (*AppleClient, error) {
	privKey, err := parseECPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("애플 개인키 파싱: %w", err)
	}

	return &AppleClient{
		clientID:    clientID,
		teamID:      teamID,
		keyID:       keyID,
		privateKey:  privKey,
		redirectURI: redirectURI,
		tokenURL:    appleTokenURL,
		revokeURL:   appleRevokeURL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (a *AppleClient) SetVerifier(v IDTokenVerifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.verifier = v
}

func (a *AppleClient) SetEndpoints(tokenURL, revokeURL string) {
	a.tokenURL, a.revokeURL = tokenURL, revokeURL
}

func (a *AppleClient) ExchangeCode(ctx context.Context, code string) (*AppleUser, error) {
	secret, err := a.clientSecret()
	if err != nil {
		return nil, err
	}

	resp, err := a.exchange(ctx, code, secret)
	if err != nil {
		return nil, err
	}

	return a.userFromIDToken(ctx, resp.IDToken)
}

func (a *AppleClient) RevokeByCode(ctx context.Context, code string) error {
	secret, err := a.clientSecret()
	if err != nil {
		return err
	}

	resp, err := a.exchange(ctx, code, secret)
	if err != nil {
		return fmt.Errorf("revoke 용 코드 교환: %w", err)
	}
	if resp.RefreshToken == "" {
		return ErrAppleNoRefreshToken
	}

	return a.revoke(ctx, resp.RefreshToken, secret)
}

func (a *AppleClient) clientSecret() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    a.teamID,
		Subject:   a.clientID,
		Audience:  jwt.ClaimStrings{appleIssuer},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)

	token.Header["kid"] = a.keyID

	signed, err := token.SignedString(a.privateKey)
	if err != nil {
		return "", fmt.Errorf("client_secret 서명: %w", err)
	}
	return signed, nil
}

func (a *AppleClient) exchange(ctx context.Context, code, clientSecret string) (*appleTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {a.clientID},
		"client_secret": {clientSecret},
		"code":          {code},
	}
	if a.redirectURI != "" {
		form.Set("redirect_uri", a.redirectURI)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("토큰 요청 생성: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("애플 토큰 요청: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("애플 토큰 엔드포인트 %d: %s", resp.StatusCode, readErrBody(resp))
	}

	var out appleTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("토큰 응답 디코딩: %w", err)
	}
	if out.IDToken == "" {
		return nil, fmt.Errorf("애플 응답에 id_token 이 없음")
	}
	return &out, nil
}

func (a *AppleClient) revoke(ctx context.Context, refreshToken, clientSecret string) error {
	form := url.Values{
		"client_id":       {a.clientID},
		"client_secret":   {clientSecret},
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("revoke 요청 생성: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("애플 revoke 요청: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("애플 revoke 엔드포인트 %d: %s", resp.StatusCode, readErrBody(resp))
	}
	return nil
}

func (a *AppleClient) userFromIDToken(ctx context.Context, idToken string) (*AppleUser, error) {
	v, err := a.idTokenVerifier(ctx)
	if err != nil {
		return nil, err
	}

	claims, err := v.VerifyAndParse(ctx, idToken)
	if err != nil {
		return nil, err
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("id_token 에 sub 클레임이 없음")
	}

	user := &AppleUser{Sub: sub}
	if email, ok := claims["email"].(string); ok && email != "" {
		user.Email = &email
	}
	return user, nil
}

func (a *AppleClient) idTokenVerifier(ctx context.Context) (IDTokenVerifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.verifier != nil {
		return a.verifier, nil
	}

	v, err := NewAppleJWKSVerifier(ctx, a.clientID)
	if err != nil {
		return nil, err
	}
	a.verifier = v
	return v, nil
}

func parseECPrivateKey(pemStr string) (*ecdsa.PrivateKey, error) {
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("PEM 블록을 찾을 수 없음")
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("ECDSA 키가 아님 (%T)", key)
		}
		return ecKey, nil
	}

	ecKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("PKCS8/SEC1 둘 다 실패: %w", err)
	}
	return ecKey, nil
}

type appleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}
