package memory

import (
	"context"
	"sync"

	"github.com/claudioed/outbound-shipping/internal/application/outbox"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// BolRepo is the in-memory ports.BolRepo with version-guarded saves and the
// one-document-per-load rule (ErrConcurrentModification on a second insert
// for the same load).
type BolRepo struct {
	mu     sync.Mutex
	rows   map[shared.BolNumber]*billoflading.BillOfLading
	byLoad map[shared.LoadID]shared.BolNumber
}

// NewBolRepo constructs an empty BolRepo.
func NewBolRepo() *BolRepo {
	return &BolRepo{rows: map[shared.BolNumber]*billoflading.BillOfLading{}, byLoad: map[shared.LoadID]shared.BolNumber{}}
}

// Get implements ports.BolRepo.
func (r *BolRepo) Get(_ context.Context, n shared.BolNumber) (*billoflading.BillOfLading, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.rows[n]
	if !ok {
		return nil, repository.ErrBolNotFound
	}
	return bolCopy(b)
}

// GetByLoad implements ports.BolRepo.
func (r *BolRepo) GetByLoad(_ context.Context, load shared.LoadID) (*billoflading.BillOfLading, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.byLoad[load]
	if !ok {
		return nil, repository.ErrBolNotFound
	}
	return bolCopy(r.rows[n])
}

// Save implements ports.BolRepo.
func (r *BolRepo) Save(_ context.Context, b *billoflading.BillOfLading, loadedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if loadedVersion == 0 {
		if _, exists := r.rows[b.BolNumber()]; exists {
			return repository.ErrConcurrentModification
		}
		if other, exists := r.byLoad[b.LoadID()]; exists {
			_ = other
			return repository.ErrConcurrentModification
		}
		fresh, err := bolCopy(b)
		if err != nil {
			return err
		}
		r.rows[b.BolNumber()] = fresh
		r.byLoad[b.LoadID()] = b.BolNumber()
		return nil
	}
	stored, exists := r.rows[b.BolNumber()]
	if !exists || stored.Version() != loadedVersion {
		return repository.ErrConcurrentModification
	}
	fresh, err := bolCopy(b)
	if err != nil {
		return err
	}
	r.rows[b.BolNumber()] = fresh
	return nil
}

func bolCopy(b *billoflading.BillOfLading) (*billoflading.BillOfLading, error) {
	return billoflading.Rehydrate(billoflading.RehydrateParams{
		BolNumber: b.BolNumber(), LoadID: b.LoadID(), Site: b.SiteID(),
		Carrier: b.Carrier(), CarrierScac: b.CarrierScac(),
		TrailerID: b.TrailerID(), SealNumber: b.SealNumber(),
		PackageCount: b.PackageCount(), TotalWeightG: b.TotalWeightG(),
		IssuedAt: b.IssuedAt(), VoidedAt: b.VoidedAt(),
		VoidReason: b.VoidReason(), State: b.State(), Version: b.Version(),
	})
}

// Copies is the in-memory bundle of the CPT schedule and layout copies
// (ports.CptCopy + ports.LayoutCopy): the CutoffCopy and the
// StagingLocationCopy of model.yaml.
type Copies struct {
	mu        sync.Mutex
	schedules map[shared.SiteID]repository.CptSchedule
	zones     map[string]repository.Zone
	locations map[string]repository.Location
}

// NewCopies constructs an empty Copies bundle.
func NewCopies() *Copies {
	return &Copies{
		schedules: map[shared.SiteID]repository.CptSchedule{},
		zones:     map[string]repository.Zone{},
		locations: map[string]repository.Location{},
	}
}

// Appointments is the in-memory dock-appointment and door-work copy
// (ports.AppointmentCopy + ports.DoorWorkCopy).
type Appointments struct {
	mu           sync.Mutex
	appointments map[shared.AppointmentID]repository.Appointment
	doorWork     []repository.DoorWorkCompletion
}

// NewAppointments constructs an empty Appointments bundle.
func NewAppointments() *Appointments {
	return &Appointments{appointments: map[shared.AppointmentID]repository.Appointment{}}
}

// Get implements ports.CptCopy.
func (c *Copies) Get(_ context.Context, site shared.SiteID) (repository.CptSchedule, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.schedules[site]
	if !ok {
		return repository.CptSchedule{}, repository.ErrCptScheduleUnknown
	}
	return s, nil
}

// Save implements ports.CptCopy.
func (c *Copies) Save(_ context.Context, s repository.CptSchedule) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.schedules[s.SiteID] = s
	return nil
}

// SaveZone implements ports.LayoutCopy.
func (c *Copies) SaveZone(_ context.Context, z repository.Zone) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.zones[z.ZoneID] = z
	return nil
}

// SaveLocation implements ports.LayoutCopy.
func (c *Copies) SaveLocation(_ context.Context, l repository.Location) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.locations[l.Code] = l
	return nil
}

// Place implements ports.LayoutCopy: the location joined with its zone.
func (c *Copies) Place(_ context.Context, code string) (repository.Place, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.locations[code]
	if !ok {
		return repository.Place{}, false, nil
	}
	p := repository.Place{Code: l.Code, ZoneID: l.ZoneID, Role: l.Role}
	if z, ok := c.zones[l.ZoneID]; ok {
		p.Site = z.SiteCode
	}
	return p, true, nil
}

// Save implements ports.AppointmentCopy.
func (c *Appointments) Save(_ context.Context, a repository.Appointment) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appointments[a.AppointmentID] = a
	return nil
}

// Get implements ports.AppointmentCopy.
func (c *Appointments) Get(_ context.Context, id shared.AppointmentID) (repository.Appointment, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.appointments[id]
	return a, ok, nil
}

// Record implements ports.DoorWorkCopy.
func (c *Appointments) Record(_ context.Context, w repository.DoorWorkCompletion) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, existing := range c.doorWork {
		if existing.TrailerID == w.TrailerID && existing.CompletedAt.Equal(w.CompletedAt) {
			return nil
		}
	}
	c.doorWork = append(c.doorWork, w)
	return nil
}

// DoorWork returns the recorded completions (a test helper).
func (c *Appointments) DoorWork() []repository.DoorWorkCompletion {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]repository.DoorWorkCompletion(nil), c.doorWork...)
}

// Snapshot implements Snapshotter (the copies roll back with the unit of
// work).
func (c *Copies) Snapshot() func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	schedules := make(map[shared.SiteID]repository.CptSchedule, len(c.schedules))
	for k, v := range c.schedules {
		schedules[k] = v
	}
	zones := make(map[string]repository.Zone, len(c.zones))
	for k, v := range c.zones {
		zones[k] = v
	}
	locations := make(map[string]repository.Location, len(c.locations))
	for k, v := range c.locations {
		locations[k] = v
	}
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.schedules, c.zones, c.locations = schedules, zones, locations
	}
}

// OutboxRecorder is the in-memory outbox inserter AND a test double that
// keeps every inserted message for assertions (the BDD suite reads it as
// "what left the service"). Insert never fails and there is no relay: the
// doubles only need the write side.
type OutboxRecorder struct {
	mu       sync.Mutex
	messages []outbox.Message
}

// NewOutboxRecorder constructs an empty recorder.
func NewOutboxRecorder() *OutboxRecorder { return &OutboxRecorder{} }

// Insert implements events.Inserter.
func (r *OutboxRecorder) Insert(_ context.Context, msgs ...outbox.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msgs...)
	return nil
}

// Messages returns a copy of every recorded message.
func (r *OutboxRecorder) Messages() []outbox.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]outbox.Message(nil), r.messages...)
}

// CountByType counts the recorded messages of one CloudEvents type on the
// integration topic only (the analytics twin excluded), so "exactly one
// ShipmentConfirmed" means one published occurrence.
func (r *OutboxRecorder) CountByType(eventType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	const integration = "warehouse.outbound-shipping.events"
	n := 0
	for _, m := range r.messages {
		if m.EventType == eventType && m.Topic == integration {
			n++
		}
	}
	return n
}

// Snapshot implements Snapshotter (rolls back with the unit of work).
func (r *OutboxRecorder) Snapshot() func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := append([]outbox.Message(nil), r.messages...)
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.messages = saved
	}
}

// boardRows is the shared state of the memory board store below.
type boardRows struct {
	mu    sync.Mutex
	rows  map[shared.LoadID]*repository.LoadBoardRow
	order []shared.LoadID
}

func (b *boardRows) get(id shared.LoadID) *repository.LoadBoardRow {
	if row, ok := b.rows[id]; ok {
		cp := *row
		return &cp
	}
	return &repository.LoadBoardRow{LoadID: id}
}

func (b *boardRows) put(row *repository.LoadBoardRow) {
	if _, ok := b.rows[row.LoadID]; !ok {
		b.order = append(b.order, row.LoadID)
	}
	cp := *row
	b.rows[row.LoadID] = &cp
}

// upsert loads-or-creates the row, applies mut and stores it.
func (b *boardRows) upsert(id shared.LoadID, mut func(*repository.LoadBoardRow)) {
	row := b.get(id)
	mut(row)
	b.put(row)
}
