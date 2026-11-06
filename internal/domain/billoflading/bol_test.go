package billoflading

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var issuedAt = time.Date(2026, 11, 6, 16, 2, 1, 0, time.UTC)

func validIssueInput() IssueInput {
	return IssueInput{
		BolNumber: "BOL-2026-11-06-0042", LoadID: "load-7f3a", Site: "SITE-1",
		LoadConfirmed: true, Carrier: "STDOUT", CarrierScac: "STDOUT",
		TrailerID: "trl-91", SealNumber: "SL-4471",
		PackageCount: 40, TotalWeightG: 412500, At: issuedAt,
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIssueRaisesBillOfLadingIssued(t *testing.T) {
	b, evs, err := Issue(validIssueInput())
	mustDo(t, err)
	if b.State() != StateIssued || b.Version() != 1 {
		t.Fatalf("state = %s version = %d", b.State(), b.Version())
	}
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	ev, ok := evs[0].(BillOfLadingIssued)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	if ev.BolNumber() != "BOL-2026-11-06-0042" || ev.SiteID() != "SITE-1" || ev.BolVersion() != 1 {
		t.Fatalf("event identity wrong: %+v", ev.Header)
	}
	if ev.LoadID != "load-7f3a" || ev.Carrier != "STDOUT" || ev.CarrierScac != "STDOUT" {
		t.Fatalf("event load fields wrong: %+v", ev)
	}
	if ev.TrailerID != "trl-91" || ev.SealNumber != "SL-4471" {
		t.Fatalf("event trailer fields wrong: %+v", ev)
	}
}

func issuedEvent(t *testing.T) BillOfLadingIssued {
	t.Helper()
	_, evs, err := Issue(validIssueInput())
	mustDo(t, err)
	ev, ok := evs[0].(BillOfLadingIssued)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	return ev
}

func TestIssuedDocumentExposesTheLoadFacts(t *testing.T) {
	b, _, err := Issue(validIssueInput())
	mustDo(t, err)
	if b.LoadID() != "load-7f3a" || b.SiteID() != "SITE-1" {
		t.Fatalf("load facts wrong: %s %s", b.LoadID(), b.SiteID())
	}
	if b.Carrier() != "STDOUT" || b.CarrierScac() != "STDOUT" || b.TrailerID() != "trl-91" {
		t.Fatalf("carrier facts wrong: %s %s %s", b.Carrier(), b.CarrierScac(), b.TrailerID())
	}
	if b.SealNumber() != "SL-4471" || b.PackageCount() != 40 || b.TotalWeightG() != 412500 {
		t.Fatalf("document facts wrong: %+v", b)
	}
	if ev := issuedEvent(t); ev.PackageCount != 40 || ev.TotalWeightG != 412500 || !ev.IssuedAt.Equal(issuedAt) {
		t.Fatalf("event counts wrong: %+v", ev)
	}
	if !b.IssuedAt().Equal(issuedAt) || b.VoidedAt() != (time.Time{}) || b.VoidReason() != "" {
		t.Fatalf("timestamps wrong: %+v", b)
	}
}

// Required table case: issuing a second bill of lading for the same load
// refuses with ErrBolAlreadyIssued.
func TestSecondIssueRefused(t *testing.T) {
	in := validIssueInput()
	in.ExistingBolForLoad = true
	_, _, err := Issue(in)
	wantErr(t, err, ErrBolAlreadyIssued)
	if !errors.Is(err, ErrBolAlreadyIssued) || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("message does not say which half refused: %v", err)
	}
}

func TestIssueForUnconfirmedLoadRefused(t *testing.T) {
	in := validIssueInput()
	in.LoadConfirmed = false
	_, _, err := Issue(in)
	wantErr(t, err, ErrBolAlreadyIssued)
	if !strings.Contains(err.Error(), "not ship-confirmed") {
		t.Fatalf("message does not say which half refused: %v", err)
	}
}

// Required-model invariant: no seal, no document.
func TestIssueWithoutSealRefused(t *testing.T) {
	in := validIssueInput()
	in.SealNumber = ""
	_, _, err := Issue(in)
	wantErr(t, err, ErrSealMissing)
}

func TestIssueValidates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*IssueInput)
		want error
	}{
		{"no bol number", func(in *IssueInput) { in.BolNumber = "" }, ErrRequiredField},
		{"no load", func(in *IssueInput) { in.LoadID = "" }, ErrRequiredField},
		{"no site", func(in *IssueInput) { in.Site = "" }, ErrRequiredField},
		{"no carrier", func(in *IssueInput) { in.Carrier = "" }, ErrRequiredField},
		{"no trailer", func(in *IssueInput) { in.TrailerID = "" }, ErrRequiredField},
		{"no timestamp", func(in *IssueInput) { in.At = time.Time{} }, ErrNoTimestamp},
		{"zero packages", func(in *IssueInput) { in.PackageCount = 0 }, ErrInvalidPackageCount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validIssueInput()
			tc.mut(&in)
			_, _, err := Issue(in)
			wantErr(t, err, tc.want)
		})
	}
}

// Edge values of the numeric bounds: weight 0 is valid, -0.1 is not;
// package count 1 is valid, 0 is not.
func TestIssueNumericEdges(t *testing.T) {
	in := validIssueInput()
	in.TotalWeightG = 0
	if _, _, err := Issue(in); err != nil {
		t.Fatalf("weight 0 refused: %v", err)
	}
	in = validIssueInput()
	in.TotalWeightG = -0.1
	_, _, err := Issue(in)
	wantErr(t, err, ErrInvalidWeight)
	in = validIssueInput()
	in.PackageCount = 1
	if _, _, err := Issue(in); err != nil {
		t.Fatalf("one package refused: %v", err)
	}
}

func TestVoid(t *testing.T) {
	b, _, err := Issue(validIssueInput())
	mustDo(t, err)
	mustDo(t, b.Void("reprint-for-carrier", issuedAt.Add(30*time.Minute)))
	if b.State() != StateVoided || b.Version() != 2 {
		t.Fatalf("state = %s version = %d", b.State(), b.Version())
	}
	ev, ok := b.PullEvents()[0].(BillOfLadingVoided)
	if !ok || ev.Reason != "reprint-for-carrier" || ev.LoadID != "load-7f3a" {
		t.Fatalf("BillOfLadingVoided wrong: %+v", ev)
	}
	if ev.BolVersion() != 2 {
		t.Fatalf("version = %d, want 2", ev.BolVersion())
	}
	wantErr(t, b.Void("again", issuedAt), ErrBolAlreadyVoided)
}

func TestVoidValidates(t *testing.T) {
	b, _, err := Issue(validIssueInput())
	mustDo(t, err)
	wantErr(t, b.Void("", issuedAt), ErrVoidReasonRequired)
	wantErr(t, b.Void("r", time.Time{}), ErrNoTimestamp)
}
