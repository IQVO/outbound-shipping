package outboundload

import (
	"slices"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

func TestStartLoadingRequiresDoorAndTrailer(t *testing.T) {
	l := plannedLoad(t)
	wantErr(t, l.StartLoading(planAt), ErrTrailerNotDocked)
	mustLoad(t, l.AssignDoor(AssignDoorInput{DoorCode: "D04", AppointmentID: "appt-2211", At: planAt}))
	wantErr(t, l.StartLoading(planAt), ErrTrailerNotDocked)
	mustLoad(t, l.AttachTrailer(TrailerDocked{TrailerID: "trl-91", DoorCode: "D04", At: planAt}))
	l.PullEvents()
	mustLoad(t, l.StartLoading(planAt))
	if l.State() != StateLoading || l.Trailer() != "trl-91" {
		t.Fatalf("state = %s trailer = %q", l.State(), l.Trailer())
	}
	evs := l.PullEvents()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	ev, ok := evs[0].(LoadingStarted)
	if !ok || ev.DoorCode != "D04" || ev.TrailerID != "trl-91" || ev.LoadVersion() != 3 {
		t.Fatalf("LoadingStarted wrong: %+v", evs[0])
	}
	wantErr(t, l.StartLoading(planAt), ErrInvalidTransition)
}

// Required table case: close with one package neither loaded nor
// short-shipped refuses with ErrPackagesOutstanding.
func TestCloseLoadRefusesOutstandingPackage(t *testing.T) {
	l := loadingLoad(t)
	err := l.CloseLoad(CloseInput{OutstandingPackages: []shared.PackageID{"pkg-2055"}, At: planAt})
	wantErr(t, err, ErrPackagesOutstanding)
	if l.State() != StateLoading {
		t.Fatalf("refused close changed the state to %s", l.State())
	}
	if evs := l.PullEvents(); len(evs) != 0 {
		t.Fatalf("refused close raised %+v", evs)
	}
}

func TestCloseLoadZeroOutstandingSucceeds(t *testing.T) {
	l := loadingLoad(t)
	mustLoad(t, l.CloseLoad(CloseInput{
		OutstandingPackages: nil,
		ShortShips:          []shared.ShortShip{{PackageID: "pkg-2077", Reason: "DIVERTED"}},
		LoadedCount:         2,
		At:                  planAt,
	}))
	if l.State() != StateClosed || l.PackageCount() != 2 {
		t.Fatalf("state = %s packageCount = %d", l.State(), l.PackageCount())
	}
	if got := l.ShortShips(); len(got) != 1 || got[0].PackageID != "pkg-2077" || got[0].Reason != "DIVERTED" {
		t.Fatalf("shortShips = %+v", got)
	}
	ev, ok := l.PullEvents()[0].(LoadClosed)
	if !ok || ev.PackageCount != 2 || ev.ShortShippedCount != 1 || ev.DoorCode != "D04" || ev.TrailerID != "trl-91" {
		t.Fatalf("LoadClosed wrong: %+v", ev)
	}
	if !ev.ClosedAt.Equal(planAt) || ev.LoadVersion() != 4 {
		t.Fatalf("LoadClosed header wrong: %+v", ev.Header)
	}
}

func TestCloseLoadValidates(t *testing.T) {
	l := loadingLoad(t)
	wantErr(t, l.CloseLoad(CloseInput{At: time.Time{}}), ErrNoTimestamp)
	wantErr(t, l.CloseLoad(CloseInput{
		At:         planAt,
		ShortShips: []shared.ShortShip{{PackageID: "pkg-1", Reason: ""}},
	}), ErrInvalidShortShip)
	wantErr(t, l.CloseLoad(CloseInput{
		At:         planAt,
		ShortShips: []shared.ShortShip{{PackageID: "", Reason: "DAMAGED"}},
	}), ErrInvalidShortShip)
	wantErr(t, l.CloseLoad(CloseInput{At: planAt, LoadedCount: -1}), ErrInvalidShortShip)
}

func TestReopenLoad(t *testing.T) {
	l := closedLoad(t)
	mustLoad(t, l.ReopenLoad(planAt))
	if l.State() != StateLoading {
		t.Fatalf("state = %s, want LOADING", l.State())
	}
	if evs := l.PullEvents(); len(evs) != 1 || evs[0].EventName() != "LoadReopened" {
		t.Fatalf("events = %+v", evs)
	}
	wantErr(t, l.ReopenLoad(planAt), ErrInvalidTransition)
}

// Required table case: confirming a LOADING load refuses with
// ErrLoadNotClosed.
func TestConfirmShipmentRefusesLoadingLoad(t *testing.T) {
	l := loadingLoad(t)
	err := l.ConfirmShipment(confirmInput())
	wantErr(t, err, ErrLoadNotClosed)
	if l.State() != StateLoading {
		t.Fatalf("refused confirm changed the state to %s", l.State())
	}
	if evs := l.PullEvents(); len(evs) != 0 {
		t.Fatalf("refused confirm raised %+v", evs)
	}
}

func confirmInput() ConfirmInput {
	return ConfirmInput{
		Departure: Departure{TrailerID: "trl-91", SealNumber: "SL-4471", DepartedAt: planAt},
		Carrier:   "STDOUT",
		Orders:    []string{"order-88421", "order-88422", "order-88421"},
		At:        planAt,
	}
}

func TestConfirmShipment(t *testing.T) {
	l := closedLoad(t)
	mustLoad(t, l.ConfirmShipment(confirmInput()))
	if l.State() != StateShipConfirmed {
		t.Fatalf("state = %s, want SHIP_CONFIRMED", l.State())
	}
	ev, ok := l.PullEvents()[0].(ShipmentConfirmed)
	if !ok {
		t.Fatalf("event = %T", ev)
	}
	if ev.Carrier != "STDOUT" || ev.TrailerID != "trl-91" || ev.SealNumber != "SL-4471" {
		t.Fatalf("ShipmentConfirmed fields wrong: %+v", ev)
	}
	if !ev.DepartedAt.Equal(planAt) || ev.CptID != "cpt-1600" || ev.LoadVersion() != 5 {
		t.Fatalf("ShipmentConfirmed header wrong: %+v", ev)
	}
	if !slices.Equal(ev.Orders, []string{"order-88421", "order-88422"}) {
		t.Fatalf("orders = %v, want deduped and sorted", ev.Orders)
	}
}

// A SHIP_CONFIRMED load never changes: every mutating command refuses with
// ErrLoadConfirmed.
func TestConfirmedLoadNeverChanges(t *testing.T) {
	l := closedLoad(t)
	mustLoad(t, l.ConfirmShipment(confirmInput()))
	l.PullEvents()
	for name, call := range map[string]func() error{
		"confirm again":  func() error { return l.ConfirmShipment(confirmInput()) },
		"close":          func() error { return l.CloseLoad(CloseInput{At: planAt}) },
		"reopen":         func() error { return l.ReopenLoad(planAt) },
		"cancel":         func() error { return l.CancelLoad("late", planAt) },
		"start loading":  func() error { return l.StartLoading(planAt) },
		"assign door":    func() error { return l.AssignDoor(AssignDoorInput{DoorCode: "D09", AppointmentID: "a9", At: planAt}) },
		"attach trailer": func() error { return l.AttachTrailer(TrailerDocked{TrailerID: "t2", DoorCode: "D04", At: planAt}) },
	} {
		t.Run(name, func(t *testing.T) { wantErr(t, call(), ErrLoadConfirmed) })
	}
	if evs := l.PullEvents(); len(evs) != 0 {
		t.Fatalf("confirmed load raised %+v", evs)
	}
}

func TestConfirmShipmentValidates(t *testing.T) {
	l := closedLoad(t)
	in := confirmInput()
	in.Departure.TrailerID = ""
	wantErr(t, l.ConfirmShipment(in), ErrRequiredField)
	in = confirmInput()
	in.Departure.DepartedAt = time.Time{}
	wantErr(t, l.ConfirmShipment(in), ErrRequiredField)
	in = confirmInput()
	in.At = time.Time{}
	wantErr(t, l.ConfirmShipment(in), ErrNoTimestamp)
}

func TestCancelLoad(t *testing.T) {
	l := plannedLoad(t)
	wantErr(t, l.CancelLoad("", planAt), ErrCancelReasonRequired)
	wantErr(t, l.CancelLoad("withdrawn", time.Time{}), ErrNoTimestamp)
	mustLoad(t, l.CancelLoad("cutoff withdrawn", planAt))
	if l.State() != StateCancelled {
		t.Fatalf("state = %s, want CANCELLED", l.State())
	}
	if evs := l.PullEvents(); len(evs) != 1 || evs[0].EventName() != "LoadCancelled" {
		t.Fatalf("events = %+v", evs)
	}
	loading := loadingLoad(t)
	wantErr(t, loading.CancelLoad("late", planAt), ErrInvalidTransition)
}

func TestPullEventsDrains(t *testing.T) {
	l := plannedLoad(t)
	l.PullEvents()
	if evs := l.PullEvents(); len(evs) != 0 {
		t.Fatalf("second PullEvents returned %+v", evs)
	}
}
