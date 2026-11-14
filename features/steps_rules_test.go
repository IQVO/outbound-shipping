package features_test

// Step vocabulary of the documentation-derived rule features
// (features/*_rules.feature): consumer-policy fixtures with explicit event
// ids, so a scenario can replay one delivery or send a new one. The REST and
// assertion vocabularies live in steps_rules_http_test.go and
// steps_rules_assert_test.go. Every step goes through a use case or the real
// router, exactly like the first six scenarios.

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// rulesState is what the rule steps remember between a When and its Then.
type rulesState struct {
	seq     int                        // unique suffix of fixture event ids and idempotency keys
	resp    *httptest.ResponseRecorder // the last REST answer
	body    any                        // that answer's decoded JSON (nil when not JSON)
	byKey   map[string]string          // first answer body per Idempotency-Key
	prevID  string                     // first item id of the previous page
	outcome usecases.Outcome           // the last consumer outcome
	err     error                      // the last consumer error
	replay  func() (usecases.Outcome, error)
}

func (w *world) nextID(prefix string) string {
	w.rules.seq++
	return fmt.Sprintf("%s-%d", prefix, w.rules.seq)
}

// consume runs one consumer delivery under a CloudEvents id, remembering the
// outcome and how to deliver the very same event again.
func (w *world) consume(id string, deliver func(id string) (usecases.Outcome, error)) {
	run := func() (usecases.Outcome, error) {
		out, err := deliver(id)
		w.rules.outcome, w.rules.err = out, err
		return out, err
	}
	w.rules.replay = run
	_, _ = run()
}

// mustApply runs a fixture delivery a Given step needs to have worked. It
// leaves the remembered outcome and the repeatable delivery alone, so a
// later "the same event is delivered again" repeats the scenario's subject.
func (w *world) mustApply(id string, deliver func(id string) (usecases.Outcome, error)) error {
	_, err := deliver(id)
	return err
}

func registerRulesSteps(sc *godog.ScenarioContext, w *world) {
	registerRulesFixtureSteps(sc, w)
	registerRulesManifestSteps(sc, w)
	registerRulesYardSteps(sc, w)
	registerRulesOutcomeSteps(sc, w)
	registerRulesHTTPSteps(sc, w)
	registerRulesStateAssertions(sc, w)
	registerRulesEventAssertions(sc, w)
	registerRulesReadAssertions(sc, w)
}

// --- fixtures: schedule, layout, appointments -----------------------------

func registerRulesFixtureSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^site "([^"]*)" in timezone "([^"]*)" runs these cutoffs:$`,
		func(site, tz string, table *godog.Table) error {
			var entries []repository.CutoffEntry
			for _, row := range table.Rows[1:] {
				e := repository.CutoffEntry{
					CptID: row.Cells[0].Value, LocalTime: row.Cells[1].Value,
					ShipMethod: shared.ShipMethod(row.Cells[2].Value),
				}
				if days := row.Cells[3].Value; days != "" {
					e.DaysOfWeek = strings.Split(days, ",")
				}
				entries = append(entries, e)
			}
			return w.mustApply(w.nextID("rl-cpt"), func(id string) (usecases.Outcome, error) {
				return w.svc.applyCpt.Handle(w.ctx, id, usecases.CptChange{
					SiteID: shared.SiteID(site), Timezone: tz, Cutoffs: entries,
					Version: int64(w.rules.seq), Changed: bddDock,
				})
			})
		})

	sc.Step(`^a schedule change for site "([^"]*)" (with an entry lacking a ship method|at version 0) arrives$`,
		func(site, defect string) error {
			change := usecases.CptChange{
				SiteID: shared.SiteID(site), Timezone: "UTC", Version: 9, Changed: bddDock,
				Cutoffs: []repository.CutoffEntry{{CptID: "cpt-2000", LocalTime: "20:00", ShipMethod: "EXPRESS"}},
			}
			if strings.HasPrefix(defect, "with") {
				change.Cutoffs[0].ShipMethod = ""
			} else {
				change.Version = 0
			}
			w.consume(w.nextID("rl-cpt"), func(id string) (usecases.Outcome, error) {
				return w.svc.applyCpt.Handle(w.ctx, id, change)
			})
			return nil
		})

	sc.Step(`^zone "([^"]*)" belongs to site "([^"]*)"$`, func(zone, site string) error {
		return w.mustApply(w.nextID("rl-zone"), func(id string) (usecases.Outcome, error) {
			return w.svc.applyLay.HandleZone(w.ctx, id, usecases.ZoneChange{ZoneID: zone, SiteCode: site, ZoneCode: zone})
		})
	})

	sc.Step(`^location "([^"]*)" is registered in zone "([^"]*)" with role "([^"]*)"$`,
		func(code, zone, role string) error {
			return w.mustApply(w.nextID("rl-loc"), func(id string) (usecases.Outcome, error) {
				return w.svc.applyLay.HandleLocation(w.ctx, id, usecases.LocationChange{Code: code, ZoneID: zone, Role: role})
			})
		})

	sc.Step(`^a location registration without a zone id arrives$`, func() error {
		w.consume(w.nextID("rl-loc"), func(id string) (usecases.Outcome, error) {
			return w.svc.applyLay.HandleLocation(w.ctx, id, usecases.LocationChange{Code: "STG-X-1", Role: repository.RoleStaging})
		})
		return nil
	})

	sc.Step(`^an? "(OUTBOUND|INBOUND)" appointment "([^"]*)"(?: for cpt "([^"]*)")? at door "([^"]*)" carried by "([^"]*)" is booked$`,
		func(direction, appt, cpt, door, carrier string) error {
			w.consume(w.nextID("rl-appt"), func(id string) (usecases.Outcome, error) {
				return w.svc.book.Handle(w.ctx, id, usecases.Appointment{
					AppointmentID: shared.AppointmentID(appt), Site: "SITE-1", Direction: direction,
					DoorCode: shared.DoorCode(door), CptID: shared.CptID(cpt), Carrier: carrier,
					CarrierScac: carrier, WindowStart: bddDock, WindowEnd: bddCutoff,
					Version: int64(w.rules.seq), BookedAt: bddDock,
				})
			})
			return w.rules.err
		})

	sc.Step(`^the appointment names no carrier$`, func() error {
		w.svc.appt.Carrier, w.svc.appt.CarrierScac = "", ""
		return nil
	})

	sc.Step(`^an empty planned load "([^"]*)" exists for the "16:00" cutoff of site "SITE-1"$`,
		func(id string) error {
			// A load is planned by the manifest of its first package, so no
			// event sequence leaves one without packages: the cancel rule
			// ("a planned load that has no packages") needs this fixture.
			l, _, err := outboundload.Plan(outboundload.PlanInput{
				LoadID: shared.LoadID(id), Site: "SITE-1", Cpt: "cpt-1600",
				OperatingDate: "2026-11-06", ShipMethod: "STDOUT-2DAY",
				Cutoff: bddCutoff, At: bddDock,
			})
			if err != nil {
				return err
			}
			return w.svc.loads.Save(w.ctx, l, 0)
		})
}

// --- the PackageManifested / PackageDiverted policies ---------------------

func (w *world) manifestFact(id, pkg, order string, weight float64, cutoff, drop string) {
	m := usecases.Manifest{
		PackageID: shared.PackageID(pkg), SiteID: "SITE-1", SourceOrderID: order,
		OrderRef: order + "-w1", WeightG: weight, ManifestedAt: bddDock,
	}
	if t, err := time.Parse(time.RFC3339, cutoff); err == nil {
		m.Cutoff = t
	}
	switch drop {
	case "site_id":
		m.SiteID = ""
	case "cpt":
		m.Cutoff = time.Time{}
	case "package id":
		m.PackageID = ""
	}
	w.consume(id, func(id string) (usecases.Outcome, error) { return w.svc.expect.Handle(w.ctx, id, m) })
}

func registerRulesManifestSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^package "([^"]*)" of order "([^"]*)"(?: weighing (\d+) g)? is manifested for the cutoff "([^"]*)"$`,
		func(pkg, order, grams, cutoff string) error {
			weight := 1200.0
			if grams != "" {
				fmt.Sscanf(grams, "%f", &weight)
			}
			w.manifestFact("rl-man-"+pkg, pkg, order, weight, cutoff, "")
			return w.rules.err
		})

	sc.Step(`^a manifest of package "([^"]*)" of order "([^"]*)" for the cutoff "([^"]*)" arrives without a "(site_id|cpt)"$`,
		func(pkg, order, cutoff, missing string) error {
			w.manifestFact("rl-man-"+pkg+"-no-"+missing, pkg, order, 1200, cutoff, missing)
			return w.rules.err
		})

	sc.Step(`^package "([^"]*)" of order "([^"]*)" is manifested again for the cutoff "([^"]*)" under a new event id$`,
		func(pkg, order, cutoff string) error {
			w.manifestFact(w.nextID("rl-man-again-"+pkg), pkg, order, 1200, cutoff, "")
			return w.rules.err
		})

	sc.Step(`^a manifest without a package id for the cutoff "([^"]*)" arrives$`, func(cutoff string) error {
		w.manifestFact(w.nextID("rl-man-nopkg"), "pkg-x", "order-x", 1200, cutoff, "package id")
		return nil
	})

	sc.Step(`^package "([^"]*)" is diverted$`, func(pkg string) error {
		w.consume(w.nextID("rl-divert-"+pkg), func(id string) (usecases.Outcome, error) {
			return w.svc.divert.Handle(w.ctx, id, shared.PackageID(pkg), bddDepart)
		})
		return w.rules.err
	})
}

// --- the yard facts -------------------------------------------------------

func registerRulesYardSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^trailer "([^"]*)" docks at door "([^"]*)"$`, func(trailer, door string) error {
		w.consume(w.nextID("rl-dock"), func(id string) (usecases.Outcome, error) {
			return w.svc.dock.Handle(w.ctx, id, usecases.Docking{
				TrailerID: shared.TrailerID(trailer), Site: "SITE-1", DoorCode: shared.DoorCode(door), DockedAt: bddDock,
			})
		})
		return w.rules.err
	})

	sc.Step(`^trailer "([^"]*)" docks without naming a door$`, func(trailer string) error {
		w.consume(w.nextID("rl-dock"), func(id string) (usecases.Outcome, error) {
			return w.svc.dock.Handle(w.ctx, id, usecases.Docking{
				TrailerID: shared.TrailerID(trailer), Site: "SITE-1", DockedAt: bddDock,
			})
		})
		return nil
	})

	sc.Step(`^door work completes for trailer "([^"]*)" at door "([^"]*)"$`, func(trailer, door string) error {
		w.consume(w.nextID("rl-doorwork"), func(id string) (usecases.Outcome, error) {
			return w.svc.doorWork.Handle(w.ctx, id, repository.DoorWorkCompletion{
				TrailerID: shared.TrailerID(trailer), Site: "SITE-1", DoorCode: shared.DoorCode(door), CompletedAt: bddDepart,
			})
		})
		return w.rules.err
	})

	sc.Step(`^trailer "([^"]*)" departs without a seal$`, func(trailer string) error {
		return w.applyDeparture(w.ctx, w.nextID("rl-depart-noseal-"+trailer), usecases.Departure{
			TrailerID: shared.TrailerID(trailer), DepartedAt: bddDepart,
		})
	})

	sc.Step(`^a departure without a (trailer id|site) arrives$`, func(missing string) error {
		d := usecases.Departure{TrailerID: "trl-91", Site: "SITE-1", SealNumber: "SL-1", DepartedAt: bddDepart}
		if missing == "site" {
			d.Site = ""
		} else {
			d.TrailerID = ""
		}
		w.consume(w.nextID("rl-depart-bad"), func(id string) (usecases.Outcome, error) {
			return w.svc.depart.Handle(w.ctx, id, d)
		})
		return nil
	})

	sc.Step(`^trailer "([^"]*)" of site "([^"]*)" departs with seal "([^"]*)"$`, func(trailer, site, seal string) error {
		return w.applyDeparture(w.ctx, w.nextID("rl-depart-"+trailer), usecases.Departure{
			TrailerID: shared.TrailerID(trailer), Site: shared.SiteID(site), SealNumber: seal, DepartedAt: bddDepart,
		})
	})

	sc.Step(`^trailer "([^"]*)" departs with seal "([^"]*)" but the confirm cannot complete$`, func(trailer, seal string) error {
		w.consume(w.nextID("rl-depart-"+trailer), func(id string) (usecases.Outcome, error) {
			return w.svc.depart.Handle(w.ctx, id, usecases.Departure{
				TrailerID: shared.TrailerID(trailer), Site: "SITE-1", SealNumber: seal, DepartedAt: bddDepart,
			})
		})
		if w.rules.err == nil {
			return errors.New("the departure was accepted, want a refused confirm")
		}
		return nil
	})
}

// --- consumer outcomes ----------------------------------------------------

func registerRulesOutcomeSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the consumer outcome is "(applied|ignored|duplicate)"$`, func(want string) error {
		if w.rules.err != nil {
			return fmt.Errorf("the delivery failed: %v", w.rules.err)
		}
		if string(w.rules.outcome) != want {
			return fmt.Errorf("outcome = %q, want %q", w.rules.outcome, want)
		}
		return nil
	})

	sc.Step(`^the event is refused as invalid$`, func() error {
		if !errors.Is(w.rules.err, usecases.ErrInvalidEvent) {
			return fmt.Errorf("err = %v, want ErrInvalidEvent", w.rules.err)
		}
		return nil
	})

	sc.Step(`^the same event is delivered again$`, func() error {
		if w.rules.replay == nil {
			return errors.New("no delivery to repeat")
		}
		_, err := w.rules.replay()
		return err
	})
}

// loadByID reads a load straight from the repository.
func (w *world) loadByID(ctx context.Context, id string) (*outboundload.OutboundLoad, error) {
	return w.svc.loads.Get(ctx, shared.LoadID(id))
}
