package auth

import "time"

type AppleLoginRequest struct {
	Code string `json:"code" binding:"required"`

	Nickname *string `json:"nickname"`
}

type KakaoLoginRequest struct {
	AccessToken string `json:"accessToken" binding:"required"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

type DeleteAccountRequest struct {
	Code string `json:"code"`
}

type TokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`

	ExpiresIn int           `json:"expiresIn"`
	User      *UserResponse `json:"user,omitempty"`
}

type UserResponse struct {
	ID           string  `json:"id"`
	Provider     string  `json:"provider"`
	Email        *string `json:"email,omitempty"`
	Nickname     *string `json:"nickname,omitempty"`
	ProfileImage *string `json:"profileImage,omitempty"`
	CreatedAt    string  `json:"createdAt"`
	LastLoginAt  *string `json:"lastLoginAt,omitempty"`
}

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
