package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/snap-crew/tripaw-server/internal/oauth"
	"github.com/snap-crew/tripaw-server/internal/token"
	"github.com/google/uuid"
)

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

// LoginTest 는 확인 절차 없이 고정된 테스트 계정으로 로그인시킨다.
//
// 심사위원이 소셜 로그인 없이 앱을 보게 하려는 것이라 입력값을 받지 않는다.
// 열어둘지 말지는 라우트 등록 단계(TEST_LOGIN_ENABLED)에서 정한다.
func (s *Service) LoginTest(ctx context.Context) (*User, *token.Pair, error) {
	user, err := s.repo.UpsertTestLogin(ctx, testProviderSub, testNickname)
	if err != nil {
		return nil, nil, err
	}

	pair, err := s.issue(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	return user, pair, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (*token.Pair, error) {
	claimedUserID, err := s.tokens.ValidateRefresh(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRefreshToken, err)
	}

	storedUserID, err := s.repo.ConsumeRefreshToken(ctx, token.HashRefresh(refreshToken))
	if err != nil {
		return nil, err
	}

	if storedUserID != claimedUserID {
		return nil, fmt.Errorf("%w: 토큰의 사용자와 저장된 사용자가 다릅니다", ErrInvalidRefreshToken)
	}

	return s.issue(ctx, storedUserID)
}

func (s *Service) Logout(ctx context.Context, userID uuid.UUID) error {
	return s.repo.DeleteRefreshTokensByUser(ctx, userID)
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*User, error) {
	return s.repo.FindUserByID(ctx, userID)
}

func (s *Service) UpdateMe(
	ctx context.Context, userID uuid.UUID, nickname *string, img *string, imageSet bool,
) (*User, error) {
	if nickname != nil {
		trimmed := strings.TrimSpace(*nickname)
		if trimmed == "" || len([]rune(trimmed)) > 20 {
			return nil, ErrInvalidNickname
		}
		nickname = &trimmed
	}

	if imageSet && img != nil && *img != "" {
		rest, ok := strings.CutPrefix(*img, imagePathPrefix)
		if !ok {
			return nil, ErrInvalidProfileImage
		}
		if _, err := uuid.Parse(rest); err != nil {
			return nil, ErrInvalidProfileImage
		}
	}

	return s.repo.UpdateProfile(ctx, userID, nickname, img, imageSet)
}

func (s *Service) NextStep(ctx context.Context, userID uuid.UUID) (string, error) {
	return s.repo.ResolveNextStep(ctx, userID)
}

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

	return s.repo.DeleteUser(ctx, userID)
}

func (s *Service) CleanupExpiredTokens(ctx context.Context) (int64, error) {
	return s.repo.DeleteExpiredRefreshTokens(ctx)
}

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
