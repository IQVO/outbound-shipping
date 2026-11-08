package usecases

import (
	"context"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// StagePackage is the dock operator's staging scan
// (POST /packages/{packageId}/stagings): EXPECTED -> STAGED at a
// facility-layout Staging location of the package's site, PackageStaged
// published. The staging rule is judged from the StagingLocationCopy.
type StagePackage struct {
	UoW    ports.UnitOfWork
	Pkgs   ports.PackageRepo
	Layout ports.LayoutCopy
	Sink   ports.EventSink
	Clock  ports.Clock
}

// Handle stages the package.
func (uc *StagePackage) Handle(ctx context.Context, packageID shared.PackageID, location string) (*shippingpackage.ShippingPackage, error) {
	var pkg *shippingpackage.ShippingPackage
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		p, err := uc.Pkgs.Get(ctx, packageID)
		if err != nil {
			return err
		}
		place, ok, err := uc.Layout.Place(ctx, location)
		if err != nil {
			return err
		}
		loaded := p.Version() // a no-op re-stage bumps nothing
		if err := p.Stage(shippingpackage.StageInput{
			Location: location, IsStaging: ok && place.IsStaging(),
			LocationSite: place.Site, At: uc.Clock.Now(),
		}); err != nil {
			return err
		}
		if err := uc.Pkgs.Save(ctx, p, loaded); err != nil {
			return err
		}
		if err := uc.Sink.PublishPackageEvents(ctx, p.PullEvents()...); err != nil {
			return err
		}
		pkg = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

// LoadPackage is the loading scan (POST /packages/{packageId}/loadings):
// EXPECTED or STAGED -> LOADED through the load's own door, PackageLoaded
// published. The door facts come from the load the package is assigned to
// (ErrWrongLoad otherwise).
type LoadPackage struct {
	UoW   ports.UnitOfWork
	Pkgs  ports.PackageRepo
	Loads ports.LoadRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Handle loads the package through doorCode onto loadID.
func (uc *LoadPackage) Handle(ctx context.Context, packageID shared.PackageID, loadID shared.LoadID, doorCode shared.DoorCode) (*shippingpackage.ShippingPackage, error) {
	var pkg *shippingpackage.ShippingPackage
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		p, err := uc.Pkgs.Get(ctx, packageID)
		if err != nil {
			return err
		}
		if p.LoadID() == "" {
			// Assigned to no load: the domain's own ErrWrongLoad.
			return shippingpackage.ErrWrongLoad
		}
		load, err := uc.Loads.Get(ctx, p.LoadID())
		if err != nil {
			return err
		}
		if err := p.Load(shippingpackage.LoadInput{
			LoadID: loadID, DoorCode: doorCode, LoadDoor: load.Door(),
			At: uc.Clock.Now(),
		}); err != nil {
			return err
		}
		if err := uc.Pkgs.Save(ctx, p, p.Version()-1); err != nil {
			return err
		}
		if err := uc.Sink.PublishPackageEvents(ctx, p.PullEvents()...); err != nil {
			return err
		}
		pkg = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

// UnloadPackage is the unload scan (POST /packages/{packageId}/unloadings):
// LOADED -> STAGED with the operator's reason, PackageUnloaded published.
// After ship-confirm it refuses ErrAlreadyShipped — the truck has left.
type UnloadPackage struct {
	UoW   ports.UnitOfWork
	Pkgs  ports.PackageRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Handle unloads the package.
func (uc *UnloadPackage) Handle(ctx context.Context, packageID shared.PackageID, reason string) (*shippingpackage.ShippingPackage, error) {
	var pkg *shippingpackage.ShippingPackage
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		p, err := uc.Pkgs.Get(ctx, packageID)
		if err != nil {
			return err
		}
		if err := p.Unload(reason, uc.Clock.Now()); err != nil {
			return err
		}
		if err := uc.Pkgs.Save(ctx, p, p.Version()-1); err != nil {
			return err
		}
		if err := uc.Sink.PublishPackageEvents(ctx, p.PullEvents()...); err != nil {
			return err
		}
		pkg = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pkg, nil
}
