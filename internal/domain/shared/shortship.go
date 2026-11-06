package shared

import "errors"

// ErrShortShipReasonRequired is returned when a short-ship entry carries no
// reason: a package is never silently left off a load.
var ErrShortShipReasonRequired = errors.New("shared: a short-ship needs a reason")

// ShortShip records one package deliberately left off a load, with the
// reason. It is the entry the OutboundLoad aggregate keeps in its ShortShip[]
// field and publishes as part of the close's short_shipped_count.
type ShortShip struct {
	PackageID PackageID
	Reason    string
}

// Valid reports whether the entry names a package and a reason.
func (s ShortShip) Valid() error {
	if s.PackageID == "" {
		return ErrShortShipPackageRequired
	}
	if s.Reason == "" {
		return ErrShortShipReasonRequired
	}
	return nil
}

// ErrShortShipPackageRequired is returned when a short-ship entry names no
// package.
var ErrShortShipPackageRequired = errors.New("shared: a short-ship names a package")
