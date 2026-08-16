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

const (
	typeAccess  = "access"
	typeRefresh = "refresh"
)

var (
	ErrInvalidToken = errors.New("유효하지 않은 토큰")

	ErrWrongTokenType = errors.New("토큰 종류가 맞지 않음")
)

type claims struct {
	Typ string `json:"typ"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret        []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

type Pair struct {
	AccessToken  string
	RefreshToken string

	ExpiresIn int

	RefreshExpiresAt time.Time
}

func NewManager(secret string, accessExpiry, refreshExpiry time.Duration) *Manager {
	return &Manager{
		secret:        []byte(secret),
		accessExpiry:  accessExpiry,
		refreshExpiry: refreshExpiry,
	}
}

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

func (m *Manager) ValidateAccess(tokenString string) (uuid.UUID, error) {
	return m.validate(tokenString, typeAccess)
}

func (m *Manager) ValidateRefresh(tokenString string) (uuid.UUID, error) {
	return m.validate(tokenString, typeRefresh)
}

func HashRefresh(tokenString string) string {
	sum := sha256.Sum256([]byte(tokenString))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) generate(userID uuid.UUID, typ string, now time.Time, expiry time.Duration) (string, error) {
	c := claims{
		Typ: typ,
		RegisteredClaims: jwt.RegisteredClaims{
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

		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),

		jwt.WithExpirationRequired(),

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
