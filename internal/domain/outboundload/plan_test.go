package outboundload

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

var planAt = time.Date(2026, 11, 6, 13, 2, 11, 0, time.UTC)

func validPlanInput() PlanInput {
	return PlanInput{
		LoadID:        "load-7f3a",
		Site:          "SITE-1",
		Cpt:           "cpt-1600",
		OperatingDate: "2026-11-06",
		ShipMethod:    "STDOUT-2DAY",
		Cutoff:        time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC),
		At:            planAt,
	}
}

func plannedLoad(t *testing.T) *OutboundLoad {
	t.Helper()
	l, _, err := Plan(validPlanInput())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return l
}

// loadingLoad is a PLANNED load with a door and a docked trailer that has
// started loading, handed over with its events already drained so a test
// sees only what IT raises.
func loadingLoad(t *testing.T) *OutboundLoad {
	t.Helper()
	l := plannedLoad(t)
	mustLoad(t, l.AssignDoor(AssignDoorInput{DoorCode: "D04", AppointmentID: "appt-2211", At: planAt}))
	mustLoad(t, l.AttachTrailer(TrailerDocked{TrailerID: "trl-91", DoorCode: "D04", At: planAt}))
	mustLoad(t, l.StartLoading(planAt))
	l.PullEvents()
	return l
}

// closedLoad is a LOADING load closed with two loaded packages and one
// short-ship.
func closedLoad(t *testing.T) *OutboundLoad {
	t.Helper()
	l := loadingLoad(t)
	mustLoad(t, l.CloseLoad(CloseInput{
		ShortShips:  []shared.ShortShip{{PackageID: "pkg-2077", Reason: "DIVERTED"}},
		LoadedCount: 2,
		At:          planAt,
	}))
	l.PullEvents()
	return l
}

func mustLoad(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func TestPlanRaisesLoadPlanned(t *testing.T) {
	l, evs, err := Plan(validPlanInput())
	mustLoad(t, err)
	if l.State() != StatePlanned {
		t.Fatalf("state = %s, want PLANNED", l.State())
	}
	if l.Version() != 1 {
		t.Fatalf("version = %d, want 1", l.Version())
	}
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	ev, ok := evs[0].(LoadPlanned)
	if !ok {
		t.Fatalf("event = %T, want LoadPlanned", evs[0])
	}
	if ev.EventName() != "LoadPlanned" || ev.LoadID() != "load-7f3a" || ev.SiteID() != "SITE-1" {
		t.Fatalf("event identity wrong: %+v", ev)
	}
	if ev.LoadVersion() != 1 || !ev.OccurredAt().Equal(planAt) {
		t.Fatalf("header wrong: %+v", ev.Header)
	}
}

func TestPlannedEventCarriesTheCutoffKey(t *testing.T) {
	l, evs, err := Plan(validPlanInput())
	mustLoad(t, err)
	ev := evs[0].(LoadPlanned)
	if ev.CptID != "cpt-1600" || ev.OperatingDate != "2026-11-06" || ev.ShipMethod != "STDOUT-2DAY" {
		t.Fatalf("key fields wrong: %+v", ev)
	}
	if !ev.Cutoff.Equal(time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("cutoff = %v", ev.Cutoff)
	}
	key := l.Key()
	if key.Site != "SITE-1" || key.Cpt != "cpt-1600" || key.OperatingDate != "2026-11-06" || key.ShipMethod != "STDOUT-2DAY" {
		t.Fatalf("key = %+v", key)
	}
}

func TestPlanRefusesDuplicateOpenLoad(t *testing.T) {
	in := validPlanInput()
	in.OpenLoadKeys = []LoadKey{{Site: "SITE-1", Cpt: "cpt-1600", OperatingDate: "2026-11-06", ShipMethod: "STDOUT-2DAY"}}
	_, _, err := Plan(in)
	wantErr(t, err, ErrDuplicateOpenLoad)
}

func TestPlanAllowsDistinctKeys(t *testing.T) {
	for name, different := range map[string]LoadKey{
		"other site":        {Site: "SITE-2", Cpt: "cpt-1600", OperatingDate: "2026-11-06", ShipMethod: "STDOUT-2DAY"},
		"other cpt":         {Site: "SITE-1", Cpt: "cpt-1800", OperatingDate: "2026-11-06", ShipMethod: "STDOUT-2DAY"},
		"other date":        {Site: "SITE-1", Cpt: "cpt-1600", OperatingDate: "2026-11-07", ShipMethod: "STDOUT-2DAY"},
		"other ship method": {Site: "SITE-1", Cpt: "cpt-1600", OperatingDate: "2026-11-06", ShipMethod: "GROUND"},
	} {
		t.Run(name, func(t *testing.T) {
			in := validPlanInput()
			in.OpenLoadKeys = []LoadKey{different}
			if _, _, err := Plan(in); err != nil {
				t.Fatalf("distinct key refused: %v", err)
			}
		})
	}
}

func TestPlanValidatesInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*PlanInput)
		want error
	}{
		{"no load id", func(in *PlanInput) { in.LoadID = "" }, ErrInvalidLoadID},
		{"no site", func(in *PlanInput) { in.Site = "" }, ErrRequiredField},
		{"no cpt", func(in *PlanInput) { in.Cpt = "" }, ErrRequiredField},
		{"no ship method", func(in *PlanInput) { in.ShipMethod = "" }, ErrRequiredField},
		{"no cutoff", func(in *PlanInput) { in.Cutoff = time.Time{} }, ErrRequiredField},
		{"no timestamp", func(in *PlanInput) { in.At = time.Time{} }, ErrNoTimestamp},
		{"empty date", func(in *PlanInput) { in.OperatingDate = "" }, ErrInvalidOperatingDate},
		{"month 13", func(in *PlanInput) { in.OperatingDate = "2026-13-01" }, ErrInvalidOperatingDate},
		{"day 32", func(in *PlanInput) { in.OperatingDate = "2026-11-32" }, ErrInvalidOperatingDate},
		{"not a date", func(in *PlanInput) { in.OperatingDate = "tomorrow" }, ErrInvalidOperatingDate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validPlanInput()
			tc.mut(&in)
			_, _, err := Plan(in)
			wantErr(t, err, tc.want)
		})
	}
}

func TestOperatingDateValidEdges(t *testing.T) {
	for d, want := range map[OperatingDate]bool{
		"2024-02-29": true,
		"2026-02-29": false, // 2026 is not a leap year
		"2026-11-06": true,
		"2026-11-6":  false,
		"":           false,
	} {
		if got := d.Valid(); got != want {
			t.Fatalf("Valid(%q) = %v, want %v", d, got, want)
		}
	}
}

func TestAssignDoor(t *testing.T) {
	l := plannedLoad(t)
	mustLoad(t, l.AssignDoor(AssignDoorInput{DoorCode: "D04", AppointmentID: "appt-2211", At: planAt}))
	if l.Door() != "D04" || l.Appointment() != "appt-2211" {
		t.Fatalf("door = %q appointment = %q", l.Door(), l.Appointment())
	}
	evs := l.PullEvents()
	if len(evs) != 1 || evs[0].EventName() != "LoadDoorAssigned" {
		t.Fatalf("events = %+v", evs)
	}
	// A later appointment for the same cpt re-assigns.
	mustLoad(t, l.AssignDoor(AssignDoorInput{DoorCode: "D05", AppointmentID: "appt-2212", At: planAt}))
	if l.Door() != "D05" {
		t.Fatalf("door after re-assign = %q", l.Door())
	}
}

func TestAssignDoorValidates(t *testing.T) {
	l := plannedLoad(t)
	wantErr(t, l.AssignDoor(AssignDoorInput{DoorCode: "", AppointmentID: "a", At: planAt}), ErrRequiredField)
	wantErr(t, l.AssignDoor(AssignDoorInput{DoorCode: "D04", AppointmentID: "", At: planAt}), ErrRequiredField)
	wantErr(t, l.AssignDoor(AssignDoorInput{DoorCode: "D04", AppointmentID: "a", At: time.Time{}}), ErrNoTimestamp)
}

func TestAttachTrailerWrongDoor(t *testing.T) {
	l := plannedLoad(t)
	mustLoad(t, l.AssignDoor(AssignDoorInput{DoorCode: "D04", AppointmentID: "appt-2211", At: planAt}))
	err := l.AttachTrailer(TrailerDocked{TrailerID: "trl-91", DoorCode: "D05", At: planAt})
	wantErr(t, err, ErrWrongDoor)
}

func TestAttachTrailerWithoutDoorRefusesTrailerNotDocked(t *testing.T) {
	l := plannedLoad(t)
	err := l.AttachTrailer(TrailerDocked{TrailerID: "trl-91", DoorCode: "D04", At: planAt})
	wantErr(t, err, ErrTrailerNotDocked)
}
