// Package token 은 로그인 후 우리 서버가 발급하는 JWT 를 다룬다.
// 소셜 공급자(애플/카카오)가 준 토큰과는 별개다. 그건 internal/oauth 담당.
package token

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// 토큰 종류. 액세스 토큰과 리프레시 토큰을 반드시 구분해야 한다.
//
// 둘 다 같은 키로 서명한 JWT 라서, 종류 표시가 없으면 유효기간 30일짜리
// 리프레시 토큰을 Authorization 헤더에 그대로 넣어도 통과한다. 그러면
// 액세스 토큰을 1시간으로 짧게 잡은 의미가 없어진다.
// 그래서 typ 클레임을 넣고 검증할 때 종류까지 확인한다.
const (
	typeAccess  = "access"
	typeRefresh = "refresh"
)

var (
	// ErrInvalidToken 은 서명이 틀렸거나 만료됐거나 형식이 깨진 경우다.
	ErrInvalidToken = errors.New("유효하지 않은 토큰")
	// ErrWrongTokenType 은 토큰 자체는 멀쩡하지만 종류가 다른 경우다.
	// (예: 리프레시 토큰을 Authorization 헤더로 보낸 경우)
	ErrWrongTokenType = errors.New("토큰 종류가 맞지 않음")
)

// claims 는 우리가 발급하는 토큰의 페이로드다.
// sub 에 사용자 ID, typ 에 토큰 종류가 들어간다.
type claims struct {
	Typ string `json:"typ"`
	jwt.RegisteredClaims
}

// Manager 는 토큰 발급과 검증을 담당한다.
type Manager struct {
	secret        []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

// Pair 는 로그인·재발급 시 함께 내려주는 토큰 한 쌍이다.
type Pair struct {
	AccessToken  string
	RefreshToken string
	// ExpiresIn 은 액세스 토큰의 남은 수명(초). 클라이언트가 갱신 시점을 잡는 데 쓴다.
	ExpiresIn int
	// RefreshExpiresAt 은 리프레시 토큰 만료 시각. DB 에 함께 저장한다.
	RefreshExpiresAt time.Time
}

// NewManager 를 만든다. secret 은 최소 32바이트를 권장한다(config 에서 검사).
func NewManager(secret string, accessExpiry, refreshExpiry time.Duration) *Manager {
	return &Manager{
		secret:        []byte(secret),
		accessExpiry:  accessExpiry,
		refreshExpiry: refreshExpiry,
	}
}

// GeneratePair 는 액세스/리프레시 토큰을 한 쌍 만든다.
func (m *Manager) GeneratePair(userID uuid.UUID) (*Pair, error) {
	now := time.Now()

	access, err := m.generate(userID, typeAccess, now, m.accessExpiry)
	if err != nil {
		return nil, fmt.Errorf("액세스 토큰 생성: %w", err)
	}
	refresh, err := m.generate(userID, typeRefresh, now, m.refreshExpiry)
	if err != nil {
		return nil, fmt.Errorf("리프레시 토큰 생성: %w", err)
	}

	return &Pair{
		AccessToken:      access,
		RefreshToken:     refresh,
		ExpiresIn:        int(m.accessExpiry.Seconds()),
		RefreshExpiresAt: now.Add(m.refreshExpiry),
	}, nil
}

// ValidateAccess 는 액세스 토큰을 검증하고 사용자 ID 를 돌려준다.
// 인증 미들웨어가 쓴다. 리프레시 토큰을 넣으면 ErrWrongTokenType 이다.
func (m *Manager) ValidateAccess(tokenString string) (uuid.UUID, error) {
	return m.validate(tokenString, typeAccess)
}

// ValidateRefresh 는 리프레시 토큰을 검증하고 사용자 ID 를 돌려준다.
// 재발급 엔드포인트가 쓴다.
func (m *Manager) ValidateRefresh(tokenString string) (uuid.UUID, error) {
	return m.validate(tokenString, typeRefresh)
}

// HashRefresh 는 리프레시 토큰을 DB 에 저장할 형태로 바꾼다.
//
// 토큰 원문을 그대로 저장하면 DB 가 유출됐을 때 그 값으로 바로 재발급을 받을 수
// 있다. 해시만 저장해 두면 유출돼도 원문을 되돌릴 수 없다. 조회할 때도 클라이언트가
// 보낸 토큰을 같은 방식으로 해시해서 찾는다.
//
// 토큰은 이미 128비트 이상의 무작위 요소(jti)를 담은 서명값이라 비밀번호처럼
// 무차별 대입이 통하지 않는다. bcrypt 같은 느린 해시는 필요 없고 SHA-256 이면 된다.
func HashRefresh(tokenString string) string {
	sum := sha256.Sum256([]byte(tokenString))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) generate(userID uuid.UUID, typ string, now time.Time, expiry time.Duration) (string, error) {
	c := claims{
		Typ: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			// jti 를 매번 새로 만든다. 같은 사용자가 같은 초에 두 번 로그인해도
			// 토큰 문자열이 달라야 refresh_tokens 의 UNIQUE 에 걸리지 않는다.
			ID:        uuid.NewString(),
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(m.secret)
}

func (m *Manager) validate(tokenString, want string) (uuid.UUID, error) {
	var c claims
	_, err := jwt.ParseWithClaims(tokenString, &c, func(*jwt.Token) (any, error) {
		return m.secret, nil
	},
		// 서명 알고리즘을 고정한다. 이게 없으면 alg 를 바꿔치기하는 공격이 가능하다.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// exp 가 없는 토큰은 만료되지 않으므로 반드시 있어야 한다.
		jwt.WithExpirationRequired(),
		// 서버 간 시계 차이 허용치.
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	if c.Typ != want {
		return uuid.Nil, fmt.Errorf("%w: %q 를 기대했지만 %q", ErrWrongTokenType, want, c.Typ)
	}

	userID, err := uuid.Parse(c.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: sub 가 UUID 가 아님", ErrInvalidToken)
	}
	return userID, nil
}
