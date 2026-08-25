package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Provider string

const imagePathPrefix = "/api/images/"

const (
	ProviderApple Provider = "apple"
	ProviderKakao Provider = "kakao"
)

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
	ErrUserNotFound = errors.New("사용자를 찾을 수 없습니다")

	ErrInvalidRefreshToken = errors.New("유효하지 않은 리프레시 토큰입니다")

	ErrProviderRejected = errors.New("소셜 로그인 확인에 실패했습니다")

	ErrInvalidNickname = errors.New("이름은 1자 이상 20자 이하여야 합니다")

	ErrInvalidProfileImage = errors.New("프로필 이미지 주소가 올바르지 않습니다")

	ErrAppleCodeRequired = errors.New("애플 계정 탈퇴에는 authorization code 가 필요합니다")
)
