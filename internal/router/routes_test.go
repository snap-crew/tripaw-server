package router

import (
	"testing"

	"github.com/snap-crew/tripaw-server/internal/auth"
	"github.com/snap-crew/tripaw-server/internal/pet"
	"github.com/snap-crew/tripaw-server/internal/place"
	"github.com/snap-crew/tripaw-server/internal/terms"
	"github.com/snap-crew/tripaw-server/internal/token"
	"github.com/snap-crew/tripaw-server/internal/trip"
)

func TestRoutesRegisterWithoutConflict(t *testing.T) {
	tokens := token.NewManager("test-secret-that-is-long-enough-32b", 3600, 3600)
	r := New(tokens,
		auth.NewHandler(auth.NewService(auth.NewRepository(nil), tokens, nil, nil), true),
		terms.NewHandler(terms.NewService(terms.NewRepository(nil))),
		pet.NewHandler(pet.NewService(pet.NewRepository(nil))),
		place.NewHandler(place.NewService(place.NewRepository(nil))),
		trip.NewHandler(trip.NewService(trip.NewRepository(nil), nil)),
	)

	for _, ri := range r.Routes() {
		t.Logf("%-7s %s", ri.Method, ri.Path)
	}
	if n := len(r.Routes()); n < 25 {
		t.Errorf("라우트 %d개, 더 있어야 한다", n)
	}
}
