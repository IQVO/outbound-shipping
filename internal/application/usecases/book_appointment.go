package usecases

import (
	"context"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// BookAppointment is the DoorFromAppointment policy (model.yaml): on an
// OUTBOUND DockAppointmentBooked naming a cpt, save the appointment to the
// local copy and assign its door to the open load of that (site, cpt) —
// LoadDoorAssigned. An appointment naming no cpt, an INBOUND one, or one
// arriving before any load of that cpt exists, only updates the copy: a
// later appointment for the same cpt re-assigns, so nothing is lost.
type BookAppointment struct {
	Intake
	Loads  ports.LoadRepo
	Copies ports.AppointmentCopy
	Sink   ports.EventSink
	Clock  ports.Clock
}

// Appointment is one DockAppointmentBooked, decoded from the producer's
// payload. CptID, CarrierScac and the window are nullable on the wire:
// empty/zero means "not set".
type Appointment struct {
	AppointmentID shared.AppointmentID
	Site          shared.SiteID
	Direction     string
	DoorCode      shared.DoorCode
	CptID         shared.CptID
	Carrier       string
	CarrierScac   string
	WindowStart   time.Time
	WindowEnd     time.Time
	Version       int64
	BookedAt      time.Time
}

// Handle applies one appointment booking.
func (uc *BookAppointment) Handle(ctx context.Context, eventID string, a Appointment) (Outcome, error) {
	return uc.apply(ctx, DockConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if a.AppointmentID == "" || a.Site == "" || a.DoorCode == "" || a.Direction == "" || a.Version < 1 {
			return "", invalidf("appointment needs id, site, direction, door and version")
		}
		if err := uc.Copies.Save(ctx, repository.Appointment{
			AppointmentID: a.AppointmentID, Site: a.Site, Direction: a.Direction,
			DoorCode: a.DoorCode, CptID: a.CptID, Carrier: a.Carrier,
			CarrierScac: a.CarrierScac, WindowStart: a.WindowStart,
			WindowEnd: a.WindowEnd, Version: a.Version,
		}); err != nil {
			return "", err
		}
		if a.Direction != "OUTBOUND" || a.CptID == "" {
			return OutcomeIgnored, nil
		}
		loads, err := uc.Loads.ListOpenByCpt(ctx, a.Site, a.CptID)
		if err != nil {
			return "", err
		}
		if len(loads) == 0 {
			// No load of that cpt yet: the copy is saved, the next
			// appointment for the same cpt assigns.
			return OutcomeIgnored, nil
		}
		load := loads[0]
		if err := load.AssignDoor(outboundload.AssignDoorInput{
			DoorCode: a.DoorCode, AppointmentID: a.AppointmentID, At: uc.Clock.Now(),
		}); err != nil {
			return "", err
		}
		if err := uc.Loads.Save(ctx, load, load.Version()-1); err != nil {
			return "", err
		}
		if err := uc.Sink.PublishLoadEvents(ctx, load.PullEvents()...); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}
