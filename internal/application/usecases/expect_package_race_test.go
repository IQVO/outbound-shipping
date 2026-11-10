package usecases

import (
	"context"
	"testing"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/memory"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// racingLoads is the in-memory LoadRepo with a rival manifestation of the
// same cutoff key committing its load at one chosen point of the FIRST
// attempt, deterministically forcing an interleaving the Postgres
// concurrency test only hits by chance:
//
//   - rivalBeforeOpenKeys: after FindOpenByKey missed but before
//     OpenLoadKeys (two READ COMMITTED statements, two snapshots), so the
//     domain's Plan refuses with outboundload.ErrDuplicateOpenLoad;
//   - rivalBeforeInsert: after both reads missed but before the INSERT, so
//     the partial unique index refuses with repository.ErrDuplicateOpenLoad.
type racingLoads struct {
	*memory.LoadRepo
	at     racePoint
	key    outboundload.LoadKey
	rival  shared.LoadID
	fired  bool
	planFn func(key outboundload.LoadKey) *outboundload.OutboundLoad
}

type racePoint int

const (
	rivalBeforeOpenKeys racePoint = iota
	rivalBeforeInsert
)

func (r *racingLoads) FindOpenByKey(ctx context.Context, key outboundload.LoadKey) (*outboundload.OutboundLoad, error) {
	r.key = key
	return r.LoadRepo.FindOpenByKey(ctx, key)
}

func (r *racingLoads) OpenLoadKeys(ctx context.Context, site shared.SiteID) ([]outboundload.LoadKey, error) {
	if r.at == rivalBeforeOpenKeys {
		r.commitRival(ctx)
	}
	return r.LoadRepo.OpenLoadKeys(ctx, site)
}

func (r *racingLoads) Save(ctx context.Context, l *outboundload.OutboundLoad, loadedVersion int64) error {
	if r.at == rivalBeforeInsert && loadedVersion == 0 {
		r.commitRival(ctx)
	}
	return r.LoadRepo.Save(ctx, l, loadedVersion)
}

// commitRival inserts the rival's load once, as another transaction's
// commit would.
func (r *racingLoads) commitRival(ctx context.Context) {
	if r.fired {
		return
	}
	r.fired = true
	l := r.planFn(r.key)
	r.rival = l.ID()
	if err := r.LoadRepo.Save(ctx, l, 0); err != nil {
		panic(err)
	}
}

// TestExpectPackageJoinsTheRivalLoadWhenItLosesThePlanRace pins the
// one-open-load race contract for both places the loser can learn it lost:
// the manifestation is APPLIED (never an error the consumer would retry or
// dead-letter), the package lands on the rival's load, and this call plans
// nothing (no LoadPlanned of its own).
func TestExpectPackageJoinsTheRivalLoadWhenItLosesThePlanRace(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   racePoint
	}{
		{"rival commits between the two reads (domain refusal)", rivalBeforeOpenKeys},
		{"rival commits before the insert (unique index)", rivalBeforeInsert},
	} {
		t.Run(tc.name, func(t *testing.T) { assertJoinsRivalLoad(t, tc.at) })
	}
}

func assertJoinsRivalLoad(t *testing.T, at racePoint) {
	t.Helper()
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)
	loads := &racingLoads{LoadRepo: r.loads, at: at, planFn: func(key outboundload.LoadKey) *outboundload.OutboundLoad {
		l, _, err := outboundload.Plan(outboundload.PlanInput{
			LoadID: "LOAD-RIVAL", Site: key.Site, Cpt: key.Cpt,
			OperatingDate: key.OperatingDate, ShipMethod: key.ShipMethod,
			Cutoff: tCutoff, At: tDock,
		})
		if err != nil {
			t.Fatalf("plan rival: %v", err)
		}
		return l
	}}
	r.expect.Loads = loads

	if out := r.manifest(ctx, t, "ev-man-race", "pkg-race", "order-race"); out != OutcomeApplied {
		t.Fatalf("out=%v want applied", out)
	}
	if !loads.fired {
		t.Fatal("the rival never committed: the race was not forced")
	}
	p, err := r.pkgs.Get(ctx, "pkg-race")
	if err != nil {
		t.Fatal(err)
	}
	if p.LoadID() != loads.rival {
		t.Fatalf("package on %q, want the rival's load %q", p.LoadID(), loads.rival)
	}
	if keys, err := r.loads.OpenLoadKeys(ctx, "SITE-1"); err != nil || len(keys) != 1 {
		t.Fatalf("open load keys = %v %v, want exactly one", keys, err)
	}
	if got := r.published("LoadPlanned"); got != 0 {
		t.Fatalf("LoadPlanned x%d, want 0 (the rival planned it)", got)
	}
}
