package trip

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/daewon/tripaw-server/internal/ai"
	"github.com/google/uuid"
)

const (
	stopsPerDay    = 4
	candidateLimit = 80

	// 같은 카테고리를 또 넣느니 이만큼 더 멀리 가는 편이 낫다고 본다
	categoryDetourKm = 10.0
	promptVersion    = "v1"

	// ponytail: 동기 응답이라 LLM 을 이 시간 안에 끊는다. main.go 의 WriteTimeout(30s)
	// 안에 들어와야 한다. 넘기면 SQL 랭킹으로 대체한다. 체감이 나쁘면 202 + 폴링으로 올린다.
	planTimeout = 20 * time.Second

	planSystem = "너는 제주 반려동물 동반 여행 일정을 짜는 플래너다. " +
		"주어진 후보 목록 안에서만 장소를 고른다."
)

var planSchema = ai.Schema{
	"type": "OBJECT",
	"properties": map[string]any{
		"days": map[string]any{
			"type": "ARRAY",
			"items": map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"dayNo": map[string]any{"type": "INTEGER"},
					"placeIds": map[string]any{
						"type":  "ARRAY",
						"items": map[string]any{"type": "INTEGER"},
					},
				},
				"required":         []string{"dayNo", "placeIds"},
				"propertyOrdering": []string{"dayNo", "placeIds"},
			},
		},
	},
	"required": []string{"days"},
}

func (s *Service) Generate(ctx context.Context, userID, tripID uuid.UUID) (*Trip, error) {
	t, err := s.Get(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	if len(t.Days) == 0 {
		return nil, invalid("invalid_date_range", "여행 기간이 정해지지 않았습니다")
	}
	if t.PlaceCount > 0 {
		return nil, ErrTripNotEmpty
	}

	cands, err := s.repo.Candidates(ctx, petSize(t.Pets), candidateLimit)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, ErrNoCandidates
	}

	days, by := s.planDays(ctx, t, cands)
	if err := s.repo.SaveGenerated(ctx, tripID, days, by); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, tripID)
}

func (s *Service) planDays(ctx context.Context, t *Trip, cands []Candidate) ([][]int64, []byte) {
	fallback := func(reason string) ([][]int64, []byte) {
		if reason != "" {
			slog.Warn("AI 일정 생성 실패, 랭킹으로 대체", "tripId", t.ID, "reason", reason)
		}
		return fallbackDays(cands, len(t.Days), t.Origin), generatedBy("ranking", "")
	}

	if s.ai == nil {
		return fallback("")
	}

	planCtx, cancel := context.WithTimeout(ctx, planTimeout)
	defer cancel()

	raw, err := s.ai.Structured(planCtx, planSystem, planPrompt(t, cands), planSchema)
	if err != nil {
		return fallback(err.Error())
	}

	days := parsePlan(raw, cands, len(t.Days))
	if days == nil {
		return fallback("응답에서 쓸 수 있는 장소가 없다")
	}
	return days, generatedBy("llm", s.ai.Model())
}

func generatedBy(source, model string) []byte {
	b, _ := json.Marshal(map[string]string{
		"source":        source,
		"model":         model,
		"promptVersion": promptVersion,
	})
	return b
}

func planPrompt(t *Trip, cands []Candidate) string {
	var b strings.Builder

	fmt.Fprintf(&b, "여행 일수: %d일\n", len(t.Days))
	if len(t.Pets) > 0 {
		pets := make([]string, 0, len(t.Pets))
		for _, p := range t.Pets {
			pets = append(pets, fmt.Sprintf("%s(크기 %s, 성향 %s)",
				p.Name, p.Size, strings.Join(p.Traits, "·")))
		}
		fmt.Fprintf(&b, "동반 반려동물: %s\n", strings.Join(pets, ", "))
	}
	if len(t.Themes) > 0 {
		fmt.Fprintf(&b, "테마: %s\n", strings.Join(t.Themes, ", "))
	}
	if t.Origin != nil {
		name := "출발지"
		if t.Origin.Name != nil {
			name = *t.Origin.Name
		}
		fmt.Fprintf(&b, "출발지: %s (위도 %.4f, 경도 %.4f)\n", name, t.Origin.Lat, t.Origin.Lng)
	}

	fmt.Fprintf(&b, `
규칙:
- 1일차부터 %d일차까지 모두 채운다. 하루에 %d곳씩 고른다
- 아래 후보 목록에 있는 placeId 만 쓴다. 목록에 없는 번호를 만들지 않는다
- 여행 전체에서 같은 장소를 두 번 쓰지 않는다
- 하루 안에서는 주소가 가까운 곳끼리 묶고, 이동하기 좋은 순서로 배열한다
- 하루에 restaurant 는 최대 1곳, cafe 는 최대 1곳까지만 넣는다%s

후보 (placeId | 카테고리 | 주소 | 이름):
`, len(t.Days), stopsPerDay, originRule(t))

	for _, c := range cands {
		addr := ""
		if c.RoadAddress != nil {
			addr = *c.RoadAddress
		}
		fmt.Fprintf(&b, "%d | %s | %s | %s\n", c.ID, c.Category, addr, c.Name)
	}
	return b.String()
}

func parsePlan(raw []byte, cands []Candidate, dayCount int) [][]int64 {
	var plan struct {
		Days []struct {
			DayNo    int     `json:"dayNo"`
			PlaceIDs []int64 `json:"placeIds"`
		} `json:"days"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil
	}

	known := make(map[int64]bool, len(cands))
	for _, c := range cands {
		known[c.ID] = true
	}

	days := make([][]int64, dayCount)
	used := make(map[int64]bool, dayCount*stopsPerDay)
	total := 0

	for _, d := range plan.Days {
		if d.DayNo < 1 || d.DayNo > dayCount {
			continue
		}
		for _, id := range d.PlaceIDs {
			if !known[id] || used[id] || len(days[d.DayNo-1]) >= stopsPerDay {
				continue
			}
			used[id] = true
			days[d.DayNo-1] = append(days[d.DayNo-1], id)
			total++
		}
	}

	if total == 0 {
		return nil
	}
	return days
}

// 하루치를 지리적으로 묶는다. 랭킹 순서대로 4개씩 자르면 후보가 카테고리별로
// 섞여 있어 하루에 제주를 한 바퀴 도는 일정이 나온다(실측 하루 최대 46km).
//
// 랭킹 1위를 그날의 기준점으로 잡고 가까운 곳부터 채우되, 이미 쓴 카테고리는
// categoryDetourKm 만큼 멀게 쳐서 같은 종류만 몰리지 않게 한다.
func fallbackDays(cands []Candidate, dayCount int, origin *Origin) [][]int64 {
	used := make([]bool, len(cands))
	days := make([][]int64, dayCount)

	for d := range days {
		seed := -1
		if d == 0 && origin != nil {
			seed = nearestTo(cands, used, origin)
		}
		if seed < 0 {
			for i := range cands {
				if !used[i] {
					seed = i
					break
				}
			}
		}
		if seed < 0 {
			break
		}

		used[seed] = true
		days[d] = append(days[d], cands[seed].ID)
		picked := map[string]bool{cands[seed].Category: true}

		for len(days[d]) < stopsPerDay {
			best, bestCost := -1, 0.0
			for i := range cands {
				if used[i] {
					continue
				}
				cost := distanceKm(cands[seed], cands[i])
				if picked[cands[i].Category] {
					cost += categoryDetourKm
				}
				if best < 0 || cost < bestCost {
					best, bestCost = i, cost
				}
			}
			if best < 0 {
				break
			}
			used[best] = true
			picked[cands[best].Category] = true
			days[d] = append(days[d], cands[best].ID)
		}
	}
	return days
}

// ponytail: 제주 한 섬 안에서만 쓰므로 위경도를 평면으로 근사한다. 오차는
// 수십 m 수준이고 정렬 결과가 바뀌지 않는다. 범위가 넓어지면 ST_Distance 로 올린다.
func distanceKm(a, b Candidate) float64 {
	const kmPerDeg = 111.0
	dLat := (a.Lat - b.Lat) * kmPerDeg
	dLng := (a.Lng - b.Lng) * kmPerDeg * math.Cos(a.Lat*math.Pi/180)
	return math.Hypot(dLat, dLng)
}

var sizeRank = map[string]int{"small": 1, "medium": 2, "large": 3}

func petSize(pets []Pet) string {
	out := "small"
	for _, p := range pets {
		if sizeRank[p.Size] > sizeRank[out] {
			out = p.Size
		}
	}
	return out
}

func originRule(t *Trip) string {
	if t.Origin == nil {
		return ""
	}
	return "\n- 1일차는 출발지에서 가까운 곳부터 시작한다"
}

// 출발지에서 가장 가까운, 아직 쓰지 않은 후보.
func nearestTo(cands []Candidate, used []bool, o *Origin) int {
	ref := Candidate{Lat: o.Lat, Lng: o.Lng}
	best, bestKm := -1, 0.0
	for i := range cands {
		if used[i] {
			continue
		}
		if km := distanceKm(ref, cands[i]); best < 0 || km < bestKm {
			best, bestKm = i, km
		}
	}
	return best
}
