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

// ErrKakaoForeignToken 은 다른 앱용으로 발급된 카카오 토큰이 들어온 경우다.
var ErrKakaoForeignToken = errors.New("다른 앱에서 발급된 카카오 토큰")

// KakaoUser 는 카카오 로그인으로 알아낸 사용자 정보다.
type KakaoUser struct {
	// Sub 는 카카오 회원번호. 숫자지만 문자열로 다룬다(users.provider_sub).
	Sub string
	// 아래 셋은 사용자가 제공에 동의하지 않으면 없다.
	Email        *string
	Nickname     *string
	ProfileImage *string
}

// KakaoClient 는 앱의 카카오 SDK 가 받아온 액세스 토큰으로 사용자 정보를 조회한다.
//
// 앱(iOS/Android)에서는 카카오 SDK 가 로그인을 처리하고 액세스 토큰을 준다.
// 서버는 그 토큰을 그대로 받아 카카오에 사용자 정보를 물어보면 된다.
// 웹에서 쓰는 authorization code 교환 과정은 앱 플로우에 없다.
type KakaoClient struct {
	// appID 는 우리 카카오 앱의 앱 ID. 0 이면 토큰 출처 검사를 건너뛴다.
	appID int64

	userMeURL    string
	tokenInfoURL string
	httpClient   *http.Client
}

// NewKakaoClient 를 만든다. appID 는 카카오 developers 의 "앱 ID" 다.
// 0 을 넘기면 토큰 출처 검사를 하지 않는다(권장하지 않음. GetUser 주석 참고).
func NewKakaoClient(appID int64) *KakaoClient {
	return &KakaoClient{
		appID:        appID,
		userMeURL:    kakaoUserMeURL,
		tokenInfoURL: kakaoTokenInfoURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// SetEndpoints 는 카카오 엔드포인트 주소를 바꾼다. 테스트용.
func (k *KakaoClient) SetEndpoints(userMeURL, tokenInfoURL string) {
	k.userMeURL, k.tokenInfoURL = userMeURL, tokenInfoURL
}

// GetUser 는 액세스 토큰으로 카카오 사용자 정보를 가져온다.
//
// 먼저 토큰이 "우리 앱" 것인지 확인한다. 카카오 액세스 토큰은 그냥 문자열이라
// 그 자체로는 어느 앱에서 발급됐는지 알 수 없다. 확인하지 않으면 공격자가 자기
// 앱에서 발급받은 토큰을 우리 서버로 보내 계정을 만들 수 있다.
// access_token_info 가 돌려주는 app_id 를 우리 앱 ID 와 대조한다.
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

	// 인증되지 않은 이메일은 신뢰할 수 없으므로 저장하지 않는다.
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
