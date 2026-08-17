package terms

import "errors"

type Term struct {
	ID         int
	Code       string
	Version    string
	Required   bool
	Title      string
	ContentURL string
}

type Agreement struct {
	TermID int
	Agreed bool
}

var ErrRequiredNotAgreed = errors.New("필수 약관에 모두 동의해야 합니다")
