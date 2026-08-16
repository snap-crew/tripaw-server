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

	// 수집 파이프라인에서 쓰던 키들. 수집이 끝나 지금은 서버가 쓰지 않는다.
	DataGoKrKey    string
	KakaoRESTKey   string
	AnthropicKey   string
	AnthropicModel string

	JWT   JWTConfig
	Apple AppleConfig
	Kakao KakaoConfig
}

type JWTConfig struct {
	Secret string
	// 액세스 토큰은 짧게, 리프레시 토큰은 길게. 액세스가 새어도 피해 시간이 짧도록.
	AccessExpiry  time.Duration
	RefreshExpiry time.Duration
}

// AppleConfig 는 애플 개발자 사이트에서 발급받는 값들이다.
type AppleConfig struct {
	// ClientID 는 iOS 앱의 번들 ID (예: com.tripaw.app).
	ClientID string
	// TeamID 는 Membership 의 10자리 팀 ID.
	TeamID string
	// KeyID 는 Sign in with Apple 용 키(.p8)의 10자리 ID.
	KeyID string
	// PrivateKey 는 .p8 파일 내용(PEM). 환경변수에 한 줄로 넣으면 \n 이스케이프도 처리한다.
	PrivateKey string
	// RedirectURI 는 앱 로그인에는 보통 필요 없다. 비워두면 전송하지 않는다.
	RedirectURI string
}

type KakaoConfig struct {
	// AppID 는 카카오 developers 의 "앱 ID"(숫자). 넘어온 액세스 토큰이 우리 앱
	// 것인지 확인하는 데 쓴다. 0 이면 그 검사를 건너뛴다.
	AppID int64
}

// Load 는 .env 를 읽어 설정을 만든다. 이미 프로세스 환경에 있는 값이 우선한다.
func Load() (*Config, error) {
	_ = godotenv.Load() // .env 가 없어도 환경변수만으로 동작 가능

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

		DataGoKrKey:    os.Getenv("DATA_GO_KR_SERVICE_KEY"),
		KakaoRESTKey:   os.Getenv("KAKAO_REST_API_KEY"),
		AnthropicKey:   os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel: getEnv("ANTHROPIC_MODEL", "claude-opus-5"),

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
		Kakao: KakaoConfig{AppID: kakaoAppID},
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL 이 설정되지 않았습니다")
	}
	return c, nil
}

// RequireDataGoKr 는 공공데이터포털 키가 필요한 명령에서 호출한다.
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

// IsProduction 은 운영 환경인지 알려준다. 로그 레벨·에러 노출 범위를 가른다.
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
