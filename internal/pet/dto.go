package pet

import (
	"time"
)

type CreateRequest struct {
	Name    string   `json:"name"`
	Species string   `json:"species"`
	BreedID *int     `json:"breedId"`
	Size    string   `json:"size"`
	Traits  []string `json:"traits"`
}

type UpdateRequest struct {
	Name    *string   `json:"name"`
	Species *string   `json:"species"`
	BreedID *int      `json:"breedId"`
	Size    *string   `json:"size"`
	Traits  *[]string `json:"traits"`
}

type DraftRequest struct {
	Step    int            `json:"step"`
	Payload map[string]any `json:"payload"`
}

type BreedResponse struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	ImageURL *string `json:"imageUrl"`
}

type BreedListResponse struct {
	Items []BreedResponse `json:"items"`
}

type PetResponse struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Species   string      `json:"species"`
	Breed     *BreedBrief `json:"breed"`
	Size      string      `json:"size"`
	Traits    []string    `json:"traits"`
	CreatedAt string      `json:"createdAt"`
	UpdatedAt string      `json:"updatedAt"`
}

type BreedBrief struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type PetListResponse struct {
	Items []PetResponse `json:"items"`
	Count int           `json:"count"`
}

type DraftResponse struct {
	Step      int            `json:"step"`
	Payload   map[string]any `json:"payload"`
	UpdatedAt string         `json:"updatedAt"`
}

func newPetResponse(p *Pet) PetResponse {
	resp := PetResponse{
		ID:      p.ID.String(),
		Name:    p.Name,
		Species: p.Species,
		Size:    p.Size,

		Traits:    append([]string{}, p.Traits...),
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
	if p.BreedID != nil && p.BreedName != nil {
		resp.Breed = &BreedBrief{ID: *p.BreedID, Name: *p.BreedName}
	}
	return resp
}

func newPetListResponse(items []Pet) PetListResponse {
	out := make([]PetResponse, 0, len(items))
	for i := range items {
		out = append(out, newPetResponse(&items[i]))
	}
	return PetListResponse{Items: out, Count: len(out)}
}

func newBreedListResponse(items []Breed) BreedListResponse {
	out := make([]BreedResponse, 0, len(items))
	for _, b := range items {
		out = append(out, BreedResponse{ID: b.ID, Name: b.Name, ImageURL: b.ImageURL})
	}
	return BreedListResponse{Items: out}
}
