# Derived from use-cases.md (CancelLoad, ExpectPackage "one open load per key",
# LoadPackage "including when the load has no door yet"), aggregate-design-
# canvas.md (OutboundLoad state machine, "Cross-aggregate rules") and
# eventstorming.md (lane "Cancel", hotspot "Can a load with packages be
# cancelled?"). No Background of the shared harness: the scenarios plan their
# own loads from explicit manifests, so a load can stay PLANNED.
@bdd
Feature: Load lifecycle rules - planned, cancelled and closed loads
  A planned load can be cancelled only while it has no packages and a
  reason is given; only an open load (PLANNED or LOADING) holds a cutoff key;
  a planned load that has no door and no trailer cannot be loaded, closed or
  reopened.

  Background:
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |

  Scenario: A planned load with no door refuses every loading scan
    Given package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And load "load-bdd1" is "PLANNED"
    And load "load-bdd1" has no door
    When the dock operator scans package "pkg-1029" onto load "load-bdd1" through door "D04"
    Then the problem is "wrong-load" with status 409
    And package "pkg-1029" is "EXPECTED"

  Scenario Outline: A planned load that never started loading refuses the supervisor's close and reopen
    Given package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When <command>
    Then the problem is "load-state" with status 409
    And load "load-bdd1" is "PLANNED"

    Examples:
      | command                                 |
      | the supervisor closes load "load-bdd1"  |
      | the supervisor reopens load "load-bdd1" |

  Scenario: A planned load that already has packages cannot be cancelled
    Given package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When the supervisor cancels load "load-bdd1" because "carrier cancelled the pickup"
    Then the problem is "load-has-packages" with status 409
    And the problem detail mentions "short-ship"
    And load "load-bdd1" is "PLANNED"
    And exactly 0 "LoadCancelled" events were published

  Scenario: A load that is already loading cannot be cancelled either
    Given package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    And trailer "trl-91" docks at door "D04"
    When the supervisor cancels load "load-bdd1" because "carrier cancelled the pickup"
    Then the response status is 409
    And load "load-bdd1" is "LOADING"
    And exactly 0 "LoadCancelled" events were published

  Scenario: A planned load with no packages is cancelled with its reason
    Given an empty planned load "load-seed1" exists for the "16:00" cutoff of site "SITE-1"
    When the supervisor cancels load "load-seed1" because "carrier cancelled the pickup"
    Then the response status is 200
    And the response field "state" is "CANCELLED"
    And the response field "version" is "2"
    And exactly 1 "LoadCancelled" event was published

  Scenario: A cancellation without a reason is a bad request and the load stays planned
    Given an empty planned load "load-seed1" exists for the "16:00" cutoff of site "SITE-1"
    When the supervisor cancels load "load-seed1" because ""
    Then the problem is "bad-request" with status 400
    And load "load-seed1" is "PLANNED"
    And exactly 0 "LoadCancelled" events were published

  Scenario: A load is cancelled only once
    Given an empty planned load "load-seed1" exists for the "16:00" cutoff of site "SITE-1"
    And the supervisor cancels load "load-seed1" because "carrier cancelled the pickup"
    When the supervisor cancels load "load-seed1" because "again"
    Then the problem is "load-state" with status 409
    And exactly 1 "LoadCancelled" event was published

  Scenario: A cancelled load no longer holds its cutoff, so the next manifest plans a fresh load
    Given an empty planned load "load-seed1" exists for the "16:00" cutoff of site "SITE-1"
    And the supervisor cancels load "load-seed1" because "carrier cancelled the pickup"
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then package "pkg-1029" is on load "load-bdd1"
    And load "load-bdd1" is "PLANNED"
    And load "load-seed1" is "CANCELLED"
    And exactly 1 "LoadPlanned" event was published

  Scenario: While a load is open the next manifest of its cutoff joins it instead of planning another
    Given an empty planned load "load-seed1" exists for the "16:00" cutoff of site "SITE-1"
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then package "pkg-1029" is on load "load-seed1"
    And the open loads of site "SITE-1" number 1
    And exactly 0 "LoadPlanned" events were published

  Scenario: A cancelled load is not open, so an appointment for its cutoff assigns no door
    Given an empty planned load "load-seed1" exists for the "16:00" cutoff of site "SITE-1"
    And the supervisor cancels load "load-seed1" because "carrier cancelled the pickup"
    When an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    Then the consumer outcome is "ignored"
    And load "load-seed1" has no door
    And exactly 0 "LoadDoorAssigned" events were published
