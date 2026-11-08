package usecases

import (
	"context"
	"strings"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// ApplyCptSchedule is the CPTScheduleChanged effect: replace the site's whole
// schedule copy (last writer wins per site, model.yaml CutoffCopy).
type ApplyCptSchedule struct {
	Intake
	Copies ports.CptCopy
}

// CptChange is one CPTScheduleChanged, decoded from the producer's payload.
type CptChange struct {
	SiteID   shared.SiteID
	Timezone string
	Cutoffs  []repository.CutoffEntry
	Version  int64
	Changed  time.Time
}

// Handle applies the schedule change.
func (uc *ApplyCptSchedule) Handle(ctx context.Context, eventID string, ch CptChange) (Outcome, error) {
	return uc.apply(ctx, CptConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if ch.SiteID == "" || ch.Timezone == "" || ch.Version < 1 || ch.Changed.IsZero() {
			return "", invalidf("cpt schedule change needs site_id, timezone, version and time")
		}
		for _, c := range ch.Cutoffs {
			if c.CptID == "" || c.LocalTime == "" || c.ShipMethod == "" {
				return "", invalidf("cpt entry %q needs cpt id, local time and ship method", c.CptID)
			}
		}
		if err := uc.Copies.Save(ctx, repository.CptSchedule{
			SiteID: ch.SiteID, Timezone: ch.Timezone,
			Cutoffs: ch.Cutoffs, Version: ch.Version, Changed: ch.Changed,
		}); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}

// ResolveCutoff maps a manifested cutoff INSTANT (fulfillment-execution's
// additive `cpt` field, RFC3339) to the (cpt id, operating date, ship method)
// a load is planned for, using the site's schedule copy: the instant in the
// site's timezone names the local date (the operating date) and the local
// time-of-day names the cutoff entry. An entry whose DaysOfWeek is empty
// matches every day; otherwise the local weekday must be listed. No match is
// repository.ErrCptNotFound — the load planner then ignores the manifest
// (a cutoff the site does not run cannot group packages).
func ResolveCutoff(s repository.CptSchedule, cutoff time.Time) (cptID shared.CptID, date outboundload.OperatingDate, method shared.ShipMethod, err error) {
	tz, tzErr := time.LoadLocation(s.Timezone)
	if tzErr != nil {
		return "", "", "", invalidf("site timezone %q: %v", s.Timezone, tzErr)
	}
	if cutoff.IsZero() {
		return "", "", "", invalidf("cutoff instant is required")
	}
	local := cutoff.In(tz)
	date = outboundload.OperatingDate(local.Format("2006-01-02"))
	if !date.Valid() {
		return "", "", "", invalidf("operating date from %q", date)
	}
	tod := local.Format("15:04")
	day := weekday(local)
	for _, c := range s.Cutoffs {
		if c.LocalTime != tod {
			continue
		}
		if len(c.DaysOfWeek) > 0 && !listsDay(c.DaysOfWeek, day) {
			continue
		}
		return shared.CptID(c.CptID), date, c.ShipMethod, nil
	}
	return "", "", "", repository.ErrCptNotFound
}

// weekday returns the three-letter weekday of t ("Mon"...), the schedule's
// days_of_week vocabulary.
func weekday(t time.Time) string {
	return t.Format("Mon")
}

// listsDay reports whether days names d, case-insensitively.
func listsDay(days []string, d string) bool {
	for _, x := range days {
		if strings.EqualFold(x, d) {
			return true
		}
	}
	return false
}
