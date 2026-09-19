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

	// 심사위원용. 소셜 로그인 없이 버튼 하나로 들어오는 계정이다.
	ProviderTest Provider = "test"
)

// 테스트 계정은 한 행을 공유한다. provider_sub 가 고정이라 몇 번을 눌러도
// 같은 사용자로 들어오고, 심사위원끼리 같은 데이터를 본다.
//
// 닉네임은 계정을 만들 때 한 번만 넣는다. 공급자가 주는 값이 아니라 우리가 정한
// 고정값이라 로그인마다 다시 쓸 이유가 없다.
const (
	testProviderSub = "judge"
	testNickname    = "테스트 계정"
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
