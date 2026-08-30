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

	ImageURL   *string `json:"imageUrl"`
	ImageCount int     `json:"imageCount"`
	OpenTime   *string `json:"openTime,omitempty"`

	PetConditions []string `json:"petConditions"`

	NeedsVerification bool `json:"needsVerification"`

	IsSaved bool `json:"isSaved"`

	DistanceM *float64 `json:"distanceM,omitempty"`
}

type PlaceDetailResponse struct {
	PlaceResponse
	Images []string `json:"images"`
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
		ImageCount:        p.ImageCount,
		OpenTime:          p.OpenTime,
		PetConditions:     petConditions(p),
		NeedsVerification: p.NeedsVerification,
		IsSaved:           p.IsSaved,
		DistanceM:         p.Distance,
	}
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
