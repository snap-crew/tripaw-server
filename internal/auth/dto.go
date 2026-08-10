package auth

import "time"

// AppleLoginRequest 는 POST /api/auth/apple 의 본문이다.
// Code 는 앱의 Sign in with Apple 에서 받은 authorization code 다.
type AppleLoginRequest struct {
	Code string `json:"code" binding:"required"`
}

// KakaoLoginRequest 는 POST /api/auth/kakao 의 본문이다.
// AccessToken 은 앱의 카카오 SDK 가 받아온 액세스 토큰이다.
type KakaoLoginRequest struct {
	AccessToken string `json:"accessToken" binding:"required"`
}

// RefreshRequest 는 POST /api/auth/refresh 의 본문이다.
type RefreshRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

// DeleteAccountRequest 는 DELETE /api/auth/account 의 본문이다.
// 애플 사용자는 연동 해제를 위해 새 authorization code 를 함께 보내야 한다.
type DeleteAccountRequest struct {
	Code string `json:"code"`
}

// TokenResponse 는 로그인·재발급의 응답이다.
type TokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	// ExpiresIn 은 액세스 토큰의 수명(초). 클라이언트가 미리 갱신하는 데 쓴다.
	ExpiresIn int           `json:"expiresIn"`
	User      *UserResponse `json:"user,omitempty"`
}

// UserResponse 는 API 로 내보내는 사용자 표현이다.
// User 를 그대로 쓰지 않고 따로 두는 건, DB 컬럼이 바뀌어도 응답 형태를
// 유지하기 위해서다.
type UserResponse struct {
	ID           string  `json:"id"`
	Provider     string  `json:"provider"`
	Email        *string `json:"email,omitempty"`
	Nickname     *string `json:"nickname,omitempty"`
	ProfileImage *string `json:"profileImage,omitempty"`
	CreatedAt    string  `json:"createdAt"`
	LastLoginAt  *string `json:"lastLoginAt,omitempty"`
}

// NewUserResponse 는 도메인 User 를 응답 형태로 바꾼다.
func NewUserResponse(u *User) *UserResponse {
	resp := &UserResponse{
		ID:           u.ID.String(),
		Provider:     string(u.Provider),
		Email:        u.Email,
		Nickname:     u.Nickname,
		ProfileImage: u.ProfileImage,
		CreatedAt:    u.CreatedAt.Format(time.RFC3339),
	}
	if u.LastLoginAt != nil {
		s := u.LastLoginAt.Format(time.RFC3339)
		resp.LastLoginAt = &s
	}
	return resp
}
