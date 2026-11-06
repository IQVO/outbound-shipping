// Package features_test hosts the godog (Cucumber for Go) acceptance suite. The
// wave-4 scenarios under features/ are DOMAIN-level: they drive the three
// aggregates directly, composing the wave-5 policies (ConfirmOnDeparture,
// FlagDepartureWithoutClose, SweepMissedLoad, DropDiverted) exactly as the
// service will, and feeding the yard facts (TrailerDeparted) as fixture
// events validated against the proposed yard model. The HTTP/Kafka surface
// arrives in wave 5 and reuses the same steps.
package features_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// TestFeatures runs every Gherkin feature under features/.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

var (
	bddDock   = time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	bddCutoff = time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)
	bddDepart = time.Date(2026, 11, 6, 16, 1, 30, 0, time.UTC)
)

// world is the per-scenario state.
type world struct {
	load            *outboundload.OutboundLoad
	packages        map[string]*shippingpackage.ShippingPackage
	bol             *billoflading.BillOfLading
	confirmedOrders []string
	bolSeal         string
	// published counts every domain event by name, standing in for the
	// outbox the wave-5 relay drains.
	published map[string]int
	missedFor map[string]int
}

func newWorld() *world {
	return &world{
		packages:  map[string]*shippingpackage.ShippingPackage{},
		published: map[string]int{},
		missedFor: map[string]int{},
	}
}

// drain pulls the aggregates' events into the published tally, remembering
// the ShipmentConfirmed orders and the issued seal along the way.
func (w *world) drain() {
	for _, ev := range w.load.PullEvents() {
		w.published[ev.EventName()]++
		if c, ok := ev.(outboundload.ShipmentConfirmed); ok {
			w.confirmedOrders = c.Orders
		}
	}
	for _, p := range w.packages {
		for _, ev := range p.PullEvents() {
			w.published[ev.EventName()]++
			if ev.EventName() == "PackageMissedLoad" {
				w.missedFor[string(ev.PackageID())]++
			}
		}
	}
	if w.bol != nil {
		for range w.bol.PullEvents() {
			w.published["BillOfLadingIssued"]++
		}
	}
}

func (w *world) planLoad() error {
	l, evs, err := outboundload.Plan(outboundload.PlanInput{
		LoadID: "load-7f3a", Site: "SITE-1", Cpt: "cpt-1600",
		OperatingDate: "2026-11-06", ShipMethod: "STDOUT-2DAY",
		Cutoff: bddCutoff, At: bddDock,
	})
	if err != nil {
		return err
	}
	w.load = l
	for _, ev := range evs {
		w.published[ev.EventName()]++
	}
	return nil
}

func (w *world) manifest(pkg, order string) error {
	p, err := shippingpackage.Expect(shippingpackage.ExpectInput{
		PackageID: shared.PackageID(pkg), Site: "SITE-1",
		SourceOrderID: order, WorkUnitRef: order + "-line-1", At: bddDock,
	})
	if err != nil {
		return err
	}
	if err := p.AssignToLoad(w.load.ID()); err != nil {
		return err
	}
	w.packages[pkg] = p
	return nil
}

// onDeparture is the ConfirmOnDeparture / FlagDepartureWithoutClose policy
// (ADR 0002): a departure for a CLOSED load confirms the shipment, marks
// the loaded packages SHIPPED and issues the BOL with the departure's seal;
// a departure for a LOADING load never confirms and flags what is not
// loaded, once.
func (w *world) onDeparture(d outboundload.Departure) error {
	w.drain()
	if w.load.State() == outboundload.StateShipConfirmed {
		// A re-delivered departure for an already-confirmed load is a
		// no-op: the consumer dedupes on the CloudEvents id and the
		// SHIP_CONFIRMED state is the domain backstop (ADR 0002).
		return nil
	}
	if w.load.State() == outboundload.StateLoading {
		return w.flagNotLoaded(bddCutoff)
	}
	if err := w.markShippedAndConfirm(d); err != nil {
		return err
	}
	return w.issueBol(d)
}

func (w *world) markShippedAndConfirm(d outboundload.Departure) error {
	orders := make([]string, 0, len(w.packages))
	for id, p := range w.packages {
		if err := p.MarkShipped(bddDepart); err != nil {
			return fmt.Errorf("package %s: %w", id, err)
		}
		if p.SourceOrderID() != "" {
			orders = append(orders, p.SourceOrderID())
		}
	}
	return w.load.ConfirmShipment(outboundload.ConfirmInput{
		Departure: d, Carrier: "STDOUT", Orders: orders, At: bddDepart,
	})
}

func (w *world) flagNotLoaded(cutoff time.Time) error {
	for id, p := range w.packages {
		if p.State() == shippingpackage.StateLoaded {
			continue
		}
		if err := p.FlagMissedLoad(cutoff, bddDepart); err != nil {
			return fmt.Errorf("package %s: %w", id, err)
		}
	}
	return nil
}

func (w *world) issueBol(d outboundload.Departure) error {
	count := 0
	for _, p := range w.packages {
		if p.State() == shippingpackage.StateShipped {
			count++
		}
	}
	b, evs, err := billoflading.Issue(billoflading.IssueInput{
		BolNumber: "BOL-2026-11-06-0042", LoadID: w.load.ID(),
		Site: "SITE-1", LoadConfirmed: true, Carrier: "STDOUT",
		CarrierScac: "STDOUT", TrailerID: d.TrailerID, SealNumber: d.SealNumber,
		PackageCount: count, At: bddDepart,
	})
	if err != nil {
		return err
	}
	for _, ev := range evs {
		w.published[ev.EventName()]++
	}
	w.bol = b
	w.bolSeal = d.SealNumber
	return nil
}

// InitializeScenario binds the Gherkin steps to the domain. godog matches
// step text against every registered regex regardless of keyword, so each
// step is registered exactly once.
func InitializeScenario(ctx *godog.ScenarioContext) {
	w := newWorld()
	registerLoadSteps(ctx, w)
	registerPackageSteps(ctx, w)
	registerDepartureSteps(ctx, w)
	registerAssertionSteps(ctx, w)
}

func registerLoadSteps(ctx *godog.ScenarioContext, w *world) {
	ctx.Step(`^a planned load "([^"]*)" for the "16:00" cutoff of site "([^"]*)"$`,
		func(loadID, site string) error {
			if loadID != "load-7f3a" || site != "SITE-1" {
				return fmt.Errorf("fixture load/site mismatch: %s %s", loadID, site)
			}
			return w.planLoad()
		})

	ctx.Step(`^the door "([^"]*)" is assigned from appointment "([^"]*)"$`,
		func(door, appt string) error {
			return w.load.AssignDoor(outboundload.AssignDoorInput{
				DoorCode: shared.DoorCode(door), AppointmentID: shared.AppointmentID(appt), At: bddDock,
			})
		})

	ctx.Step(`^trailer "([^"]*)" docks at door "([^"]*)"$`,
		func(trailer, door string) error {
			return w.load.AttachTrailer(outboundload.TrailerDocked{
				TrailerID: shared.TrailerID(trailer), DoorCode: shared.DoorCode(door), At: bddDock,
			})
		})

	ctx.Step(`^loading has started$`, func() error {
		return w.load.StartLoading(bddDock)
	})

	ctx.Step(`^the load closes with (\d+) package loaded$`, func(loaded int) error {
		return w.closeLoad(loaded)
	})

	ctx.Step(`^the load is "([^"]*)"$`, func(state string) error {
		if got := string(w.load.State()); got != state {
			return fmt.Errorf("load state = %s, want %s", got, state)
		}
		return nil
	})
}

func registerPackageSteps(ctx *godog.ScenarioContext, w *world) {
	ctx.Step(`^package "([^"]*)" of order "([^"]*)" is manifested on the load$`,
		func(pkg, order string) error { return w.manifest(pkg, order) })

	ctx.Step(`^package "([^"]*)" stages at "([^"]*)"$`, func(pkg, loc string) error {
		return w.packages[pkg].Stage(shippingpackage.StageInput{
			Location: loc, IsStaging: true, LocationSite: "SITE-1", At: bddDock,
		})
	})

	ctx.Step(`^package "([^"]*)" loads through door "([^"]*)"$`, func(pkg, door string) error {
		return w.packages[pkg].Load(shippingpackage.LoadInput{
			LoadID: w.load.ID(), DoorCode: shared.DoorCode(door),
			LoadDoor: w.load.Door(), At: bddDock,
		})
	})

	ctx.Step(`^package "([^"]*)" is diverted before the cutoff$`, func(pkg string) error {
		return w.packages[pkg].MarkShortShipped("DIVERTED", bddCutoff)
	})

	ctx.Step(`^package "([^"]*)" is "([^"]*)"$`, func(pkg, state string) error {
		if got := string(w.packages[pkg].State()); got != state {
			return fmt.Errorf("package %s state = %s, want %s", pkg, got, state)
		}
		return nil
	})
}

func registerDepartureSteps(ctx *godog.ScenarioContext, w *world) {
	ctx.Step(`^trailer "([^"]*)" departs with seal "([^"]*)"$`, func(trailer, seal string) error {
		return w.onDeparture(outboundload.Departure{
			TrailerID: shared.TrailerID(trailer), SealNumber: seal, DepartedAt: bddDepart,
		})
	})

	// A re-delivered TrailerDeparted runs the same policy; the
	// SHIP_CONFIRMED state is the idempotency backstop, so the second
	// departure must refuse inside the domain and publish nothing new.
	ctx.Step(`^trailer "([^"]*)" departs with seal "([^"]*)" again$`,
		func(trailer, seal string) error {
			return w.onDeparture(outboundload.Departure{
				TrailerID: shared.TrailerID(trailer), SealNumber: seal, DepartedAt: bddDepart,
			})
		})

	ctx.Step(`^the cutoff sweep runs at the cutoff$`, func() error {
		return w.flagNotLoaded(bddCutoff)
	})

	ctx.Step(`^no bill of lading exists$`, func() error {
		if w.bol != nil {
			return fmt.Errorf("a bill of lading was issued for a load that never confirmed")
		}
		return nil
	})
}

func registerAssertionSteps(ctx *godog.ScenarioContext, w *world) {
	ctx.Step(`^exactly (\d+) "([^"]*)" event was published, listing order "([^"]*)"$`,
		func(n int, event, order string) error {
			w.drain()
			if got := w.published[event]; got != n {
				return fmt.Errorf("%s = %d, want %d", event, got, n)
			}
			if len(w.confirmedOrders) != 1 || w.confirmedOrders[0] != order {
				return fmt.Errorf("confirmed orders = %v, want [%s]", w.confirmedOrders, order)
			}
			return nil
		})

	ctx.Step(`^exactly (\d+) "([^"]*)" event was published with seal "([^"]*)"$`,
		func(n int, event, seal string) error {
			w.drain()
			if got := w.published[event]; got != n {
				return fmt.Errorf("%s = %d, want %d", event, got, n)
			}
			if w.bolSeal != seal {
				return fmt.Errorf("issued seal = %q, want %q", w.bolSeal, seal)
			}
			return nil
		})

	ctx.Step(`^exactly (\d+) "([^"]*)" event was published for package "([^"]*)"$`,
		func(n int, event, pkg string) error {
			w.drain()
			if got := w.missedFor[pkg]; got != n {
				return fmt.Errorf("%s for %s = %d, want %d", event, pkg, got, n)
			}
			return nil
		})

	ctx.Step(`^exactly (\d+) "([^"]*)" events were published$`, func(n int, event string) error {
		w.drain()
		if got := w.published[event]; got != n {
			return fmt.Errorf("%s = %d, want %d", event, got, n)
		}
		return nil
	})
}

func (w *world) closeLoad(loaded int) error {
	short := make([]shared.ShortShip, 0)
	for id, p := range w.packages {
		if p.State() == shippingpackage.StateShortShipped {
			short = append(short, shared.ShortShip{PackageID: shared.PackageID(id), Reason: "DIVERTED"})
		}
	}
	w.drain()
	return w.load.CloseLoad(outboundload.CloseInput{
		ShortShips: short, LoadedCount: loaded, At: bddCutoff,
	})
}
