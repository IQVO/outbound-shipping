package usecases

import (
	"context"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// ApplyLayout feeds the StagingLocationCopy (model.yaml read model):
// ZoneRegistered saves the zone-to-site mapping, LocationSlotRegistered the
// location and its role. The two may arrive in any order; a location's site
// is resolved through its zoneId at read time (the site_key convention).
type ApplyLayout struct {
	Intake
	Copies ports.LayoutCopy
}

// ZoneChange is one ZoneRegistered.
type ZoneChange struct {
	ZoneID   string
	SiteCode string
	ZoneCode string
}

// Handle applies one zone registration.
func (uc *ApplyLayout) HandleZone(ctx context.Context, eventID string, z ZoneChange) (Outcome, error) {
	return uc.apply(ctx, LayoutConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if z.ZoneID == "" || z.SiteCode == "" {
			return "", invalidf("zone registration needs zoneId and siteCode")
		}
		if err := uc.Copies.SaveZone(ctx, repository.Zone{
			ZoneID: z.ZoneID, SiteCode: shared.SiteID(z.SiteCode), ZoneCode: z.ZoneCode,
		}); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}

// LocationChange is one LocationSlotRegistered. Role "" means a pre-role
// event: Storage, never Staging.
type LocationChange struct {
	Code   string
	ZoneID string
	Role   string
}

// HandleLocation applies one location registration.
func (uc *ApplyLayout) HandleLocation(ctx context.Context, eventID string, l LocationChange) (Outcome, error) {
	return uc.apply(ctx, LayoutConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if l.Code == "" || l.ZoneID == "" {
			return "", invalidf("location registration needs locationCode and zoneId")
		}
		if err := uc.Copies.SaveLocation(ctx, repository.Location{
			Code: l.Code, ZoneID: l.ZoneID, Role: l.Role,
		}); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}
