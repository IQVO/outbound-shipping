// Package kafka holds this service's INBOUND Kafka consumers (wave 5): the
// fulfillment consumer (fulfillment-execution's PackageManifested and
// PackageDiverted), the cpt consumer (process-path-management's
// CPTScheduleChanged), the layout consumer (facility-layout's ZoneRegistered
// and LocationSlotRegistered), the dock consumer (dock-scheduling's
// DockAppointmentBooked) and the yard consumer (yard-management's
// TrailerDocked, DoorWorkCompleted and TrailerDeparted, fixture-validated
// against the proposed yard model until its contract merges). The analytics
// consumer is this service's own projector over the analytics topic. Every
// consumer decodes CloudEvents 1.0 only, via
// internal/adapters/kafka/cloudevents, and dispatches on the FULL `type`
// string. The consumers only decode: every effect is a use case of
// internal/application/usecases.
//
// Delivery guarantee: AT-LEAST-ONCE with an ATOMIC effect. Each message is
// handled inside ONE ports.UnitOfWork (processed-event claim + effect commit
// or roll back together, see usecases.Intake) and its offset is committed only
// AFTER handling returned nil (FetchMessage + CommitMessages). Handling
// returns a non-nil error ONLY for transient failures; the run loop then
// retries the SAME message with capped exponential backoff, and after a
// bounded number of attempts dead-letters it to "<topic>.dlq" so a stuck
// message cannot wedge the partition forever. Deterministic problems (not a
// CloudEvent, unknown type) are logged at WARN and committed past; a payload
// of a handled type that can never be applied is dead-lettered at once.
package kafka
