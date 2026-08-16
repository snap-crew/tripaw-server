package trip

import "time"

const dateFmt = "2006-01-02"

type CreateRequest struct {
	Title     string   `json:"title"`
	StartDate string   `json:"startDate"`
	EndDate   string   `json:"endDate"`
	PetIDs    []string `json:"petIds"`
	Themes    []string `json:"themes"`
}

type UpdateRequest struct {
	Title     *string   `json:"title"`
	StartDate *string   `json:"startDate"`
	EndDate   *string   `json:"endDate"`
	PetIDs    *[]string `json:"petIds"`
	Themes    *[]string `json:"themes"`
}

type AddStopsRequest struct {
	PlaceIDs []int64 `json:"placeIds"`
}

type ReorderRequest struct {
	PlaceIDs []int64 `json:"placeIds"`
}

type PetResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type StopResponse struct {
	Seq int `json:"seq"`

	Kind     string  `json:"kind"`
	PlaceID  int64   `json:"placeId"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
}

type DayResponse struct {
	DayNo int            `json:"dayNo"`
	Date  string         `json:"date"`
	Stops []StopResponse `json:"stops"`
}

type TripSummary struct {
	ID         string        `json:"id"`
	Title      *string       `json:"title"`
	StartDate  *string       `json:"startDate"`
	EndDate    *string       `json:"endDate"`
	DDay       *int          `json:"dDay"`
	PlaceCount int           `json:"placeCount"`
	Themes     []string      `json:"themes"`
	Pets       []PetResponse `json:"pets"`
}

type TripResponse struct {
	TripSummary
	TotalPlaceCount int           `json:"totalPlaceCount"`
	Days            []DayResponse `json:"days"`
}

type ListResponse struct {
	Featured   *TripSummary  `json:"featured"`
	Items      []TripSummary `json:"items"`
	NextOffset *int          `json:"nextOffset"`
}

func newPets(pets []Pet) []PetResponse {
	out := make([]PetResponse, 0, len(pets))
	for _, p := range pets {
		out = append(out, PetResponse{ID: p.ID.String(), Name: p.Name})
	}
	return out
}

func newSummary(t *Trip, today time.Time) TripSummary {
	s := TripSummary{
		ID:         t.ID.String(),
		Title:      t.Title,
		DDay:       t.DDay(today),
		PlaceCount: t.PlaceCount,
		Themes:     append([]string{}, t.Themes...),
		Pets:       newPets(t.Pets),
	}
	if t.StartDate != nil {
		v := t.StartDate.Format(dateFmt)
		s.StartDate = &v
	}
	if t.EndDate != nil {
		v := t.EndDate.Format(dateFmt)
		s.EndDate = &v
	}
	return s
}

func newTripResponse(t *Trip, today time.Time) TripResponse {
	days := make([]DayResponse, 0, len(t.Days))
	for _, d := range t.Days {
		stops := make([]StopResponse, 0, len(d.Stops))
		for _, s := range d.Stops {
			stops = append(stops, StopResponse{
				Seq: s.Seq, Kind: "place", PlaceID: s.PlaceID,
				Name: s.Name, Category: s.Category, Lat: s.Lat, Lng: s.Lng,
			})
		}
		days = append(days, DayResponse{
			DayNo: d.DayNo, Date: d.Date.Format(dateFmt), Stops: stops,
		})
	}

	return TripResponse{
		TripSummary:     newSummary(t, today),
		TotalPlaceCount: t.PlaceCount,
		Days:            days,
	}
}

func newListResponse(res *ListResult, today time.Time, offset, limit int) ListResponse {
	items := make([]TripSummary, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, newSummary(&res.Items[i], today))
	}

	out := ListResponse{Items: items}
	if res.Featured != nil {
		f := newSummary(res.Featured, today)
		out.Featured = &f
	}
	if res.HasMore {
		next := offset + limit
		out.NextOffset = &next
	}
	return out
}
