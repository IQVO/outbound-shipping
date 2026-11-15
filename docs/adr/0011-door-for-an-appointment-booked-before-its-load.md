# ADR 0011: A door for an appointment booked before its load exists

## Status

Proposed (2026-11-15). Nothing here is implemented; today's behaviour is unchanged and pinned by the
`consumer_policy_rules.feature` scenario "An appointment that arrives before the load leaves the load without a door until another appointment arrives".

## Context

`BookAppointment` assigns a door only when `DockAppointmentBooked` arrives while an open load of the same
`(site, cpt)` exists. If the appointment arrives first, the copy is saved and nothing else happens; the planning
path (`ExpectPackage`, on the first `PackageManifested` of a cutoff) never reads the appointment copy, so the
load has no door until another appointment event for that cpt arrives. The operator workaround is to have
dock-scheduling publish the booking again once the load exists
([Troubleshooting](/docs/operations/troubleshooting#appointment-before-the-load-planned)).

The records do not state the rule for the reverse order: ADR 0005 says `LoadDoorAssigned` is raised when "an
outbound dock appointment names the load's cpt", ADR 0007 pins the contract, and the AsyncAPI says the door is
assigned "if one exists when the event arrives". Topic ordering across `warehouse.dock-scheduling.events` and
`warehouse.fulfillment.events` is not guaranteed ([Integration](/docs/ecosystem/integration)), so the order
appointment-first is reachable in production, not only in tests.

## Decision

Proposed, to be confirmed by the context owner. Nothing changes until this ADR is Accepted.

Assign the door when the load is planned: after `ExpectPackage` plans a load, look the appointment copies up by
`(site, cpt)` and, if one qualifies, assign it through the same `AssignDoor` command and raise the same
`LoadDoorAssigned`. Open points the owner must settle first:

1. **Which appointment** when several copies name one cpt. The load is keyed by `(site, cpt, operating date, ship
   method)`; the copy carries no operating date and can be read only by appointment id. Candidate rule: the
   appointment whose window starts on the load's operating date, then the highest version, then the latest
   booking. Needs a lookup by `(site, cpt)` on the copy store.
2. **Cancel and reschedule.** Only `DockAppointmentBooked` is consumed; a cancelled or moved appointment would
   leave a stale copy and a door assigned from it. Either consume the cancel and reschedule events (an ADR 0007
   change) or accept the staleness already present in the appointment-after-load path.
3. **Trigger.** On planning only, on every manifest that joins the load, or on a sweep. They differ in which
   `LoadDoorAssigned` events are raised and when (one on planning versus possibly a re-assignment later).

## Consequences

- If accepted: `ExpectPackage` gains a door lookup, the appointment copy store a read by `(site, cpt)`, and the
  "appointment before the load" scenario, troubleshooting.md, use-cases.md and domain-message-flow.md change
  from known-gap to rule.
- If rejected: the workaround stays and the troubleshooting page says so as a decision instead of a gap.
- No code, spec or contract changes in this record.
