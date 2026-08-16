package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	kakaoUserMeURL    = "https://kapi.kakao.com/v2/user/me"
	kakaoTokenInfoURL = "https://kapi.kakao.com/v1/user/access_token_info"
)

var ErrKakaoForeignToken = errors.New("다른 앱에서 발급된 카카오 토큰")

type KakaoUser struct {
	Sub string

	Email        *string
	Nickname     *string
	ProfileImage *string
}

type KakaoClient struct {
	appID int64

	userMeURL    string
	tokenInfoURL string
	httpClient   *http.Client
}

func NewKakaoClient(appID int64) *KakaoClient {
	return &KakaoClient{
		appID:        appID,
		userMeURL:    kakaoUserMeURL,
		tokenInfoURL: kakaoTokenInfoURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (k *KakaoClient) SetEndpoints(userMeURL, tokenInfoURL string) {
	k.userMeURL, k.tokenInfoURL = userMeURL, tokenInfoURL
}

func (k *KakaoClient) GetUser(ctx context.Context, accessToken string) (*KakaoUser, error) {
	if k.appID != 0 {
		if err := k.verifyToken(ctx, accessToken); err != nil {
			return nil, err
		}
	}
	return k.fetchUser(ctx, accessToken)
}

func (k *KakaoClient) verifyToken(ctx context.Context, accessToken string) error {
	var info kakaoTokenInfo
	if err := k.get(ctx, k.tokenInfoURL, accessToken, &info); err != nil {
		return fmt.Errorf("카카오 토큰 정보 조회: %w", err)
	}
	if info.AppID != k.appID {
		return fmt.Errorf("%w: app_id=%d, 우리 앱=%d", ErrKakaoForeignToken, info.AppID, k.appID)
	}
	return nil
}

func (k *KakaoClient) fetchUser(ctx context.Context, accessToken string) (*KakaoUser, error) {
	var resp kakaoUserResponse
	if err := k.get(ctx, k.userMeURL, accessToken, &resp); err != nil {
		return nil, fmt.Errorf("카카오 사용자 정보 조회: %w", err)
	}
	if resp.ID == 0 {
		return nil, fmt.Errorf("카카오 응답에 회원번호가 없음")
	}

	user := &KakaoUser{Sub: strconv.FormatInt(resp.ID, 10)}
	acc := resp.KakaoAccount
	if acc == nil {
		return user, nil
	}

	if acc.Email != "" && acc.IsEmailVerified {
		user.Email = &acc.Email
	}
	if acc.Profile != nil {
		if acc.Profile.Nickname != "" {
			user.Nickname = &acc.Profile.Nickname
		}
		if acc.Profile.ProfileImageURL != "" {
			user.ProfileImage = &acc.Profile.ProfileImageURL
		}
	}
	return user, nil
}

func (k *KakaoClient) get(ctx context.Context, url, accessToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("요청 생성: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("상태 %d: %s", resp.StatusCode, readErrBody(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("응답 디코딩: %w", err)
	}
	return nil
}

type kakaoTokenInfo struct {
	ID        int64 `json:"id"`
	AppID     int64 `json:"app_id"`
	ExpiresIn int   `json:"expires_in"`
}

type kakaoUserResponse struct {
	ID           int64         `json:"id"`
	KakaoAccount *kakaoAccount `json:"kakao_account"`
}

type kakaoAccount struct {
	Profile         *kakaoProfile `json:"profile"`
	Email           string        `json:"email"`
	IsEmailVerified bool          `json:"is_email_verified"`
}

type kakaoProfile struct {
	Nickname        string `json:"nickname"`
	ProfileImageURL string `json:"profile_image_url"`
}
