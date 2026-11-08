package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// BolRepo is the pgxpool-backed ports.BolRepo. The one-document-per-load
// rule is the UNIQUE load_id constraint (bills_of_lading), surfaced as
// repository.ErrConcurrentModification on a second insert for one load — the
// use case pre-checks with GetByLoad and the constraint is the race backstop.
type BolRepo struct {
	pool *pgxpool.Pool
}

// NewBolRepo constructs a BolRepo over pool.
func NewBolRepo(pool *pgxpool.Pool) *BolRepo { return &BolRepo{pool: pool} }

const bolCols = `
	SELECT bol_number, load_id, site_id, carrier, carrier_scac, trailer_id,
	       seal_number, package_count, total_weight_g, issued_at, voided_at,
	       void_reason, state, version`

func scanBol(row pgx.Row) (*billoflading.BillOfLading, error) {
	var p billoflading.RehydrateParams
	if err := row.Scan(&p.BolNumber, &p.LoadID, &p.Site, &p.Carrier,
		&p.CarrierScac, &p.TrailerID, &p.SealNumber, &p.PackageCount,
		&p.TotalWeightG, &p.IssuedAt, &p.VoidedAt, &p.VoidReason, &p.State,
		&p.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrBolNotFound
		}
		return nil, fmt.Errorf("scan bill of lading: %w", err)
	}
	return billoflading.Rehydrate(p)
}

// Get implements ports.BolRepo.
func (r *BolRepo) Get(ctx context.Context, n shared.BolNumber) (*billoflading.BillOfLading, error) {
	return scanBol(queryFor(ctx, r.pool).QueryRow(ctx, bolCols+`
		FROM bills_of_lading WHERE bol_number = $1`, n))
}

// GetByLoad implements ports.BolRepo.
func (r *BolRepo) GetByLoad(ctx context.Context, load shared.LoadID) (*billoflading.BillOfLading, error) {
	return scanBol(queryFor(ctx, r.pool).QueryRow(ctx, bolCols+`
		FROM bills_of_lading WHERE load_id = $1`, load))
}

// Save implements ports.BolRepo.
func (r *BolRepo) Save(ctx context.Context, b *billoflading.BillOfLading, loadedVersion int64) error {
	var (
		tag pgconn.CommandTag
		err error
	)
	if loadedVersion == 0 {
		tag, err = queryFor(ctx, r.pool).Exec(ctx, `
			INSERT INTO bills_of_lading
			    (bol_number, load_id, site_id, carrier, carrier_scac,
			     trailer_id, seal_number, package_count, total_weight_g,
			     issued_at, voided_at, void_reason, state, version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			b.BolNumber(), b.LoadID(), b.SiteID(), b.Carrier(), b.CarrierScac(),
			b.TrailerID(), b.SealNumber(), b.PackageCount(), b.TotalWeightG(),
			b.IssuedAt(), b.VoidedAt(), b.VoidReason(), b.State(), b.Version())
	} else {
		tag, err = queryFor(ctx, r.pool).Exec(ctx, `
			UPDATE bills_of_lading SET
			    carrier=$2, carrier_scac=$3, trailer_id=$4, seal_number=$5,
			    package_count=$6, total_weight_g=$7, issued_at=$8,
			    voided_at=$9, void_reason=$10, state=$11, version=$12
			WHERE bol_number=$1 AND version=$13`,
			b.BolNumber(), b.Carrier(), b.CarrierScac(), b.TrailerID(),
			b.SealNumber(), b.PackageCount(), b.TotalWeightG(), b.IssuedAt(),
			b.VoidedAt(), b.VoidReason(), b.State(), b.Version(), loadedVersion)
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: a bill of lading already exists for load %s",
				repository.ErrConcurrentModification, b.LoadID())
		}
		return fmt.Errorf("save bill of lading %s: %w", b.BolNumber(), err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: bill of lading %s (loaded version %d)",
			repository.ErrConcurrentModification, b.BolNumber(), loadedVersion)
	}
	return nil
}

// Copies is the pgxpool-backed bundle of the event-fed local copies: the CPT
// schedule (ports.CptCopy) and the layout (ports.LayoutCopy).
type Copies struct {
	pool *pgxpool.Pool
}

// NewCopies constructs a Copies bundle over pool.
func NewCopies(pool *pgxpool.Pool) *Copies { return &Copies{pool: pool} }

// Get implements ports.CptCopy.
func (c *Copies) Get(ctx context.Context, site shared.SiteID) (repository.CptSchedule, error) {
	var (
		s         repository.CptSchedule
		cutoffsJS []byte
	)
	err := queryFor(ctx, c.pool).QueryRow(ctx, `
		SELECT site_id, timezone, cutoffs, version FROM cpt_schedules
		WHERE site_id = $1`, site).
		Scan(&s.SiteID, &s.Timezone, &cutoffsJS, &s.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.CptSchedule{}, repository.ErrCptScheduleUnknown
		}
		return repository.CptSchedule{}, fmt.Errorf("scan cpt schedule: %w", err)
	}
	if err := json.Unmarshal(cutoffsJS, &s.Cutoffs); err != nil {
		return repository.CptSchedule{}, fmt.Errorf("decode cutoffs of %s: %w", site, err)
	}
	return s, nil
}

// Save implements ports.CptCopy.
func (c *Copies) Save(ctx context.Context, s repository.CptSchedule) error {
	cutoffs, err := json.Marshal(s.Cutoffs)
	if err != nil {
		return fmt.Errorf("encode cutoffs of %s: %w", s.SiteID, err)
	}
	_, err = queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO cpt_schedules (site_id, timezone, cutoffs, version)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (site_id) DO UPDATE SET
		    timezone = EXCLUDED.timezone, cutoffs = EXCLUDED.cutoffs,
		    version = EXCLUDED.version, updated_at = now()`,
		s.SiteID, s.Timezone, cutoffs, s.Version)
	if err != nil {
		return fmt.Errorf("save cpt schedule of %s: %w", s.SiteID, err)
	}
	return nil
}

// SaveZone implements ports.LayoutCopy.
func (c *Copies) SaveZone(ctx context.Context, z repository.Zone) error {
	_, err := queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO zones (zone_id, site_code, zone_code) VALUES ($1,$2,$3)
		ON CONFLICT (zone_id) DO UPDATE SET
		    site_code = EXCLUDED.site_code, zone_code = EXCLUDED.zone_code,
		    updated_at = now()`, z.ZoneID, z.SiteCode, z.ZoneCode)
	if err != nil {
		return fmt.Errorf("save zone %s: %w", z.ZoneID, err)
	}
	return nil
}

// SaveLocation implements ports.LayoutCopy.
func (c *Copies) SaveLocation(ctx context.Context, l repository.Location) error {
	_, err := queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO locations (location_code, zone_id, role) VALUES ($1,$2,$3)
		ON CONFLICT (location_code) DO UPDATE SET
		    zone_id = EXCLUDED.zone_id, role = EXCLUDED.role, updated_at = now()`,
		l.Code, l.ZoneID, l.Role)
	if err != nil {
		return fmt.Errorf("save location %s: %w", l.Code, err)
	}
	return nil
}

// Place implements ports.LayoutCopy: the location joined with its zone (the
// site through zoneId, never by parsing the code).
func (c *Copies) Place(ctx context.Context, code string) (repository.Place, bool, error) {
	var p repository.Place
	err := queryFor(ctx, c.pool).QueryRow(ctx, `
		SELECT l.location_code, COALESCE(z.site_code, ''), COALESCE(l.zone_id,''), l.role
		FROM locations l
		LEFT JOIN zones z ON z.zone_id = l.zone_id
		WHERE l.location_code = $1`, code).
		Scan(&p.Code, &p.Site, &p.ZoneID, &p.Role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Place{}, false, nil
		}
		return repository.Place{}, false, fmt.Errorf("scan place %s: %w", code, err)
	}
	return p, true, nil
}

// Appointments is the pgxpool-backed dock-appointment and door-work copy
// (ports.AppointmentCopy + ports.DoorWorkCopy).
type Appointments struct {
	pool *pgxpool.Pool
}

// NewAppointments constructs an Appointments bundle over pool.
func NewAppointments(pool *pgxpool.Pool) *Appointments { return &Appointments{pool: pool} }

// Save implements ports.AppointmentCopy.
func (a *Appointments) Save(ctx context.Context, ap repository.Appointment) error {
	_, err := queryFor(ctx, a.pool).Exec(ctx, `
		INSERT INTO dock_appointments
		    (appointment_id, site_id, direction, door_code, cpt_id, carrier,
		     carrier_scac, window_start, window_end, version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (appointment_id) DO UPDATE SET
		    site_id = EXCLUDED.site_id, direction = EXCLUDED.direction,
		    door_code = EXCLUDED.door_code, cpt_id = EXCLUDED.cpt_id,
		    carrier = EXCLUDED.carrier, carrier_scac = EXCLUDED.carrier_scac,
		    window_start = EXCLUDED.window_start, window_end = EXCLUDED.window_end,
		    version = EXCLUDED.version, booked_at = now()`,
		ap.AppointmentID, ap.Site, ap.Direction, ap.DoorCode, ap.CptID,
		ap.Carrier, ap.CarrierScac, ap.WindowStart, ap.WindowEnd, ap.Version)
	if err != nil {
		return fmt.Errorf("save appointment %s: %w", ap.AppointmentID, err)
	}
	return nil
}

// Get implements ports.AppointmentCopy.
func (a *Appointments) Get(ctx context.Context, id shared.AppointmentID) (repository.Appointment, bool, error) {
	var ap repository.Appointment
	err := queryFor(ctx, a.pool).QueryRow(ctx, `
		SELECT appointment_id, site_id, direction, door_code, cpt_id, carrier,
		       carrier_scac, window_start, window_end, version
		FROM dock_appointments WHERE appointment_id = $1`, id).
		Scan(&ap.AppointmentID, &ap.Site, &ap.Direction, &ap.DoorCode, &ap.CptID,
			&ap.Carrier, &ap.CarrierScac, &ap.WindowStart, &ap.WindowEnd, &ap.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Appointment{}, false, nil
		}
		return repository.Appointment{}, false, fmt.Errorf("scan appointment %s: %w", id, err)
	}
	return ap, true, nil
}

// Record implements ports.DoorWorkCopy.
func (a *Appointments) Record(ctx context.Context, w repository.DoorWorkCompletion) error {
	_, err := queryFor(ctx, a.pool).Exec(ctx, `
		INSERT INTO door_work_completions
		    (trailer_id, site_id, door_code, appointment_id, completed_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (trailer_id, completed_at) DO NOTHING`,
		w.TrailerID, w.Site, w.DoorCode, w.AppointmentID, w.CompletedAt)
	if err != nil {
		return fmt.Errorf("record door work of %s: %w", w.TrailerID, err)
	}
	return nil
}
