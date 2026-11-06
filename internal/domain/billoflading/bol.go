package billoflading

import (
	"fmt"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// State is the document lifecycle. Exactly the model's two states.
type State string

// The bill of lading states (model.yaml): ISSUED, VOIDED.
const (
	StateIssued State = "ISSUED"
	StateVoided State = "VOIDED"
)

// validStates are the states Rehydrate accepts.
var validStates = []State{StateIssued, StateVoided}

// BillOfLading is the aggregate root: the shipping document of one
// confirmed load, issued exactly once per load in v1.
//
// States: ISSUED -> VOIDED. Version is 1 when issued and 2 once voided.
// Invariants (model.yaml): issued only for a SHIP_CONFIRMED load, exactly
// once per load (ErrBolAlreadyIssued); issued only with the seal number
// yard-management recorded (ErrSealMissing).
type BillOfLading struct {
	bolNumber    shared.BolNumber
	loadID       shared.LoadID
	site         shared.SiteID
	carrier      string
	carrierScac  string
	trailerID    shared.TrailerID
	sealNumber   string
	packageCount int
	totalWeightG float64
	issuedAt     time.Time
	voidedAt     time.Time
	voidReason   string
	state        State
	version      int64
	events       []Event
}

// IssueInput is everything Issue needs: the load facts from the ship
// confirmation, plus the cross-aggregate answers the use case owns — whether
// the load is SHIP_CONFIRMED and whether a document already exists for it.
type IssueInput struct {
	BolNumber          shared.BolNumber
	LoadID             shared.LoadID
	Site               shared.SiteID
	LoadConfirmed      bool
	ExistingBolForLoad bool
	Carrier            string
	CarrierScac        string
	TrailerID          shared.TrailerID
	SealNumber         string
	PackageCount       int
	TotalWeightG       float64
	At                 time.Time
}

// Issue creates an ISSUED bill of lading at version 1 and raises
// BillOfLadingIssued. It enforces both model invariants: the load must be
// SHIP_CONFIRMED and no document may already exist for it
// (ErrBolAlreadyIssued), and the seal must be present (ErrSealMissing).
func Issue(in IssueInput) (*BillOfLading, []Event, error) {
	if err := validateIssue(in); err != nil {
		return nil, nil, err
	}
	b := &BillOfLading{
		bolNumber: in.BolNumber, loadID: in.LoadID, site: in.Site,
		carrier: in.Carrier, carrierScac: in.CarrierScac, trailerID: in.TrailerID,
		sealNumber: in.SealNumber, packageCount: in.PackageCount,
		totalWeightG: in.TotalWeightG, issuedAt: in.At.UTC(),
		state: StateIssued, version: 1,
	}
	b.raise(BillOfLadingIssued{
		Header: b.header(in.At), LoadID: b.loadID, Carrier: b.carrier,
		CarrierScac: b.carrierScac, TrailerID: b.trailerID, SealNumber: b.sealNumber,
		PackageCount: b.packageCount, TotalWeightG: b.totalWeightG, IssuedAt: b.issuedAt,
	})
	return b, b.PullEvents(), nil
}

func validateIssue(in IssueInput) error {
	if in.BolNumber == "" || in.LoadID == "" || in.Site == "" || in.Carrier == "" || in.TrailerID == "" {
		return ErrRequiredField
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	if in.PackageCount < 1 {
		return ErrInvalidPackageCount
	}
	if in.TotalWeightG < 0 {
		return ErrInvalidWeight
	}
	if in.SealNumber == "" {
		return ErrSealMissing
	}
	if in.ExistingBolForLoad {
		return fmt.Errorf("%w: a bill of lading already exists for load %s", ErrBolAlreadyIssued, in.LoadID)
	}
	if !in.LoadConfirmed {
		return fmt.Errorf("%w: load %s is not ship-confirmed", ErrBolAlreadyIssued, in.LoadID)
	}
	return nil
}

// Void moves ISSUED -> VOIDED and raises BillOfLadingVoided. The shipment
// itself is not undone.
func (b *BillOfLading) Void(reason string, at time.Time) error {
	if b.state == StateVoided {
		return ErrBolAlreadyVoided
	}
	if reason == "" {
		return ErrVoidReasonRequired
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	b.state = StateVoided
	b.voidedAt = at.UTC()
	b.voidReason = reason
	b.version++
	b.raise(BillOfLadingVoided{Header: b.header(at), LoadID: b.loadID, Reason: reason, VoidedAt: b.voidedAt})
	return nil
}

// RehydrateParams is the persisted state of a document, used by repository
// adapters to rebuild it without re-raising events.
type RehydrateParams struct {
	BolNumber    shared.BolNumber
	LoadID       shared.LoadID
	Site         shared.SiteID
	Carrier      string
	CarrierScac  string
	TrailerID    shared.TrailerID
	SealNumber   string
	PackageCount int
	TotalWeightG float64
	IssuedAt     time.Time
	VoidedAt     time.Time
	VoidReason   string
	State        State
	Version      int64
}

// Rehydrate rebuilds a persisted document, validating every field. A
// rehydrated document has no pending events.
func Rehydrate(p RehydrateParams) (*BillOfLading, error) {
	if err := validateRehydrate(p); err != nil {
		return nil, err
	}
	return &BillOfLading{
		bolNumber: p.BolNumber, loadID: p.LoadID, site: p.Site,
		carrier: p.Carrier, carrierScac: p.CarrierScac, trailerID: p.TrailerID,
		sealNumber: p.SealNumber, packageCount: p.PackageCount,
		totalWeightG: p.TotalWeightG, issuedAt: p.IssuedAt.UTC(),
		voidedAt: p.VoidedAt.UTC(), voidReason: p.VoidReason,
		state: p.State, version: p.Version,
	}, nil
}

func validateRehydrate(p RehydrateParams) error {
	if p.BolNumber == "" || p.LoadID == "" || p.Site == "" || p.Carrier == "" || p.TrailerID == "" {
		return ErrRequiredField
	}
	if p.SealNumber == "" {
		return ErrSealMissing
	}
	if p.PackageCount < 1 {
		return ErrInvalidPackageCount
	}
	if !stateValid(p.State) {
		return ErrInvalidState
	}
	if p.Version < 1 {
		return ErrInvalidVersion
	}
	return nil
}

func stateValid(s State) bool {
	return s == StateIssued || s == StateVoided
}

// PullEvents returns the events recorded since the last PullEvents (or since
// construction) and clears them. Rehydrated documents never have any.
func (b *BillOfLading) PullEvents() []Event {
	out := b.events
	b.events = nil
	return out
}

func (b *BillOfLading) raise(e Event) { b.events = append(b.events, e) }

func (b *BillOfLading) header(at time.Time) Header {
	return Header{Bol: b.bolNumber, Site: b.site, Version: b.version, At: at.UTC()}
}

// BolNumber returns the document number.
func (b *BillOfLading) BolNumber() shared.BolNumber { return b.bolNumber }

// LoadID returns the load the document certifies.
func (b *BillOfLading) LoadID() shared.LoadID { return b.loadID }

// SiteID returns the site of the load.
func (b *BillOfLading) SiteID() shared.SiteID { return b.site }

// Carrier returns the carrier.
func (b *BillOfLading) Carrier() string { return b.carrier }

// CarrierScac returns the carrier SCAC ("" when the appointment had none).
func (b *BillOfLading) CarrierScac() string { return b.carrierScac }

// TrailerID returns the trailer the load left on.
func (b *BillOfLading) TrailerID() shared.TrailerID { return b.trailerID }

// SealNumber returns the seal yard-management recorded.
func (b *BillOfLading) SealNumber() string { return b.sealNumber }

// PackageCount returns the certified package count.
func (b *BillOfLading) PackageCount() int { return b.packageCount }

// TotalWeightG returns the total weight in grams.
func (b *BillOfLading) TotalWeightG() float64 { return b.totalWeightG }

// IssuedAt returns when the document was issued.
func (b *BillOfLading) IssuedAt() time.Time { return b.issuedAt }

// VoidedAt returns when the document was voided (zero while ISSUED).
func (b *BillOfLading) VoidedAt() time.Time { return b.voidedAt }

// VoidReason returns the void reason ("" while ISSUED).
func (b *BillOfLading) VoidReason() string { return b.voidReason }

// State returns the document's current state.
func (b *BillOfLading) State() State { return b.state }

// Version returns the aggregate version.
func (b *BillOfLading) Version() int64 { return b.version }
