package outboundload

import (
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

func validRehydrateParams() RehydrateParams {
	return RehydrateParams{
		ID:            "load-7f3a",
		Site:          "SITE-1",
		Cpt:           "cpt-1600",
		OperatingDate: "2026-11-06",
		ShipMethod:    "STDOUT-2DAY",
		Cutoff:        time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC),
		State:         StateLoading,
		Version:       3,
	}
}

func TestRehydrateRoundTripsEveryState(t *testing.T) {
	for _, state := range validStates {
		p := validRehydrateParams()
		p.State = state
		l, err := Rehydrate(p)
		if err != nil {
			t.Fatalf("Rehydrate(%s): %v", state, err)
		}
		if l.State() != state || l.Version() != 3 || l.ID() != "load-7f3a" {
			t.Fatalf("roundtrip lost data: %+v", l)
		}
		if evs := l.PullEvents(); len(evs) != 0 {
			t.Fatalf("rehydrated load has events: %+v", evs)
		}
	}
}

func TestRehydrateValidates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RehydrateParams)
		want error
	}{
		{"no id", func(p *RehydrateParams) { p.ID = "" }, ErrInvalidLoadID},
		{"no site", func(p *RehydrateParams) { p.Site = "" }, ErrRequiredField},
		{"no cpt", func(p *RehydrateParams) { p.Cpt = "" }, ErrRequiredField},
		{"no ship method", func(p *RehydrateParams) { p.ShipMethod = "" }, ErrRequiredField},
		{"no cutoff", func(p *RehydrateParams) { p.Cutoff = time.Time{} }, ErrRequiredField},
		{"bad date", func(p *RehydrateParams) { p.OperatingDate = "2026-11-6" }, ErrInvalidOperatingDate},
		{"empty date", func(p *RehydrateParams) { p.OperatingDate = "" }, ErrInvalidOperatingDate},
		{"unknown state", func(p *RehydrateParams) { p.State = "PENDING" }, ErrInvalidState},
		{"empty state", func(p *RehydrateParams) { p.State = "" }, ErrInvalidState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validRehydrateParams()
			tc.mut(&p)
			_, err := Rehydrate(p)
			wantErr(t, err, tc.want)
		})
	}
}

// Edge values of the version bound: 1 restores, 0 refuses (the recurring
// CONDITIONALS_BOUNDARY mutant lives exactly here).
func TestRehydrateVersionEdges(t *testing.T) {
	p := validRehydrateParams()
	p.Version = 1
	if _, err := Rehydrate(p); err != nil {
		t.Fatalf("version 1 refused: %v", err)
	}
	p.Version = 0
	_, err := Rehydrate(p)
	wantErr(t, err, ErrInvalidVersion)
	p.Version = -1
	_, err = Rehydrate(p)
	wantErr(t, err, ErrInvalidVersion)
}

// Edge values of the close bounds: zero outstanding packages and zero
// loaded count are valid; one outstanding package and a negative count are
// not.
func TestCloseBoundaryEdges(t *testing.T) {
	l := loadingLoad(t)
	if err := l.CloseLoad(CloseInput{OutstandingPackages: nil, LoadedCount: 0, At: planAt}); err != nil {
		t.Fatalf("empty close refused: %v", err)
	}
	l2 := loadingLoad(t)
	if err := l2.CloseLoad(CloseInput{OutstandingPackages: []shared.PackageID{}, LoadedCount: 0, At: planAt}); err != nil {
		t.Fatalf("zero-length outstanding refused: %v", err)
	}
}

func TestRehydrateShortShipsValidated(t *testing.T) {
	p := validRehydrateParams()
	p.ShortShips = []shared.ShortShip{{PackageID: "pkg-2077", Reason: ""}}
	_, err := Rehydrate(p)
	wantErr(t, err, ErrInvalidShortShip)
}

// Closing a load that is not LOADING refuses with ErrInvalidTransition.
func TestCloseLoadFromPlannedRefused(t *testing.T) {
	l := plannedLoad(t)
	wantErr(t, l.CloseLoad(CloseInput{At: planAt}), ErrInvalidTransition)
}
