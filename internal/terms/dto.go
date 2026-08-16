package terms

type TermResponse struct {
	ID         int    `json:"id"`
	Code       string `json:"code"`
	Version    string `json:"version"`
	Required   bool   `json:"required"`
	Title      string `json:"title"`
	ContentURL string `json:"contentUrl"`
}

type ListResponse struct {
	Items []TermResponse `json:"items"`
}

type AgreeRequest struct {
	Agreements []AgreementRequest `json:"agreements" binding:"required"`
}

type AgreementRequest struct {
	TermID *int  `json:"termId" binding:"required"`
	Agreed *bool `json:"agreed" binding:"required"`
}

func newListResponse(items []Term) ListResponse {
	out := make([]TermResponse, 0, len(items))
	for _, t := range items {
		out = append(out, TermResponse{
			ID:         t.ID,
			Code:       t.Code,
			Version:    t.Version,
			Required:   t.Required,
			Title:      t.Title,
			ContentURL: t.ContentURL,
		})
	}
	return ListResponse{Items: out}
}
