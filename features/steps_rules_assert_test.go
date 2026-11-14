package features_test

// State, event and read-model assertions of the rule features. Every
// assertion reads a repository, the recorded outbox messages or a query use
// case: nothing here drives the domain.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/cucumber/godog"

	inboundkafka "github.com/claudioed/outbound-shipping/internal/adapters/inbound/kafka"
	"github.com/claudioed/outbound-shipping/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

func registerRulesStateAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^load "([^"]*)" is "([^"]*)"$`, func(id, state string) error {
		l, err := w.loadByID(w.ctx, id)
		if err != nil {
			return err
		}
		if got := string(l.State()); got != state {
			return fmt.Errorf("load %s state = %s, want %s", id, got, state)
		}
		return nil
	})

	sc.Step(`^load "([^"]*)" has (?:door "([^"]*)"|no door)$`, func(id, door string) error {
		l, err := w.loadByID(w.ctx, id)
		if err != nil {
			return err
		}
		if got := string(l.Door()); got != door {
			return fmt.Errorf("load %s door = %q, want %q", id, got, door)
		}
		return nil
	})

	sc.Step(`^load "([^"]*)" has trailer "([^"]*)"$`, func(id, trailer string) error {
		l, err := w.loadByID(w.ctx, id)
		if err != nil {
			return err
		}
		if got := string(l.Trailer()); got != trailer {
			return fmt.Errorf("load %s trailer = %q, want %q", id, got, trailer)
		}
		return nil
	})

	sc.Step(`^the open loads of site "([^"]*)" number (\d+)$`, func(site string, n int) error {
		keys, err := w.svc.loads.OpenLoadKeys(w.ctx, shared.SiteID(site))
		if err != nil {
			return err
		}
		if len(keys) != n {
			return fmt.Errorf("open loads = %d (%v), want %d", len(keys), keys, n)
		}
		return nil
	})

	sc.Step(`^(\d+) loads exist for site "([^"]*)"$`, func(n int, site string) error {
		all, err := w.svc.loads.List(w.ctx, repository.LoadFilter{Site: shared.SiteID(site)}, "", 200)
		if err != nil {
			return err
		}
		if len(all) != n {
			return fmt.Errorf("loads = %d, want %d", len(all), n)
		}
		return nil
	})

	registerRulesPackageAssertions(sc, w)
	registerRulesCopyAssertions(sc, w)
}

func registerRulesPackageAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^package "([^"]*)" is at version (\d+)$`, func(pkg string, v int) error {
		p, err := w.svc.pkgs.Get(w.ctx, shared.PackageID(pkg))
		if err != nil {
			return err
		}
		if p.Version() != int64(v) {
			return fmt.Errorf("package %s version = %d, want %d", pkg, p.Version(), v)
		}
		return nil
	})

	sc.Step(`^package "([^"]*)" is staged at "([^"]*)"$`, func(pkg, loc string) error {
		p, err := w.svc.pkgs.Get(w.ctx, shared.PackageID(pkg))
		if err != nil {
			return err
		}
		if p.StagingLocation() != loc {
			return fmt.Errorf("package %s staging location = %q, want %q", pkg, p.StagingLocation(), loc)
		}
		return nil
	})

	sc.Step(`^package "([^"]*)" is on load "([^"]*)"$`, func(pkg, load string) error {
		p, err := w.svc.pkgs.Get(w.ctx, shared.PackageID(pkg))
		if err != nil {
			return err
		}
		if string(p.LoadID()) != load {
			return fmt.Errorf("package %s load = %q, want %q", pkg, p.LoadID(), load)
		}
		return nil
	})

	sc.Step(`^no package "([^"]*)" exists$`, func(pkg string) error {
		if _, err := w.svc.pkgs.Get(w.ctx, shared.PackageID(pkg)); err == nil {
			return fmt.Errorf("package %s exists, want none", pkg)
		}
		return nil
	})

	sc.Step(`^no bill of lading exists for load "([^"]*)"$`, func(load string) error {
		if _, err := w.svc.bols.GetByLoad(w.ctx, shared.LoadID(load)); err == nil {
			return fmt.Errorf("a bill of lading exists for %s, want none", load)
		}
		return nil
	})
}

func registerRulesCopyAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the appointment "([^"]*)" is saved in the local copy$`, func(id string) error {
		_, ok, err := w.svc.appts.Get(w.ctx, shared.AppointmentID(id))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("appointment %s is not in the local copy", id)
		}
		return nil
	})

	sc.Step(`^the door work of trailer "([^"]*)" is recorded$`, func(trailer string) error {
		for _, d := range w.svc.appts.DoorWork() {
			if string(d.TrailerID) == trailer {
				return nil
			}
		}
		return fmt.Errorf("no door work recorded for %s", trailer)
	})
}

// --- published events -------------------------------------------------------

// lastPayload returns the data of the newest integration-topic occurrence.
func (w *world) lastPayload(event string) (map[string]any, error) {
	all := w.dataOf(event)
	if len(all) == 0 {
		return nil, fmt.Errorf("no %s event was published", event)
	}
	return all[len(all)-1], nil
}

func registerRulesEventAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the last "([^"]*)" event has "([^"]*)" "([^"]*)"$`, func(event, field, want string) error {
		data, err := w.lastPayload(event)
		if err != nil {
			return err
		}
		got, ok := lookup(data, field)
		if !ok || render(got) != want {
			return fmt.Errorf("%s.%s = %v (present %v), want %s", event, field, got, ok, want)
		}
		return nil
	})

	sc.Step(`^the last "([^"]*)" event has no "([^"]*)"$`, func(event, field string) error {
		data, err := w.lastPayload(event)
		if err != nil {
			return err
		}
		if got, ok := lookup(data, field); ok {
			return fmt.Errorf("%s.%s = %v, want it omitted", event, field, got)
		}
		return nil
	})

	sc.Step(`^the last "([^"]*)" event lists the orders "([^"]*)"$`, func(event, want string) error {
		data, err := w.lastPayload(event)
		if err != nil {
			return err
		}
		var got []string
		rows, _ := data["orders"].([]any)
		for _, r := range rows {
			if m, ok := r.(map[string]any); ok {
				got = append(got, render(m["order_id"]))
			}
		}
		if strings.Join(got, ", ") != want {
			return fmt.Errorf("orders = %v, want [%s]", got, want)
		}
		return nil
	})

	sc.Step(`^nothing was published$`, func() error {
		if n := len(w.svc.rec.Messages()); n != 0 {
			return fmt.Errorf("%d messages were published, want none", n)
		}
		return nil
	})

	registerRulesEnvelopeAssertions(sc, w)
}

func registerRulesEnvelopeAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^every "([^"]*)" event was also written to the analytics topic under the same id$`, w.assertAnalyticsTwins)
	sc.Step(`^the "([^"]*)" event is typed "([^"]*)" with source "([^"]*)" and keyed by "([^"]*)"$`, w.assertEnvelope)
}

// idsOnTopic collects the CloudEvents ids of one event name on one topic.
func (w *world) idsOnTopic(topic, event string) []string {
	var ids []string
	for _, m := range w.svc.rec.Messages() {
		if m.Topic == topic && typeSuffix(m.EventType) == event {
			ids = append(ids, m.EventID)
		}
	}
	return ids
}

func (w *world) assertAnalyticsTwins(event string) error {
	integration := w.idsOnTopic(events.TopicEvents, event)
	analytics := w.idsOnTopic(events.TopicAnalytics, event)
	sort.Strings(integration)
	sort.Strings(analytics)
	if len(integration) == 0 || strings.Join(integration, ",") != strings.Join(analytics, ",") {
		return fmt.Errorf("%s: integration ids %v, analytics ids %v", event, integration, analytics)
	}
	return nil
}

func (w *world) assertEnvelope(event, typ, source, key string) error {
	for _, m := range w.svc.rec.Messages() {
		if m.Topic != events.TopicEvents || typeSuffix(m.EventType) != event {
			continue
		}
		var env struct{ Type, Source string }
		if err := json.Unmarshal(m.Value, &env); err != nil {
			return err
		}
		if m.EventType != typ || env.Type != typ || env.Source != source || string(m.Key) != key {
			return fmt.Errorf("%s: type %q/%q source %q key %q, want %q %q %q",
				event, m.EventType, env.Type, env.Source, m.Key, typ, source, key)
		}
		return nil
	}
	return fmt.Errorf("no %s event was published", event)
}

// --- read models ------------------------------------------------------------

func registerRulesReadAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the analytics projector folds every published event$`, w.foldAnalytics)
	sc.Step(`^the loads at risk for site "([^"]*)"(?: within (\d+) hours)? number (\d+)$`, w.assertLoadsAtRisk)
	sc.Step(`^the missed-loads read for site "([^"]*)" lists exactly "([^"]*)"$`, w.assertMissedLoads)
}

// foldAnalytics feeds every analytics-topic message through the real
// analytics handlers, the way the projector consumer does.
func (w *world) foldAnalytics() error {
	handlers := inboundkafka.AnalyticsHandlers(inboundkafka.ProjectorUseCases{Project: w.svc.project}, nil)
	for _, m := range w.svc.rec.Messages() {
		if m.Topic != events.TopicAnalytics {
			continue
		}
		e, err := cloudevents.Decode(m.Value)
		if err != nil {
			return err
		}
		if err := foldOne(w, handlers, e); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) assertLoadsAtRisk(site, hours string, want int) error {
	h := 0
	if hours != "" {
		fmt.Sscanf(hours, "%d", &h)
	}
	rows, err := w.svc.queries.LoadsAtRisk(w.ctx, shared.SiteID(site), h, 0)
	if err != nil {
		return err
	}
	if len(rows) != want {
		return fmt.Errorf("loads at risk = %d, want %d", len(rows), want)
	}
	return nil
}

func (w *world) assertMissedLoads(site, want string) error {
	pkgs, err := w.svc.queries.MissedLoads(w.ctx, shared.SiteID(site), 0)
	if err != nil {
		return err
	}
	var ids []string
	for _, p := range pkgs {
		ids = append(ids, string(p.ID()))
	}
	sort.Strings(ids)
	if got := strings.Join(ids, ", "); got != want {
		return fmt.Errorf("missed packages = [%s], want [%s]", got, want)
	}
	return nil
}

func foldOne(w *world, handlers map[string]inboundkafka.HandlerFunc, e ce.Event) error {
	h, ok := handlers[e.Type()]
	if !ok {
		return fmt.Errorf("the projector has no handler for %s", e.Type())
	}
	return h(w.ctx, e)
}
