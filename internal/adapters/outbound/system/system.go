// Package system holds the production clock and id generator: the two
// outbound adapters that need no infrastructure. They live apart from
// internal/adapters/outbound/memory so the shipped binary (cmd/**) never
// imports the in-memory test doubles (TestRuntimeBinaryHasNoInMemoryAdapters).
package system

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// Compile-time proof that both adapters implement their port.
var (
	_ ports.Clock   = Clock{}
	_ ports.LoadIDs = RandomIDs{}
)

// Clock is the wall clock: the production ports.Clock.
type Clock struct{}

// Now returns the current time in UTC.
func (Clock) Now() time.Time { return time.Now().UTC() }

// RandomIDs is the production ports.LoadIDs: "load-<uuid>" for loads and
// "BOL-<date>-<uuid6>" for bills of lading.
type RandomIDs struct{}

// NewLoadID implements ports.LoadIDs.
func (RandomIDs) NewLoadID() shared.LoadID {
	return shared.LoadID("load-" + uuid())
}

// NewBolNumber implements ports.LoadIDs.
func (RandomIDs) NewBolNumber(date outboundload.OperatingDate) shared.BolNumber {
	return shared.BolNumber(fmt.Sprintf("BOL-%s-%s", date, uuid()[:6]))
}

// uuid mints a random RFC 4122 v4 UUID string.
func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("read random bytes: %w", err))
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
