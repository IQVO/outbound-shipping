package ports

import "context"

// LoadMetrics is the Tier 2 business-metric port of the supervisor's close
// command (fleet standard metrics convention: <context>.<noun>.<verb>, with
// `outcome` and `reason` attributes). The REST adapter calls it once per
// close attempt; the OpenTelemetry adapter in
// internal/adapters/outbound/telemetry implements it, and a nil port means
// "not instrumented". It is NEVER called from internal/domain.
type LoadMetrics interface {
	// LoadClosed records a load that closed.
	LoadClosed(ctx context.Context)
	// LoadCloseFailed records a close that was refused or failed; reason is
	// the stable RFC 7807 problem slug the client received.
	LoadCloseFailed(ctx context.Context, reason string)
}
