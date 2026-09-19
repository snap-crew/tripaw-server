package place

import "strconv"

type MarkerResponse struct {
	Type     string  `json:"type"`
	PlaceID  *int64  `json:"placeId,omitempty"`
	Name     *string `json:"name,omitempty"`
	Category *string `json:"category,omitempty"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Count    int     `json:"count,omitempty"`
}

type MapResponse struct {
	Markers []MarkerResponse `json:"markers"`
	Total   int              `json:"total"`
}

type PlaceResponse struct {
	PlaceID     int64   `json:"placeId"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	RoadAddress *string `json:"roadAddress"`
	Tel         *string `json:"tel,omitempty"`
	HomepageURL *string `json:"homepageUrl,omitempty"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`

	ImageURL         *string `json:"imageUrl"`
	ImageThumbURL    *string `json:"imageThumbUrl"`
	ImageAttribution *string `json:"imageAttribution"`
	ImageCount       int     `json:"imageCount"`

	// 대표 분류 외에 필터에 걸리는 분류 (예: 관광지인 한림공원의 park).
	ExtraCategories []string `json:"extraCategories"`

	OpenTime    *string `json:"openTime,omitempty"`
	RestDate    *string `json:"restDate,omitempty"`
	ParkingNote *string `json:"parkingNote,omitempty"`
	MenuSummary *string `json:"menuSummary,omitempty"`

	// allowed = 동반 가능, partial = 일부 구역·조건부 동반.
	PetStatus     string   `json:"petStatus"`
	PetConditions []string `json:"petConditions"`

	NeedsVerification bool `json:"needsVerification"`

	IsSaved bool `json:"isSaved"`

	DistanceM *float64 `json:"distanceM,omitempty"`
}

type PlaceDetailResponse struct {
	PlaceResponse
	Images       []string        `json:"images"`
	ImageDetails []ImageResponse `json:"imageDetails"`
	PetPolicy    *PetPolicy      `json:"petPolicy"`
	Hours        *HoursResponse  `json:"hours"`
}

type ImageResponse struct {
	URL         string  `json:"url"`
	ThumbURL    *string `json:"thumbUrl"`
	Attribution *string `json:"attribution"`
}

// PetPolicy 는 동반 조건을 판단한 근거다. 조건 문구(petConditions)가 원문의 어느
// 문장에서 나왔는지, 언제 어느 출처 기준인지 사용자가 확인할 수 있게 한다.
type PetPolicy struct {
	Evidence *string `json:"evidence"`
	Source   *string `json:"source"`
	DatedAt  *string `json:"datedAt"`
}

// HoursResponse 는 openTime 문자열을 구조화한 값이다.
// kind: business_hours | checkin_checkout | always_open | varies | unknown
// openDays 는 1=월 … 7=일(ISO 8601), openMin·closeMin 은 자정부터의 분이다.
type HoursResponse struct {
	Kind            string  `json:"kind"`
	OpenDays        []int16 `json:"openDays"`
	OpenMin         *int16  `json:"openMin"`
	CloseMin        *int16  `json:"closeMin"`
	CrossesMidnight bool    `json:"crossesMidnight"`
}

type ListResponse struct {
	Items      []PlaceResponse `json:"items"`
	Total      int             `json:"total,omitempty"`
	NextCursor *string         `json:"nextCursor"`
}

type SearchResponse struct {
	Items []PlaceResponse `json:"items"`
}

type SavedPlaceResponse struct {
	PlaceResponse
	SavedAt string `json:"savedAt"`
}

type SavedListResponse struct {
	Items      []SavedPlaceResponse `json:"items"`
	NextCursor *string              `json:"nextCursor"`
}

type CategoryCountResponse struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

type CategoriesResponse struct {
	Total int                     `json:"total"`
	Items []CategoryCountResponse `json:"items"`
}

type SaveRequest struct {
	PlaceID int64 `json:"placeId"`
}

func newMapResponse(markers []Marker, total int) MapResponse {
	out := make([]MarkerResponse, 0, len(markers))
	for _, m := range markers {
		r := MarkerResponse{Lat: m.Lat, Lng: m.Lng}
		if m.Count > 1 {
			r.Type, r.Count = "cluster", m.Count
		} else {
			r.Type = "place"
			r.PlaceID, r.Name, r.Category = m.PlaceID, m.Name, m.Category
		}
		out = append(out, r)
	}
	return MapResponse{Markers: out, Total: total}
}

func newPlaceResponse(p *Place) PlaceResponse {
	return PlaceResponse{
		PlaceID:           p.ID,
		Name:              p.Name,
		Category:          p.Category,
		RoadAddress:       p.RoadAddress,
		Tel:               p.Tel,
		HomepageURL:       p.HomepageURL,
		Lat:               p.Lat,
		Lng:               p.Lng,
		ImageURL:          p.ImageURL,
		ImageThumbURL:     p.ImageThumbURL,
		ImageAttribution:  p.ImageAttribution,
		ImageCount:        p.ImageCount,
		ExtraCategories:   append([]string{}, p.ExtraCategories...),
		OpenTime:          p.OpenTime,
		RestDate:          p.RestDate,
		ParkingNote:       p.ParkingNote,
		MenuSummary:       p.MenuSummary,
		PetStatus:         p.Status,
		PetConditions:     petConditions(p),
		NeedsVerification: p.NeedsVerification,
		IsSaved:           p.IsSaved,
		DistanceM:         p.Distance,
	}
}

func newPlaceDetail(p *Place, images []Image) PlaceDetailResponse {
	out := PlaceDetailResponse{
		PlaceResponse: newPlaceResponse(p),
		Images:        make([]string, 0, len(images)),
		ImageDetails:  make([]ImageResponse, 0, len(images)),
	}
	for _, img := range images {
		out.Images = append(out.Images, img.URL)
		out.ImageDetails = append(out.ImageDetails,
			ImageResponse{URL: img.URL, ThumbURL: img.ThumbURL, Attribution: img.Attribution})
	}
	if p.PolicyEvidence != nil || p.PolicySource != nil {
		out.PetPolicy = &PetPolicy{
			Evidence: p.PolicyEvidence, Source: sourceLabel(p.PolicySource), DatedAt: p.PolicyDatedAt,
		}
	}
	if p.HoursKind != nil {
		out.Hours = &HoursResponse{
			Kind: *p.HoursKind, OpenDays: append([]int16{}, p.OpenDays...),
			OpenMin: p.OpenMin, CloseMin: p.CloseMin, CrossesMidnight: p.CrossesMidnight,
		}
	}
	return out
}

// sourceLabel 은 내부 출처 코드를 화면에 보여 줄 기관명으로 바꾼다.
func sourceLabel(src *string) *string {
	if src == nil {
		return nil
	}
	names := map[string]string{
		"kto_pet": "한국관광공사", "kto_common": "한국관광공사", "kto_related": "한국관광공사",
		"kcisa_csv": "한국문화정보원", "visitjeju_api": "비짓제주", "visitjeju_stay": "비짓제주",
		"kakao_local": "카카오",
	}
	if n, ok := names[*src]; ok {
		return &n
	}
	return src
}

func newPlaceList(items []Place) []PlaceResponse {
	out := make([]PlaceResponse, 0, len(items))
	for i := range items {
		out = append(out, newPlaceResponse(&items[i]))
	}
	return out
}

func petConditions(p *Place) []string {
	out := []string{}

	switch p.SizeLimit {
	case "small":
		out = append(out, "소형견")
	case "medium":
		out = append(out, "중형견까지")
	case "large":
		out = append(out, "대형견")
	case "any":
		out = append(out, "크기 제한 없음")
	}

	if p.MaxWeightKg != nil {
		out = append(out, strconv.FormatFloat(*p.MaxWeightKg, 'f', -1, 64)+"kg 이하")
	}
	if p.Area != nil {
		switch *p.Area {
		case "indoor":
			out = append(out, "실내 동반")
		case "outdoor":
			out = append(out, "야외 동반")
		case "both":
			out = append(out, "실내외 동반")
		}
	}
	if p.LeashRequired != nil && *p.LeashRequired {
		out = append(out, "리드줄 필수")
	}
	if p.MuzzleRequired != nil && *p.MuzzleRequired {
		if p.MuzzleDangerousOnly != nil && *p.MuzzleDangerousOnly {
			out = append(out, "맹견 입마개 필수")
		} else {
			out = append(out, "입마개 필수")
		}
	}
	if p.CrateRequired != nil && *p.CrateRequired {
		out = append(out, "케이지 필수")
	}
	if p.WasteBagRequired != nil && *p.WasteBagRequired {
		out = append(out, "배변봉투 지참")
	}
	if p.VaccinationRequired != nil && *p.VaccinationRequired {
		out = append(out, "예방접종 필수")
	}
	if p.ExtraFeeKrw != nil {
		if *p.ExtraFeeKrw == 0 {
			out = append(out, "동반요금 없음")
		} else {
			out = append(out, strconv.Itoa(*p.ExtraFeeKrw)+"원")
		}
	}
	if p.ParkingAvailable != nil && *p.ParkingAvailable {
		out = append(out, "주차 가능")
	}

	return out
}

func cursorPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
