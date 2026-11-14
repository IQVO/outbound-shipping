package http

import (
	"context"
)

// countLoadClose records one close attempt on the Tier 2 counter: a closed
// load on success, otherwise a failure whose reason is the RFC 7807 problem
// slug the client is about to receive. It sits in the handler, not the use
// case, because the slug is this adapter's vocabulary (problemFor) and an
// Idempotency-Key replay is answered by the middleware without reaching the
// handler, so a replay is never counted twice. A nil Metrics is a no-op.
func (s *Server) countLoadClose(ctx context.Context, err error) {
	if s.Metrics == nil {
		return
	}
	if err == nil {
		s.Metrics.LoadClosed(ctx)
		return
	}
	s.Metrics.LoadCloseFailed(ctx, problemFor(err).slug)
}
