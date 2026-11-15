package http

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// TestProblemForMapsTheBadShortShipAndTheCancelRefusals pins the catalogue
// rows of the BDD-wave rulings (ADR 0010): a short-ship entry without a
// package or a reason is a 400 bad-request (never the internal-error
// fallback), and the cancel refusals keep their own slugs.
func TestProblemForMapsTheBadShortShipAndTheCancelRefusals(t *testing.T) {
	cases := []struct {
		err    error
		status int
		slug   string
	}{
		{shared.ErrShortShipReasonRequired, http.StatusBadRequest, "bad-request"},
		{shared.ErrShortShipPackageRequired, http.StatusBadRequest, "bad-request"},
		{shippingpackage.ErrShortShipReasonRequired, http.StatusBadRequest, "bad-request"},
		{outboundload.ErrInvalidShortShip, http.StatusBadRequest, "bad-request"},
		{outboundload.ErrLoadConfirmed, http.StatusConflict, "load-confirmed"},
		{outboundload.ErrInvalidTransition, http.StatusConflict, "load-state"},
		{usecases.ErrLoadHasPackages, http.StatusConflict, "load-has-packages"},
		{errors.New("something else"), http.StatusInternalServerError, "internal-error"},
	}
	for _, c := range cases {
		t.Run(c.slug+"/"+c.err.Error(), func(t *testing.T) {
			// A wrapped error matches the same row.
			p := problemFor(fmt.Errorf("wrapped: %w", c.err))
			if p.status != c.status || p.slug != c.slug {
				t.Fatalf("problemFor(%v) = %d %s, want %d %s", c.err, p.status, p.slug, c.status, c.slug)
			}
		})
	}
}
