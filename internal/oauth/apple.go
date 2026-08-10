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

// ErrAppleNoRefreshToken 은 탈퇴 처리 중 애플이 refresh token 을 주지 않은 경우다.
// 보통 이미 사용된 authorization code 를 다시 보냈을 때 발생한다.
var ErrAppleNoRefreshToken = errors.New("애플이 refresh token 을 주지 않음")

// AppleUser 는 애플 로그인으로 알아낸 사용자 정보다.
type AppleUser struct {
	// Sub 는 애플이 준 고유 ID. 우리 users.provider_sub 에 저장한다.
	Sub string
	// Email 은 없을 수 있다. "나의 이메일 가리기" 를 켰거나,
	// 두 번째 이후 로그인이면 애플이 이메일을 주지 않는다.
	Email *string
}

// AppleClient 는 애플과 통신한다.
//
// 앱이 넘긴 authorization code 를 애플 토큰 엔드포인트에서 교환하고,
// 돌아온 id_token 에서 사용자 정보를 꺼낸다.
type AppleClient struct {
	clientID    string
	teamID      string
	keyID       string
	privateKey  *ecdsa.PrivateKey
	redirectURI string

	tokenURL   string
	revokeURL  string
	httpClient *http.Client

	// 검증기는 애플 JWKS 를 받아와야 만들어진다. 서버 기동 시점에 애플이
	// 잠깐 불안정하다고 기동 자체가 실패하면 곤란하므로, 처음 필요할 때 만들고
	// 실패하면 다음 요청에서 다시 시도한다.
	mu       sync.Mutex
	verifier IDTokenVerifier
}

// NewAppleClient 를 만든다. privateKeyPEM 은 애플 개발자 사이트에서 받은
// ES256 개인키(.p8 파일 내용)다. 네트워크를 타지 않는다.
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

// SetVerifier 는 id_token 검증기를 갈아끼운다. 테스트에서 쓴다.
func (a *AppleClient) SetVerifier(v IDTokenVerifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.verifier = v
}

// SetEndpoints 는 애플 엔드포인트 주소를 바꾼다. 테스트에서 httptest 서버를 물릴 때 쓴다.
func (a *AppleClient) SetEndpoints(tokenURL, revokeURL string) {
	a.tokenURL, a.revokeURL = tokenURL, revokeURL
}

// ExchangeCode 는 앱이 받아온 authorization code 를 애플에서 교환하고
// id_token 에 담긴 사용자 정보를 돌려준다.
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

// RevokeByCode 는 탈퇴 시 애플 쪽 연동을 끊는다.
//
// 애플은 "앱을 지워도 설정 > Apple ID 에 연동이 남아 있으면 안 된다" 는 심사 기준이
// 있어서, 탈퇴할 때 revoke 를 호출해야 한다. 그래서 클라이언트가 탈퇴 요청에
// 새 authorization code 를 함께 보내야 한다.
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

// clientSecret 은 애플이 요구하는 client_secret 을 만든다.
// 고정 문자열이 아니라 우리 개인키로 서명한 짧은 수명의 JWT 다.
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
	// 애플이 어느 키로 검증할지 알려면 kid 가 필요하다.
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

// userFromIDToken 은 id_token 서명을 검증하고 sub/email 을 꺼낸다.
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

// parseECPrivateKey 는 PEM 형식의 EC 개인키를 읽는다.
// 환경변수에 한 줄로 넣으면 줄바꿈이 \n 문자열로 들어오므로 되돌려 준다.
func parseECPrivateKey(pemStr string) (*ecdsa.PrivateKey, error) {
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("PEM 블록을 찾을 수 없음")
	}

	// 애플 .p8 은 PKCS8 이다. 혹시 모를 SEC1 형식도 받아준다.
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
