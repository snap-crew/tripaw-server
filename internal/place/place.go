package place

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
)

type Filter struct {
	Category    string
	MaxWeightKg *float64
	Area        string
	MaxFeeKrw   *int
	Leash       *bool
	Muzzle      *bool
	Parking     bool
	WasteBag    bool
}

type BBox struct {
	MinLat, MaxLat, MinLng, MaxLng float64
}

type Marker struct {
	PlaceID  *int64
	Name     *string
	Category *string
	Lat, Lng float64
	Count    int
}

type Place struct {
	ID          int64
	Name        string
	Category    string
	RoadAddress *string
	Tel         *string
	Lat, Lng    float64
	ImageURL    *string
	ImageCount  int

	Status              string
	Area                *string
	SizeLimit           string
	MaxWeightKg         *float64
	LeashRequired       *bool
	MuzzleRequired      *bool
	MuzzleDangerousOnly *bool
	CrateRequired       *bool
	WasteBagRequired    *bool
	ExtraFeeKrw         *int
	ParkingAvailable    *bool
	NeedsVerification   bool

	OpenTime    *string
	HomepageURL *string

	IsSaved  bool
	Distance *float64

	sortKey any
}

type SavedPlace struct {
	Place
	SavedAt string
}

type CategoryCount struct {
	Category string
	Count    int
}

// 등록 화면은 몸무게를 숫자로 받지 않고 구간 3개만 받는다. 장소의 무게 제한과
// 비교하려면 구간 안에서 가장 무거울 수 있는 값을 써야 한다.
//
//	소형  10kg 미만    → 10kg 에 근접할 수 있으므로 허용치가 10 이상이어야 통과
//	중형  10~25kg      → 25
//	대형  25kg 초과    → 상한이 없다. 무게를 명시한 곳은 통과시키지 않는다
var petBandMaxKg = map[string]float64{"small": 10, "medium": 25, "large": unboundedKg}

// 어떤 실제 무게 제한보다도 큰 값. 대형견이 무게 제한 있는 곳을 통과하지 못하게 한다.
const unboundedKg = 100000

var sizeRank = map[string]int{"small": 1, "medium": 2, "large": 3}

// 여러 마리를 데려가면 전부 들어갈 수 있어야 하므로 가장 큰 개를 기준으로 본다.
func LargestSize(sizes []string) string {
	out := ""
	for _, s := range sizes {
		if sizeRank[s] > sizeRank[out] {
			out = s
		}
	}
	return out
}

var (
	ErrNotFound      = errors.New("장소를 찾을 수 없습니다")
	ErrInvalidCursor = errors.New("커서가 올바르지 않습니다")
	ErrUnknownPet    = errors.New("등록되지 않은 반려동물입니다")
)

var validCategory = map[string]bool{
	"attraction": true, "cafe": true, "restaurant": true, "stay": true,
	"culture": true, "leisure": true, "shop": true, "vet": true,
	"pharmacy": true, "park": true, "beach": true, "other": true,
}

func clusterCellDeg(zoom int) float64 {
	if zoom < 1 {
		zoom = 1
	}
	if zoom > 20 {
		zoom = 20
	}
	return 360.0 * 60.0 / (256.0 * math.Pow(2, float64(zoom)))
}

type cursor struct {
	Num float64 `json:"n,omitempty"`
	Str string  `json:"s,omitempty"`
	ID  int64   `json:"id"`
}

func (c cursor) encode() string {
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (*cursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, ErrInvalidCursor
	}
	return &c, nil
}

func nextCursor(items []Place, limit int) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	c := cursor{ID: last.ID}
	switch v := last.sortKey.(type) {
	case float64:
		c.Num = v
	case string:
		c.Str = v
	}
	return c.encode()
}

func nextSavedCursor(items []SavedPlace, limit int) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	return cursor{Str: last.SavedAt, ID: last.ID}.encode()
}
