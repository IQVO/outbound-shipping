package shared

import (
	"errors"
	"testing"
)

func TestShortShipValid(t *testing.T) {
	valid := ShortShip{PackageID: "pkg-2077", Reason: "DIVERTED"}
	if err := valid.Valid(); err != nil {
		t.Fatalf("valid entry refused: %v", err)
	}
	noPkg := ShortShip{PackageID: "", Reason: "DAMAGED"}
	if err := noPkg.Valid(); !errors.Is(err, ErrShortShipPackageRequired) {
		t.Fatalf("want ErrShortShipPackageRequired, got %v", err)
	}
	noReason := ShortShip{PackageID: "pkg-2077", Reason: ""}
	if err := noReason.Valid(); !errors.Is(err, ErrShortShipReasonRequired) {
		t.Fatalf("want ErrShortShipReasonRequired, got %v", err)
	}
}
