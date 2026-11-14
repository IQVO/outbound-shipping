# Derived from the documentation of the context (warehouse-docs
# docs/contexts/outbound-shipping): use-cases.md (StagePackage, LoadPackage,
# UnloadPackage, CloseLoad, ReopenLoad, CancelLoad), aggregate-design-canvas.md
# (ShippingPackage and OutboundLoad invariants and state machines),
# troubleshooting.md (REST refusals) and the openapi in apis/.
# Each scenario pins ONE documented rule and asserts the outcome (state,
# published event, problem slug), driving the real router over the in-memory
# doubles. Load id "load-bdd1" and door "D04" come from the shared harness.
@bdd
Feature: Package and load rules - what the dock operator and the supervisor may do
  A package stages only at a Staging location of its own site, loads only onto
  its own load through that load's door and unloads only before the truck
  left. A load closes only when every package is loaded or short-shipped with
  a reason, reopens only from CLOSED, and a confirmed load never changes.

  Background:
    Given site "SITE-1" runs the "16:00" cutoff shipped "STDOUT-2DAY"
    And dock zone "DOCK-D" and staging lane "STG-D04-A" exist at site "SITE-1"
    And appointment "appt-2211" is booked at door "D04" for the "16:00" cutoff

  # --- StagePackage ------------------------------------------------------

  Scenario: A package scanned into a Staging location of its own site is staged
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When the dock operator scans package "pkg-1029" into "STG-D04-A"
    Then the response status is 200
    And the response field "state" is "STAGED"
    And the response field "staging_location" is "STG-D04-A"
    And the response field "version" is "2"
    And exactly 1 "PackageStaged" event was published for package "pkg-1029"
    And the last "PackageStaged" event has "staging_location" "STG-D04-A"
    And the last "PackageStaged" event has "load_id" "load-bdd1"

  Scenario Outline: A package is never staged anywhere but a Staging location of its own site
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And zone "<zone>" belongs to site "<site>"
    And location "<registered>" is registered in zone "<zone>" with role "<role>"
    When the dock operator scans package "pkg-1029" into "<scanned>"
    Then the problem is "not-staging-location" with status 422
    And package "pkg-1029" is "EXPECTED"
    And exactly 0 "PackageStaged" events were published

    Examples:
      | case                                          | zone   | site   | registered | role    | scanned    |
      | a Storage location                            | zone-3 | SITE-1 | RACK-A1    | Storage | RACK-A1    |
      | a location registered before roles existed    | zone-3 | SITE-1 | RACK-OLD   |         | RACK-OLD   |
      | a Staging location of another site            | zone-9 | SITE-2 | STG-S2-A   | Staging | STG-S2-A   |
      | a location the layout copy never heard of     | zone-3 | SITE-1 | STG-D04-B  | Staging | STG-D04-Z  |

  Scenario: A location registered before its zone stages only once the zone arrives
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And location "STG-D05-A" is registered in zone "zone-5" with role "Staging"
    When the dock operator scans package "pkg-1029" into "STG-D05-A"
    Then the problem is "not-staging-location" with status 422
    When zone "zone-5" belongs to site "SITE-1"
    And the dock operator scans package "pkg-1029" into "STG-D05-A"
    Then the response status is 200
    And package "pkg-1029" is "STAGED"

  Scenario: Scanning a package into the location it is already staged at changes nothing
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And the dock operator scans package "pkg-1029" into "STG-D04-A"
    When the dock operator scans package "pkg-1029" into "STG-D04-A"
    Then the response status is 200
    And the response field "version" is "2"
    And exactly 1 "PackageStaged" event was published for package "pkg-1029"

  Scenario: A staged package is not moved to another staging location by a second scan
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And location "STG-D04-B" is registered in zone "zone-1" with role "Staging"
    And the dock operator scans package "pkg-1029" into "STG-D04-A"
    When the dock operator scans package "pkg-1029" into "STG-D04-B"
    Then the problem is "package-state" with status 409
    And package "pkg-1029" is staged at "STG-D04-A"

  Scenario: A package that is already loaded cannot be staged
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    When the dock operator scans package "pkg-1029" into "STG-D04-A"
    Then the problem is "package-state" with status 409
    And package "pkg-1029" is "LOADED"

  Scenario: Staging a package nobody manifested is not found
    When the dock operator scans package "pkg-9999" into "STG-D04-A"
    Then the problem is "package-not-found" with status 404

  # --- LoadPackage -------------------------------------------------------

  Scenario: A package goes straight from EXPECTED onto the load through its door
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    Then the response status is 200
    And the response field "state" is "LOADED"
    And the response field "load_id" is "load-bdd1"
    And exactly 1 "PackageLoaded" event was published for package "pkg-1029"
    And the last "PackageLoaded" event has "version" "2"

  Scenario Outline: A package loads only onto its own load through that load's door
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    When the dock operator scans package "pkg-1029" onto load "<load>" through door "<door>"
    Then the problem is "wrong-load" with status 409
    And package "pkg-1029" is "STAGED"
    And exactly 0 "PackageLoaded" events were published

    Examples:
      | case                      | load      | door |
      | through another door      | load-bdd1 | D07  |
      | onto another load         | load-bdd2 | D04  |
      | without naming the door   | load-bdd1 |      |

  Scenario: A package scanned onto the load twice is refused the second time
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    When the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    Then the problem is "package-state" with status 409
    And exactly 1 "PackageLoaded" event was published for package "pkg-1029"

  Scenario: A diverted package can no longer be loaded
    Given package "pkg-2077" of order "order-88424" is manifested on the load
    And package "pkg-2077" is diverted
    When the dock operator scans package "pkg-2077" onto load "load-bdd1" through door "D04"
    Then the problem is "package-state" with status 409
    And package "pkg-2077" is "SHORT_SHIPPED"

  Scenario: Once the truck has left a package can neither be unloaded nor loaded again
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And trailer "trl-91" departs with seal "SL-4471"
    When the dock operator takes package "pkg-1029" back off the load because "changed our mind"
    Then the problem is "already-shipped" with status 409
    When the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    Then the problem is "already-shipped" with status 409
    And package "pkg-1029" is "SHIPPED"

  # --- UnloadPackage -----------------------------------------------------

  Scenario: Unloading puts the package back to STAGED and publishes the operator's reason
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    When the dock operator takes package "pkg-1029" back off the load because "damaged-carton"
    Then the response status is 200
    And the response field "state" is "STAGED"
    And the response field "version" is "4"
    And exactly 1 "PackageUnloaded" event was published for package "pkg-1029"
    And the last "PackageUnloaded" event has "reason" "damaged-carton"

  Scenario: An unload without a reason publishes no reason
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    When the dock operator takes package "pkg-1029" back off the load
    Then the response status is 200
    And the last "PackageUnloaded" event has no "reason"

  Scenario Outline: Only a loaded package unloads
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And <setup>
    When the dock operator takes package "pkg-1029" back off the load
    Then the problem is "package-state" with status 409
    And exactly 0 "PackageUnloaded" events were published

    Examples:
      | setup                                  |
      | package "pkg-1029" stages at "STG-D04-A" |
      | package "pkg-1029" is diverted         |

  Scenario: A package taken back off the load keeps the load from closing until it is loaded again
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And the dock operator takes package "pkg-1029" back off the load because "wrong lane"
    When the supervisor closes load "load-bdd1"
    Then the problem is "packages-outstanding" with status 409
    And load "load-bdd1" is "LOADING"
    When the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    And the supervisor closes load "load-bdd1"
    Then the response status is 200
    And the response field "state" is "CLOSED"

  # --- CloseLoad ---------------------------------------------------------

  Scenario: A load with a package neither loaded nor short-shipped does not close
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When the supervisor closes load "load-bdd1"
    Then the problem is "packages-outstanding" with status 409
    And load "load-bdd1" is "LOADING"
    And exactly 0 "LoadClosed" events were published

  Scenario: Closing with a short-ship records the package and its reason on the load
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When the supervisor closes load "load-bdd1" short-shipping package "pkg-2055" because "NO-STOCK"
    Then the response status is 200
    And the response field "state" is "CLOSED"
    And the response field "package_count" is "1"
    And the response field "short_ships.0.package_id" is "pkg-2055"
    And the response field "short_ships.0.reason" is "NO-STOCK"
    And package "pkg-2055" is "SHORT_SHIPPED"
    And the last "LoadClosed" event has "package_count" "1"
    And the last "LoadClosed" event has "short_shipped_count" "1"

  Scenario: A package that is already loaded cannot be short-shipped
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    When the supervisor closes load "load-bdd1" short-shipping package "pkg-1029" because "DAMAGED"
    Then the problem is "package-state" with status 409
    And package "pkg-1029" is "LOADED"
    And load "load-bdd1" is "LOADING"

  Scenario: Short-shipping a package the load does not know is not found
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    When the supervisor closes load "load-bdd1" short-shipping package "pkg-9999" because "DAMAGED"
    Then the problem is "package-not-found" with status 404
    And load "load-bdd1" is "LOADING"

  Scenario: A diverted package is recorded on the closed load once, with reason DIVERTED
    Given package "pkg-2077" of order "order-88424" is manifested on the load
    And package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2077" is diverted
    When the supervisor closes load "load-bdd1"
    Then the response status is 200
    And the response list "short_ships" has 1 item
    And the response field "short_ships.0.package_id" is "pkg-2077"
    And the response field "short_ships.0.reason" is "DIVERTED"
    And the last "LoadClosed" event has "short_shipped_count" "1"

  Scenario: Naming an already diverted package in the close does not record it twice
    Given package "pkg-2077" of order "order-88424" is manifested on the load
    And package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2077" is diverted
    When the supervisor closes load "load-bdd1" short-shipping package "pkg-2077" because "DIVERTED"
    Then the response status is 200
    And the response list "short_ships" has 1 item
    And the last "LoadClosed" event has "short_shipped_count" "1"

  # Spec: apis/openapi.yaml ShortShip requires a reason (minLength 1) and a
  # malformed body is 400 bad-request ("a missing/invalid required field");
  # use-cases.md: "each entry needs both fields". The code lets
  # ErrShortShipReasonRequired fall through the problem catalogue: 500
  # internal-error.
  @known-bug
  Scenario: A short-ship entry without a reason is refused as a bad request
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When the supervisor closes load "load-bdd1" short-shipping package "pkg-2055" because ""
    Then the problem is "bad-request" with status 400

  Scenario: A load that is already closed refuses a second close
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When the supervisor closes load "load-bdd1"
    Then the problem is "load-state" with status 409
    And exactly 1 "LoadClosed" event was published

  # --- ReopenLoad / confirmed loads -------------------------------------

  Scenario: A supervisor reopens a closed load and it closes again after the correction
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When the supervisor reopens load "load-bdd1"
    Then the response status is 200
    And the response field "state" is "LOADING"
    And the response field "version" is "5"
    And exactly 1 "LoadReopened" event was published
    When the dock operator takes package "pkg-1029" back off the load because "wrong lane"
    And the supervisor closes load "load-bdd1"
    Then the problem is "packages-outstanding" with status 409
    When the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    And the supervisor closes load "load-bdd1"
    Then the response status is 200
    And the response field "state" is "CLOSED"
    And exactly 2 "LoadClosed" events were published

  Scenario: A load that is still loading cannot be reopened
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When the supervisor reopens load "load-bdd1"
    Then the problem is "load-state" with status 409
    And exactly 0 "LoadReopened" events were published

  Scenario Outline: A ship-confirmed load never changes
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And trailer "trl-91" departs with seal "SL-4471"
    When <command>
    Then the problem is "load-confirmed" with status 409
    And load "load-bdd1" is "SHIP_CONFIRMED"

    Examples:
      | command                                  |
      | the supervisor reopens load "load-bdd1"  |
      | the supervisor closes load "load-bdd1"   |

  Scenario: Cancelling a ship-confirmed load is refused and changes nothing
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And trailer "trl-91" departs with seal "SL-4471"
    When the supervisor cancels load "load-bdd1" because "customer called"
    Then the response status is 409
    And load "load-bdd1" is "SHIP_CONFIRMED"
    And exactly 0 "LoadCancelled" events were published

  # Spec: apis/openapi.yaml cancelLoad ("A SHIP_CONFIRMED load refuses with
  # 409 load-confirmed") and the aggregate canvas ("refuseIfConfirmed on every
  # command"). The CancelLoad use case looks for packages first, and a
  # confirmed load always has some: it answers 409 load-has-packages.
  @known-bug
  Scenario: Cancelling a ship-confirmed load answers load-confirmed
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And trailer "trl-91" departs with seal "SL-4471"
    When the supervisor cancels load "load-bdd1" because "customer called"
    Then the problem is "load-confirmed" with status 409

  Scenario: A package manifested after its load closed plans a second load for the same cutoff
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When package "pkg-3009" of order "order-88429" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then 2 loads exist for site "SITE-1"
    And package "pkg-3009" is on load "load-bdd2"
    And load "load-bdd2" is "PLANNED"
    And exactly 2 "LoadPlanned" events were published
