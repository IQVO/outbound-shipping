package billoflading

import (
	"testing"
)

func validRehydrateParams() RehydrateParams {
	return RehydrateParams{
		BolNumber: "BOL-2026-11-06-0042", LoadID: "load-7f3a", Site: "SITE-1",
		Carrier: "STDOUT", CarrierScac: "STDOUT", TrailerID: "trl-91",
		SealNumber: "SL-4471", PackageCount: 40, TotalWeightG: 412500,
		IssuedAt: issuedAt, State: StateIssued, Version: 1,
	}
}

func TestRehydrateRoundTripsEveryState(t *testing.T) {
	for _, state := range validStates {
		p := validRehydrateParams()
		p.State = state
		b, err := Rehydrate(p)
		if err != nil {
			t.Fatalf("Rehydrate(%s): %v", state, err)
		}
		if b.State() != state || b.Version() != 1 || b.BolNumber() != "BOL-2026-11-06-0042" {
			t.Fatalf("roundtrip lost data: %+v", b)
		}
		if evs := b.PullEvents(); len(evs) != 0 {
			t.Fatalf("rehydrated document has events: %+v", evs)
		}
	}
}

func TestRehydrateValidates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RehydrateParams)
		want error
	}{
		{"no bol number", func(p *RehydrateParams) { p.BolNumber = "" }, ErrRequiredField},
		{"no load", func(p *RehydrateParams) { p.LoadID = "" }, ErrRequiredField},
		{"no site", func(p *RehydrateParams) { p.Site = "" }, ErrRequiredField},
		{"no carrier", func(p *RehydrateParams) { p.Carrier = "" }, ErrRequiredField},
		{"no trailer", func(p *RehydrateParams) { p.TrailerID = "" }, ErrRequiredField},
		{"no seal", func(p *RehydrateParams) { p.SealNumber = "" }, ErrSealMissing},
		{"zero packages", func(p *RehydrateParams) { p.PackageCount = 0 }, ErrInvalidPackageCount},
		{"unknown state", func(p *RehydrateParams) { p.State = "DRAFT" }, ErrInvalidState},
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

// Edge values of the version bound: 1 restores, 0 and -1 refuse.
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

// Edge values of the package-count bound: 1 restores, 0 refuses.
func TestRehydratePackageCountEdges(t *testing.T) {
	p := validRehydrateParams()
	p.PackageCount = 1
	if _, err := Rehydrate(p); err != nil {
		t.Fatalf("one package refused: %v", err)
	}
	p.PackageCount = 0
	_, err := Rehydrate(p)
	wantErr(t, err, ErrInvalidPackageCount)
}

func TestPullEventsDrains(t *testing.T) {
	b, _, err := Issue(validIssueInput())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	b.PullEvents()
	if evs := b.PullEvents(); len(evs) != 0 {
		t.Fatalf("second PullEvents returned %+v", evs)
	}
}
