package trip

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const MaxTitleLen = 20

const DuplicateSuffix = " (복제)"

type Pet struct {
	ID     uuid.UUID
	Name   string
	Size   string
	Traits []string
}

// 출발지. 클라이언트가 카카오 지도에서 고른 위치다.
type Origin struct {
	Lat, Lng float64
	Name     *string
}

type Candidate struct {
	ID          int64
	Name        string
	Category    string
	RoadAddress *string
	Lat, Lng    float64
}

type Stop struct {
	Seq      int
	PlaceID  int64
	Name     string
	Category string
	Lat, Lng float64

	ImageURL, ImageThumbURL, ImageAttribution *string
}

type Day struct {
	DayNo int
	Date  time.Time
	Stops []Stop
}

type Trip struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Title      *string
	StartDate  *time.Time
	EndDate    *time.Time
	Themes     []string
	Origin     *Origin
	PlaceCount int
	Pets       []Pet
	Days       []Day
	CreatedAt  time.Time
}

func (t *Trip) DDay(today time.Time) *int {
	if t.StartDate == nil {
		return nil
	}
	d := int(today.Sub(*t.StartDate).Hours() / 24)
	return &d
}

// 수집 장소가 위도 33.119~33.564 · 경도 126.169~126.967 에 들어 있다.
// 추자도(위도 33.95)까지 담고 육지는 걸러지도록 여유를 둔 범위다.
const (
	minOriginLat, maxOriginLat = 32.9, 34.1
	minOriginLng, maxOriginLng = 125.9, 127.1

	MaxOriginNameLen = 100
)

var (
	ErrNotFound  = errors.New("여행을 찾을 수 없습니다")
	ErrForbidden = errors.New("다른 사용자의 여행입니다")

	ErrStopsOutsideRange = errors.New("줄어드는 기간에 일정이 남아 있습니다")

	ErrDayOutOfRange = errors.New("여행 기간에 없는 일차입니다")

	ErrTripNotEmpty = errors.New("이미 일정이 있는 여행입니다")
	ErrNoCandidates = errors.New("추천할 장소가 없습니다")
)

type ValidationError struct {
	Code   string
	Detail string
}

func (e *ValidationError) Error() string { return e.Detail }

func invalid(code, detail string) error {
	return &ValidationError{Code: code, Detail: detail}
}

func truncateTitle(s string) string {
	r := []rune(s)
	if len(r) <= MaxTitleLen {
		return s
	}
	return string(r[:MaxTitleLen])
}
