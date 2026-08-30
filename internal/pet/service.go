package pet

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/daewon/tripaw-server/internal/image"
	"github.com/google/uuid"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

var nameRe = regexp.MustCompile(`^[가-힣ㄱ-ㅎㅏ-ㅣa-zA-Z0-9 ]{1,12}$`)

var draftPayloadKeys = map[string]bool{
	"name": true, "species": true, "breedId": true, "photoUrl": true,
	"size": true, "gender": true, "neutered": true, "traits": true,
	"weightKg": true,
}

func (s *Service) ListBreeds(ctx context.Context, species, q string) ([]Breed, error) {
	if !allowedSpecies[species] {
		return nil, invalid("invalid_request", "species 는 dog 또는 cat 이어야 합니다")
	}
	return s.repo.ListBreeds(ctx, species, strings.TrimSpace(q))
}

func (s *Service) SaveDraft(ctx context.Context, userID uuid.UUID, step int, payload map[string]any) error {
	if step < 1 || step > 2 {
		return invalid("invalid_request", "step 은 1 또는 2 여야 합니다")
	}
	if payload == nil {
		return invalid("invalid_request", "payload 가 필요합니다")
	}
	for k := range payload {
		if !draftPayloadKeys[k] {
			return invalid("invalid_request", "알 수 없는 payload 키입니다: "+k)
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return invalid("invalid_request", "payload 를 저장할 수 없습니다")
	}
	return s.repo.SaveDraft(ctx, userID, step, raw)
}

func (s *Service) FindDraft(ctx context.Context, userID uuid.UUID) (int, map[string]any, string, error) {
	d, err := s.repo.FindDraft(ctx, userID)
	if err != nil {
		return 0, nil, "", err
	}

	var payload map[string]any
	if err := json.Unmarshal(d.Payload, &payload); err != nil {
		return 0, nil, "", err
	}
	return d.Step, payload, d.UpdatedAt.Format(rfc3339), nil
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"

func (s *Service) Create(ctx context.Context, userID uuid.UUID, req *CreateRequest) (*Pet, error) {
	name, err := validateName(req.Name)
	if err != nil {
		return nil, err
	}
	if !allowedSpecies[req.Species] {
		return nil, invalid("invalid_species", "종은 dog 또는 cat 이어야 합니다")
	}
	if !allowedSize[req.Size] {
		return nil, invalid("invalid_size", "크기는 small, medium, large 중 하나여야 합니다")
	}
	if err := validateWeight(req.WeightKg); err != nil {
		return nil, err
	}
	if !allowedGender[req.Gender] {
		return nil, invalid("invalid_gender", "성별은 male 또는 female 이어야 합니다")
	}
	if !allowedNeutered[req.Neutered] {
		return nil, invalid("invalid_neutered", "중성화는 done, not_done, unknown 중 하나여야 합니다")
	}
	traits, err := validateTraits(req.Traits)
	if err != nil {
		return nil, err
	}
	if err := validatePhotoURL(req.PhotoURL); err != nil {
		return nil, err
	}
	if req.BreedID == nil {
		return nil, invalid("invalid_breed", "품종을 선택해 주세요")
	}
	if err := s.checkBreed(ctx, *req.BreedID, req.Species); err != nil {
		return nil, err
	}

	n, err := s.repo.CountByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if n >= MaxPets {
		return nil, ErrLimitExceeded
	}

	return s.repo.Create(ctx, &Pet{
		UserID:   userID,
		Name:     name,
		Species:  req.Species,
		BreedID:  req.BreedID,
		Size:     req.Size,
		Gender:   req.Gender,
		Neutered: req.Neutered,
		Traits:   traits,
		PhotoURL: req.PhotoURL,
		WeightKg: req.WeightKg,
	})
}

func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Pet, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *Service) Get(ctx context.Context, userID, petID uuid.UUID) (*Pet, error) {
	p, err := s.repo.FindByID(ctx, petID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, ErrForbidden
	}
	return p, nil
}

func (s *Service) Update(ctx context.Context, userID, petID uuid.UUID, req *UpdateRequest) (*Pet, error) {
	current, err := s.Get(ctx, userID, petID)
	if err != nil {
		return nil, err
	}

	var u petUpdate

	if req.Name != nil {
		name, err := validateName(*req.Name)
		if err != nil {
			return nil, err
		}
		u.Name = &name
	}
	if req.Species != nil {
		if !allowedSpecies[*req.Species] {
			return nil, invalid("invalid_species", "종은 dog 또는 cat 이어야 합니다")
		}
		u.Species = req.Species
	}
	if req.Size != nil {
		if !allowedSize[*req.Size] {
			return nil, invalid("invalid_size", "크기는 small, medium, large 중 하나여야 합니다")
		}
		u.Size = req.Size
	}
	if req.WeightKg != nil {
		if err := validateWeight(req.WeightKg); err != nil {
			return nil, err
		}
		u.WeightKg = req.WeightKg
	}
	if req.Gender != nil {
		if !allowedGender[*req.Gender] {
			return nil, invalid("invalid_gender", "성별은 male 또는 female 이어야 합니다")
		}
		u.Gender = req.Gender
	}
	if req.Neutered != nil {
		if !allowedNeutered[*req.Neutered] {
			return nil, invalid("invalid_neutered", "중성화는 done, not_done, unknown 중 하나여야 합니다")
		}
		u.Neutered = req.Neutered
	}
	if req.Traits != nil {
		traits, err := validateTraits(*req.Traits)
		if err != nil {
			return nil, err
		}

		if traits == nil {
			traits = []string{}
		}
		u.Traits = traits
	}
	if req.BreedID != nil {
		species := current.Species
		if req.Species != nil {
			species = *req.Species
		}
		if err := s.checkBreed(ctx, *req.BreedID, species); err != nil {
			return nil, err
		}
		u.BreedID = req.BreedID
	} else if req.Species != nil && *req.Species != current.Species && current.BreedID != nil {
		return nil, invalid("invalid_breed", "종을 바꾸려면 품종도 함께 선택해 주세요")
	}

	photoURL, set, err := req.photoURL()
	if err != nil {
		return nil, invalid("invalid_photo_url", "photoUrl 형식이 잘못되었습니다")
	}
	if set {
		if err := validatePhotoURL(photoURL); err != nil {
			return nil, err
		}
		u.PhotoURL, u.PhotoURLSet = photoURL, true
	}

	return s.repo.Update(ctx, petID, &u)
}

func (s *Service) Delete(ctx context.Context, userID, petID uuid.UUID) error {
	if _, err := s.Get(ctx, userID, petID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, petID)
}

func (s *Service) checkBreed(ctx context.Context, breedID int, species string) error {
	got, err := s.repo.BreedSpecies(ctx, breedID)
	if err == ErrNotFound {
		return invalid("invalid_breed", "존재하지 않는 품종입니다")
	}
	if err != nil {
		return err
	}
	if got != species {
		return invalid("invalid_breed", "선택한 종의 품종이 아닙니다")
	}
	return nil
}

func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", invalid("invalid_pet_name", "이름을 입력해 주세요")
	}

	if n := len([]rune(name)); n > 12 {
		return "", invalid("invalid_pet_name", "이름은 12자 이하여야 합니다")
	}
	if !nameRe.MatchString(name) {
		return "", invalid("invalid_pet_name",
			"이름에는 한글·영문·숫자·공백만 쓸 수 있습니다")
	}
	return name, nil
}

func validateTraits(traits []string) ([]string, error) {
	if len(traits) == 0 {
		return nil, nil
	}
	if len(traits) > 3 {
		return nil, invalid("invalid_traits", "성향은 최대 3개까지 선택할 수 있습니다")
	}

	seen := make(map[string]bool, len(traits))
	for _, t := range traits {
		if !allowedTraits[t] {
			return nil, invalid("invalid_traits", "알 수 없는 성향입니다: "+t)
		}
		if seen[t] {
			return nil, invalid("invalid_traits", "성향이 중복되었습니다: "+t)
		}
		seen[t] = true
	}
	return traits, nil
}

func validatePhotoURL(raw *string) error {
	if raw == nil || *raw == "" {
		return nil
	}
	if !image.IsManagedURL(*raw) {
		return invalid("invalid_photo_url",
			"photoUrl 은 POST /api/images 가 돌려준 주소여야 합니다")
	}
	return nil
}

func validateWeight(w *float64) error {
	if w == nil {
		return nil
	}
	if *w < MinWeightKg || *w > MaxWeightKg {
		return invalid("invalid_weight",
			"몸무게는 0.1kg 이상 150kg 이하여야 합니다")
	}
	return nil
}
