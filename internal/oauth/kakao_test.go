package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

const testKakaoAppID = 1234567

func kakaoStub(t *testing.T, appID int64, userBody string) *KakaoClient {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/user/access_token_info", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer kakao-access-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"app_id":` + strconv.FormatInt(appID, 10) + `,"expires_in":21599}`))
	})
	mux.HandleFunc("/v2/user/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(userBody))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewKakaoClient(testKakaoAppID)
	c.SetEndpoints(srv.URL+"/v2/user/me", srv.URL+"/v1/user/access_token_info")
	return c
}

func TestKakaoGetUser(t *testing.T) {
	c := kakaoStub(t, testKakaoAppID, `{
		"id": 987654321,
		"kakao_account": {
			"email": "user@kakao.com",
			"is_email_verified": true,
			"profile": {"nickname": "카카오유저", "profile_image_url": "https://img/p.jpg"}
		}
	}`)

	user, err := c.GetUser(context.Background(), "kakao-access-token")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	if user.Sub != "987654321" {
		t.Errorf("Sub = %q, want 987654321", user.Sub)
	}
	if user.Email == nil || *user.Email != "user@kakao.com" {
		t.Errorf("Email = %v", user.Email)
	}
	if user.Nickname == nil || *user.Nickname != "카카오유저" {
		t.Errorf("Nickname = %v", user.Nickname)
	}
	if user.ProfileImage == nil || *user.ProfileImage != "https://img/p.jpg" {
		t.Errorf("ProfileImage = %v", user.ProfileImage)
	}
}

func TestKakaoRejectsForeignAppToken(t *testing.T) {
	c := kakaoStub(t, 9999999, `{"id":1}`)

	_, err := c.GetUser(context.Background(), "kakao-access-token")
	if !errors.Is(err, ErrKakaoForeignToken) {
		t.Fatalf("err = %v, want ErrKakaoForeignToken", err)
	}
}

func TestKakaoSkipsVerificationWhenAppIDUnset(t *testing.T) {
	c := kakaoStub(t, 9999999, `{"id":555}`)
	c.appID = 0

	user, err := c.GetUser(context.Background(), "kakao-access-token")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Sub != "555" {
		t.Errorf("Sub = %q, want 555", user.Sub)
	}
}

func TestKakaoOmitsUnverifiedEmail(t *testing.T) {
	c := kakaoStub(t, testKakaoAppID, `{
		"id": 1,
		"kakao_account": {"email": "unverified@kakao.com", "is_email_verified": false}
	}`)

	user, err := c.GetUser(context.Background(), "kakao-access-token")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Email != nil {
		t.Errorf("미인증 이메일이 저장됨: %q", *user.Email)
	}
}

func TestKakaoMinimalConsent(t *testing.T) {
	c := kakaoStub(t, testKakaoAppID, `{"id": 777}`)

	user, err := c.GetUser(context.Background(), "kakao-access-token")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Sub != "777" {
		t.Errorf("Sub = %q", user.Sub)
	}
	if user.Email != nil || user.Nickname != nil || user.ProfileImage != nil {
		t.Error("동의하지 않은 항목이 채워짐")
	}
}

func TestKakaoRejectsExpiredToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/user/access_token_info", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":-401,"msg":"this access token does not exist"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewKakaoClient(testKakaoAppID)
	c.SetEndpoints(srv.URL+"/v2/user/me", srv.URL+"/v1/user/access_token_info")

	if _, err := c.GetUser(context.Background(), "expired"); err == nil {
		t.Fatal("만료된 토큰이 통과함")
	}
}

func TestKakaoRejectsEmptyUserID(t *testing.T) {
	c := kakaoStub(t, testKakaoAppID, `{}`)

	if _, err := c.GetUser(context.Background(), "kakao-access-token"); err == nil {
		t.Fatal("회원번호 없는 응답이 통과함")
	}
}
