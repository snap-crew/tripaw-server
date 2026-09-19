package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv      string
	Port        string
	DatabaseURL string

	DataGoKrKey  string
	KakaoRESTKey string
	GeminiKey    string
	GeminiModel  string

	// 심사위원용 테스트 계정 로그인(POST /api/auth/test) 을 열지 여부.
	TestLogin bool

	JWT   JWTConfig
	Apple AppleConfig
	Kakao KakaoConfig
}

type JWTConfig struct {
	Secret string

	AccessExpiry  time.Duration
	RefreshExpiry time.Duration
}

type AppleConfig struct {
	ClientID string

	TeamID string

	KeyID string

	PrivateKey string

	RedirectURI string
}

type KakaoConfig struct {
	AppID int64
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	accessExpiry, err := parseDuration("JWT_ACCESS_EXPIRY", "1h")
	if err != nil {
		return nil, err
	}
	refreshExpiry, err := parseDuration("JWT_REFRESH_EXPIRY", "720h")
	if err != nil {
		return nil, err
	}

	kakaoAppID, err := parseInt64("KAKAO_APP_ID")
	if err != nil {
		return nil, err
	}

	c := &Config{
		AppEnv:      getEnv("APP_ENV", "development"),
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),

		DataGoKrKey:  os.Getenv("DATA_GO_KR_SERVICE_KEY"),
		KakaoRESTKey: os.Getenv("KAKAO_REST_API_KEY"),
		GeminiKey:    os.Getenv("GEMINI_API_KEY"),
		GeminiModel:  getEnv("GEMINI_MODEL", "gemini-3.1-flash-lite"),

		JWT: JWTConfig{
			Secret:        os.Getenv("JWT_SECRET"),
			AccessExpiry:  accessExpiry,
			RefreshExpiry: refreshExpiry,
		},
		Apple: AppleConfig{
			ClientID:    os.Getenv("APPLE_CLIENT_ID"),
			TeamID:      os.Getenv("APPLE_TEAM_ID"),
			KeyID:       os.Getenv("APPLE_KEY_ID"),
			PrivateKey:  os.Getenv("APPLE_PRIVATE_KEY"),
			RedirectURI: os.Getenv("APPLE_REDIRECT_URI"),
		},
		Kakao:     KakaoConfig{AppID: kakaoAppID},
		TestLogin: getEnv("TEST_LOGIN_ENABLED", "false") == "true",
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL 이 설정되지 않았습니다")
	}
	return c, nil
}

func (c *Config) RequireDataGoKr() error {
	if c.DataGoKrKey == "" {
		return fmt.Errorf("DATA_GO_KR_SERVICE_KEY 가 설정되지 않았습니다")
	}
	return nil
}

func (c *Config) RequireAuth() error {
	if len(c.JWT.Secret) < 32 {
		return fmt.Errorf("JWT_SECRET 이 %d바이트입니다. 32바이트 이상이어야 합니다 (openssl rand -base64 32)", len(c.JWT.Secret))
	}

	missing := make([]string, 0, 4)
	for name, v := range map[string]string{
		"APPLE_CLIENT_ID":   c.Apple.ClientID,
		"APPLE_TEAM_ID":     c.Apple.TeamID,
		"APPLE_KEY_ID":      c.Apple.KeyID,
		"APPLE_PRIVATE_KEY": c.Apple.PrivateKey,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("애플 로그인 설정이 비어 있습니다: %v", missing)
	}
	return nil
}

func (c *Config) IsProduction() bool { return c.AppEnv == "production" }

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDuration(key, fallback string) (time.Duration, error) {
	raw := getEnv(key, fallback)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q 를 기간으로 읽을 수 없습니다 (예: 1h, 30m, 720h): %w", key, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s 는 0보다 커야 합니다 (현재 %s)", key, d)
	}
	return d, nil
}

func parseInt64(key string) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q 를 숫자로 읽을 수 없습니다: %w", key, raw, err)
	}
	return n, nil
}
