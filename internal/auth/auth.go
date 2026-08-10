// Package auth 는 소셜 로그인으로 사용자를 인증하고 세션을 관리한다.
//
// 흐름은 이렇다:
//
//	앱이 애플/카카오 SDK 로 로그인
//	  → 받은 자격증명(애플 code / 카카오 access token)을 우리 서버로 전송
//	  → 서버가 공급자에게 확인하고 사용자를 찾거나 만든다
//	  → 우리 서비스 토큰(액세스 + 리프레시)을 발급
package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Provider 는 로그인 공급자다. DB 의 auth_provider ENUM 과 값이 같아야 한다.
type Provider string

const (
	ProviderApple Provider = "apple"
	ProviderKakao Provider = "kakao"
)

// User 는 가입한 사용자다.
//
// 포인터 필드는 "없을 수 있음" 을 뜻한다. 애플은 이메일을 가릴 수 있고 두 번째
// 로그인부터는 아예 주지 않는다. 카카오도 사용자가 동의하지 않으면 주지 않는다.
type User struct {
	ID           uuid.UUID
	Provider     Provider
	ProviderSub  string
	Email        *string
	Nickname     *string
	ProfileImage *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastLoginAt  *time.Time
}

var (
	// ErrUserNotFound 는 해당 ID 의 사용자가 없는 경우다.
	ErrUserNotFound = errors.New("사용자를 찾을 수 없습니다")

	// ErrInvalidRefreshToken 은 리프레시 토큰이 없거나 만료됐거나 이미 사용된 경우다.
	// 이미 사용된 경우까지 여기에 포함되는 건 재발급 때 기존 토큰을 지우기 때문이다.
	ErrInvalidRefreshToken = errors.New("유효하지 않은 리프레시 토큰입니다")

	// ErrProviderRejected 는 공급자가 자격증명을 거부한 경우다.
	// 코드가 만료됐거나, 이미 쓴 코드거나, 다른 앱의 토큰인 경우.
	ErrProviderRejected = errors.New("소셜 로그인 확인에 실패했습니다")

	// ErrAppleCodeRequired 는 애플 사용자가 탈퇴하면서 authorization code 를
	// 보내지 않은 경우다. 애플 연동을 끊으려면 code 가 필요하다.
	ErrAppleCodeRequired = errors.New("애플 계정 탈퇴에는 authorization code 가 필요합니다")
)
