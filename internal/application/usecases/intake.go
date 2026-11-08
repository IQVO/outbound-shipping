package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
)

// Intake is the claim-and-apply skeleton every consumer use case shares: the
// claim of the CloudEvents id and the effect run in ONE unit of work, so a
// rollback un-claims the id and a redelivery is processed, not skipped.
type Intake struct {
	UoW       ports.UnitOfWork
	Processed ports.ProcessedEvents
}

func (in Intake) apply(ctx context.Context, consumer, eventID string, effect func(ctx context.Context) (Outcome, error)) (Outcome, error) {
	if eventID == "" {
		return "", invalid(errors.New("event id is required"))
	}
	var outcome Outcome
	err := in.UoW.Do(ctx, func(ctx context.Context) error {
		claimed, err := in.Processed.Claim(ctx, consumer, eventID)
		if err != nil {
			return fmt.Errorf("claim processed event: %w", err)
		}
		if !claimed {
			outcome = OutcomeDuplicate
			return nil
		}
		out, err := effect(ctx)
		outcome = out
		return err
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}

// invalid wraps a cause as an ErrInvalidEvent chain (a deterministic payload
// problem: the consumer dead-letters it, never retries).
func invalid(err error) error { return fmt.Errorf("%w: %w", ErrInvalidEvent, err) }

// invalidf is invalid with formatting.
func invalidf(format string, args ...any) error { return invalid(fmt.Errorf(format, args...)) }
