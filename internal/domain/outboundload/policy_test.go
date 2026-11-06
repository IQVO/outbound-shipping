package outboundload

import (
	"testing"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// Assigning a door and attaching a trailer stay allowed while LOADING: a
// later appointment for the same cpt re-assigns the door.
func TestAssignDoorAndAttachWhileLoading(t *testing.T) {
	l := loadingLoad(t)
	mustLoad(t, l.AssignDoor(AssignDoorInput{DoorCode: "D05", AppointmentID: "appt-2212", At: planAt}))
	if l.Door() != "D05" || l.Appointment() != "appt-2212" {
		t.Fatalf("door = %q appointment = %q", l.Door(), l.Appointment())
	}
	mustLoad(t, l.AttachTrailer(TrailerDocked{TrailerID: "trl-92", DoorCode: "D05", At: planAt}))
	if l.Trailer() != "trl-92" {
		t.Fatalf("trailer = %q", l.Trailer())
	}
}

// The version bump of every transition is observable on the next event.
func TestVersionBumpsOnReopenAndCancel(t *testing.T) {
	l := closedLoad(t) // version 4 after close
	mustLoad(t, l.ReopenLoad(planAt))
	if l.Version() != 5 {
		t.Fatalf("version after reopen = %d, want 5", l.Version())
	}
	if evs := l.PullEvents(); len(evs) != 1 || evs[0].LoadVersion() != 5 {
		t.Fatalf("reopen event version wrong: %+v", evs)
	}

	p := plannedLoad(t) // version 1
	mustLoad(t, p.CancelLoad("cutoff withdrawn", planAt))
	if p.Version() != 2 {
		t.Fatalf("version after cancel = %d, want 2", p.Version())
	}
	if evs := p.PullEvents(); len(evs) != 1 || evs[0].LoadVersion() != 2 {
		t.Fatalf("cancel event version wrong: %+v", evs)
	}
}

// A short-shipped package does not count as loaded: the load records the
// entry and the count stays what the use case passed.
func TestCloseRecordsShortShipsOnly(t *testing.T) {
	l := loadingLoad(t)
	mustLoad(t, l.CloseLoad(CloseInput{
		ShortShips: []shared.ShortShip{
			{PackageID: "pkg-2077", Reason: "DIVERTED"},
			{PackageID: "pkg-2078", Reason: "NO-STOCK"},
		},
		LoadedCount: 1,
		At:          planAt,
	}))
	if l.PackageCount() != 1 {
		t.Fatalf("packageCount = %d, want 1", l.PackageCount())
	}
	if len(l.ShortShips()) != 2 {
		t.Fatalf("shortShips = %d, want 2", len(l.ShortShips()))
	}
}
