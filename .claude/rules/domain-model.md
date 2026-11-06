<!-- outbound-shipping: filled from the canonical model (campaign model.yaml). -->
# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **Outbound Load** — Packages planned to leave together for one cutoff and
  ship method. One open load per (site, cpt, operating date, ship method).
- **Staging** — Holding a manifested package at a Staging location near the
  door before loading.
- **Ship-Confirm** — The fact that a closed load left the site on a trailer.
  Follows the physical departure (`TrailerDeparted`), never precedes it.
- **Short-Ship** — A package deliberately left off a load, with a reason.
- **Missed Load** — A package that was not loaded by its cutoff.
- **Bill of Lading** — The shipping document for one confirmed load.

## Aggregates

- **OutboundLoad** (`internal/domain/outboundload`): root `loadId`. States
  PLANNED -> LOADING -> CLOSED -> SHIP_CONFIRMED (or CANCELLED from PLANNED;
  ReopenLoad CLOSED -> LOADING). Enforces `ErrDuplicateOpenLoad` (with the
  use case + partial unique index), `ErrTrailerNotDocked` (door + docked
  trailer before loading), `ErrPackagesOutstanding` (close only when every
  package is loaded or short-shipped with a reason), `ErrLoadNotClosed`
  (confirm needs CLOSED), `ErrLoadConfirmed` (a SHIP_CONFIRMED load never
  changes).
- **ShippingPackage** (`internal/domain/shippingpackage`): root `packageId`.
  States EXPECTED -> STAGED -> LOADED -> SHIPPED, or SHORT_SHIPPED from
  EXPECTED/STAGED; UnloadPackage LOADED -> STAGED. Enforces
  `ErrNotStagingLocation` (facility-layout Staging location of its site),
  `ErrWrongLoad` (only its own load, through that load's door),
  `ErrPackageOnAnotherLoad` (one load at a time), `ErrAlreadyShipped`
  (unload only before ship-confirm).
- **BillOfLading** (`internal/domain/billoflading`): root `bolNumber`. ISSUED
  -> VOIDED. Enforces `ErrBolAlreadyIssued` (only for a SHIP_CONFIRMED load,
  exactly once per load) and `ErrSealMissing` (the seal yard-management
  recorded on departure).

## Domain events

- `LoadPlanned`, `LoadDoorAssigned`, `LoadingStarted`, `LoadClosed`,
  `LoadReopened`, `ShipmentConfirmed`, `LoadCancelled` — the OutboundLoad
  lifecycle; `ShipmentConfirmed` carries the orders that shipped.
- `PackageStaged`, `PackageLoaded`, `PackageUnloaded`, `PackageMissedLoad` —
  the package's way to the truck; `PackageMissedLoad` is raised once per
  package per cutoff (sweep) or on departure-without-close.
- `BillOfLadingIssued`, `BillOfLadingVoided` — the shipping document.

## Key use cases (`internal/application/usecases`)

Wave 5 builds them from the model's policies: PlanLoadFromCutoff,
AssignPackageToLoad, DoorFromAppointment, StartOnDock, ConfirmOnDeparture,
FlagDepartureWithoutClose, SweepMissedLoad, DropDiverted.
