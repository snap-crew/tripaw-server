package pet

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	SpeciesDog = "dog"
	SpeciesCat = "cat"
)

const MaxPets = 5

// 등록 화면이 받는 몸무게 범위. 치와와 1kg 부터 대형견까지 담고,
// 오타(120 을 1200 으로)를 걸러낼 정도로만 잡는다.
const (
	MinWeightKg = 0.1
	MaxWeightKg = 150.0
)

var (
	allowedSpecies  = map[string]bool{SpeciesDog: true, SpeciesCat: true}
	allowedGender   = map[string]bool{"male": true, "female": true}
	allowedNeutered = map[string]bool{"done": true, "not_done": true, "unknown": true}
	allowedSize     = map[string]bool{"small": true, "medium": true, "large": true}
	allowedTraits   = map[string]bool{
		"active": true, "calm": true, "social": true, "timid": true, "curious": true,
	}
)

type Breed struct {
	ID       int
	Name     string
	ImageURL *string
}

type Pet struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Name      string
	Species   string
	BreedID   *int
	BreedName *string
	Size      string
	Gender    string
	Neutered  string
	Traits    []string
	PhotoURL  *string
	WeightKg  *float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Draft struct {
	Step      int
	Payload   []byte
	UpdatedAt time.Time
}

var (
	ErrNotFound      = errors.New("반려동물을 찾을 수 없습니다")
	ErrForbidden     = errors.New("다른 사용자의 반려동물입니다")
	ErrDraftNotFound = errors.New("임시 저장된 프로필이 없습니다")
	ErrLimitExceeded = errors.New("반려동물은 최대 5마리까지 등록할 수 있습니다")
)

type ValidationError struct {
	Code   string
	Detail string
}

func (e *ValidationError) Error() string { return e.Detail }

func invalid(code, detail string) error {
	return &ValidationError{Code: code, Detail: detail}
}
