package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// DivertPackage is the DropDiverted policy (model.yaml): on
// PackageDiverted, a package already EXPECTED (or STAGED) becomes
// SHORT_SHIPPED with reason DIVERTED, so its load can still close. A package
// that was never manifested, or is already LOADED/SHIPPED/SHORT_SHIPPED, is
// ignored — the diversion fact has nothing to drop.
type DivertPackage struct {
	Intake
	Packages ports.PackageRepo
	Clock    ports.Clock
}

// Handle applies one diversion.
func (uc *DivertPackage) Handle(ctx context.Context, eventID string, packageID shared.PackageID, divertedAt time.Time) (Outcome, error) {
	return uc.apply(ctx, FulfillmentConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if packageID == "" {
			return "", invalidf("diversion needs a package id")
		}
		pkg, err := uc.Packages.Get(ctx, packageID)
		if err != nil {
			if errors.Is(err, repository.ErrPackageNotFound) {
				return OutcomeIgnored, nil
			}
			return "", err
		}
		if err := pkg.MarkShortShipped("DIVERTED", atOr(divertedAt, uc.Clock.Now())); err != nil {
			// LOADED, SHIPPED or already SHORT_SHIPPED: nothing to drop.
			return OutcomeIgnored, nil
		}
		if err := uc.Packages.Save(ctx, pkg, pkg.Version()-1); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}

// atOr returns at, or now when at is zero.
func atOr(at, now time.Time) time.Time {
	if at.IsZero() {
		return now
	}
	return at
}
