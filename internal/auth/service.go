package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/daewon/tripaw-server/internal/oauth"
	"github.com/daewon/tripaw-server/internal/token"
	"github.com/google/uuid"
)

// Service 는 로그인 관련 처리를 모아둔다.
type Service struct {
	repo   *Repository
	tokens *token.Manager
	apple  *oauth.AppleClient
	kakao  *oauth.KakaoClient
}

func NewService(
	repo *Repository,
	tokens *token.Manager,
	apple *oauth.AppleClient,
	kakao *oauth.KakaoClient,
) *Service {
	return &Service{repo: repo, tokens: tokens, apple: apple, kakao: kakao}
}

// LoginApple 은 앱이 받아온 애플 authorization code 로 로그인한다.
// 처음이면 가입까지 함께 처리한다.
//
// nickname 은 클라이언트가 넘겨준 표시 이름이다. 애플은 id_token 에 이름을
// 담지 않고 최초 인증 때 클라이언트에만 주기 때문에, 서버가 스스로 알아낼 방법이
// 없어서 받아둔다. 두 번째 로그인부터는 nil 로 와도 기존 값이 유지된다.
func (s *Service) LoginApple(ctx context.Context, code string, nickname *string) (*User, *token.Pair, error) {
	info, err := s.apple.ExchangeCode(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrProviderRejected, err)
	}

	return s.loginWith(ctx, &User{
		Provider:    ProviderApple,
		ProviderSub: info.Sub,
		Email:       info.Email,
		Nickname:    normalizeName(nickname),
	})
}

// normalizeName 은 클라이언트가 보낸 이름을 다듬는다.
// 애플 fullName 은 사용자가 이름 제공에 동의하지 않으면 빈 문자열로 조립되어
// 오는 경우가 있다. 빈 값을 저장하면 "이름 있음" 으로 취급되어 나중에 진짜
// 이름이 와도 COALESCE 가 덮지 않는다.
func normalizeName(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// LoginKakao 는 앱의 카카오 SDK 가 받아온 액세스 토큰으로 로그인한다.
func (s *Service) LoginKakao(ctx context.Context, accessToken string) (*User, *token.Pair, error) {
	info, err := s.kakao.GetUser(ctx, accessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrProviderRejected, err)
	}

	return s.loginWith(ctx, &User{
		Provider:     ProviderKakao,
		ProviderSub:  info.Sub,
		Email:        info.Email,
		Nickname:     normalizeName(info.Nickname),
		ProfileImage: info.ProfileImage,
	})
}

// Refresh 는 리프레시 토큰을 새 토큰 한 쌍으로 바꾼다(rotation).
// 쓴 토큰은 즉시 무효가 되므로, 같은 토큰을 두 번 쓰면 두 번째는 실패한다.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*token.Pair, error) {
	// 먼저 서명과 종류를 본다. 여기서 액세스 토큰이 걸러진다.
	// 형식이 틀린 토큰으로 DB 를 두드리지 않는 효과도 있다.
	claimedUserID, err := s.tokens.ValidateRefresh(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRefreshToken, err)
	}

	// DB 에서 꺼내면서 동시에 없앤다. 여기를 통과한 요청만 재발급을 받는다.
	storedUserID, err := s.repo.ConsumeRefreshToken(ctx, token.HashRefresh(refreshToken))
	if err != nil {
		return nil, err
	}

	// 서명은 맞는데 저장된 주인이 다르다면 뭔가 잘못된 것이다. 발급하지 않는다.
	if storedUserID != claimedUserID {
		return nil, fmt.Errorf("%w: 토큰의 사용자와 저장된 사용자가 다릅니다", ErrInvalidRefreshToken)
	}

	return s.issue(ctx, storedUserID)
}

// Logout 은 그 사용자의 리프레시 토큰을 전부 지운다.
//
// 이미 발급된 액세스 토큰은 만료될 때까지(기본 1시간) 유효하다. 서버에 상태를
// 두지 않는 JWT 의 특성이라, 즉시 차단이 필요해지면 별도의 차단 목록이 필요하다.
func (s *Service) Logout(ctx context.Context, userID uuid.UUID) error {
	return s.repo.DeleteRefreshTokensByUser(ctx, userID)
}

// Me 는 현재 로그인한 사용자를 돌려준다.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*User, error) {
	return s.repo.FindUserByID(ctx, userID)
}

// DeleteAccount 는 탈퇴를 처리한다.
//
// 애플 사용자는 appleCode 가 필요하다. 애플은 "앱에서 탈퇴하면 설정 > Apple ID 의
// 연동도 끊겨야 한다" 는 심사 기준이 있어서, 클라이언트가 탈퇴 요청에 새 authorization
// code 를 함께 보내야 우리가 애플에 revoke 를 호출할 수 있다.
//
// revoke 가 실패하면 사용자를 지우지 않는다. 우리 쪽만 지우면 애플에는 연동이
// 남아서, 다시 로그인해도 애플이 이메일을 주지 않는 상태가 되기 때문이다.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID, appleCode string) error {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}

	if user.Provider == ProviderApple {
		if appleCode == "" {
			return ErrAppleCodeRequired
		}
		if err := s.apple.RevokeByCode(ctx, appleCode); err != nil {
			return fmt.Errorf("애플 연동 해제: %w", err)
		}
	}

	// refresh_tokens 는 ON DELETE CASCADE 로 함께 지워진다.
	return s.repo.DeleteUser(ctx, userID)
}

// CleanupExpiredTokens 는 만료된 리프레시 토큰을 지운다. 주기 작업에서 호출한다.
func (s *Service) CleanupExpiredTokens(ctx context.Context) (int64, error) {
	return s.repo.DeleteExpiredRefreshTokens(ctx)
}

// loginWith 는 공급자에서 확인이 끝난 사용자로 가입/로그인을 마무리한다.
func (s *Service) loginWith(ctx context.Context, incoming *User) (*User, *token.Pair, error) {
	user, err := s.repo.UpsertOnLogin(ctx, incoming)
	if err != nil {
		return nil, nil, err
	}

	pair, err := s.issue(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	return user, pair, nil
}

// issue 는 토큰 한 쌍을 만들고 리프레시 토큰을 해시로 저장한다.
func (s *Service) issue(ctx context.Context, userID uuid.UUID) (*token.Pair, error) {
	pair, err := s.tokens.GeneratePair(userID)
	if err != nil {
		return nil, err
	}

	err = s.repo.StoreRefreshToken(
		ctx, userID, token.HashRefresh(pair.RefreshToken), pair.RefreshExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return pair, nil
}
