package trip

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const MaxTitleLen = 20

const DuplicateSuffix = " (복제)"

type Pet struct {
	ID   uuid.UUID
	Name string
}

type Stop struct {
	Seq      int
	PlaceID  int64
	Name     string
	Category string
	Lat, Lng float64
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

var (
	ErrNotFound  = errors.New("여행을 찾을 수 없습니다")
	ErrForbidden = errors.New("다른 사용자의 여행입니다")

	ErrStopsOutsideRange = errors.New("줄어드는 기간에 일정이 남아 있습니다")

	ErrDayOutOfRange = errors.New("여행 기간에 없는 일차입니다")
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
