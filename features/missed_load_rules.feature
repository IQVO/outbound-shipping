# Derived from use-cases.md (SweepMissedLoads, DivertPackage, OnDeparture),
# aggregate-design-canvas.md (FlagMissedLoad "at most once", MarkShortShipped),
# eventstorming.md (lanes "Cutoff passes" and "Diversion") and
# troubleshooting.md. The harness clock stands at 14:00Z, the cutoff of
# the shared load is 16:00Z, site SITE-1.
@bdd
Feature: Missed loads and diversions - flagged once, never dropped silently
  A package that is not loaded by its cutoff raises PackageMissedLoad exactly
  once, whichever path (the sweep or the departure) finds it first. A diverted
  package is short-shipped so its load can still close, and every diversion
  that has nothing to drop is ignored.

  Background:
    Given site "SITE-1" runs the "16:00" cutoff shipped "STDOUT-2DAY"
    And dock zone "DOCK-D" and staging lane "STG-D04-A" exist at site "SITE-1"
    And appointment "appt-2211" is booked at door "D04" for the "16:00" cutoff

  # --- SweepMissedLoads --------------------------------------------------

  Scenario: A sweep one second before the cutoff flags nothing
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When the scheduler sweeps site "SITE-1" at cutoff "2026-11-06T15:59:59Z"
    Then the response status is 200
    And the response field "flagged" is "0"
    And exactly 0 "PackageMissedLoad" events were published

  Scenario: A sweep at the cutoff flags every staged or expected package that is not loaded
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    And package "pkg-3001" of order "order-88425" is manifested on the load
    And package "pkg-4004" of order "order-88426" is manifested on the load
    And package "pkg-4004" is diverted
    When the scheduler sweeps site "SITE-1" at cutoff "2026-11-06T16:00:00Z"
    Then the response field "flagged" is "2"
    And the response field "swept_at" is "2026-11-06T16:00:00Z"
    And exactly 1 "PackageMissedLoad" event was published for package "pkg-2055"
    And exactly 1 "PackageMissedLoad" event was published for package "pkg-3001"
    And exactly 0 "PackageMissedLoad" events were published for package "pkg-1029"
    And exactly 0 "PackageMissedLoad" events were published for package "pkg-4004"
    And package "pkg-2055" is "STAGED"
    And package "pkg-3001" is "EXPECTED"

  Scenario: A re-run sweep flags nothing and publishes nothing
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    And the scheduler sweeps site "SITE-1" at cutoff "2026-11-06T16:00:00Z"
    When the scheduler sweeps site "SITE-1" at cutoff "2026-11-06T16:00:00Z"
    Then the response field "flagged" is "0"
    And exactly 1 "PackageMissedLoad" event was published

  Scenario: A sweep without a cutoff runs at the clock and finds nothing due before the cutoff
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    When the scheduler sweeps site "SITE-1"
    Then the response field "flagged" is "0"
    And the response field "swept_at" is "2026-11-06T14:00:00Z"

  Scenario: A sweep of another site flags nothing
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    When the scheduler sweeps site "SITE-2" at cutoff "2026-11-06T16:00:00Z"
    Then the response field "flagged" is "0"
    And exactly 0 "PackageMissedLoad" events were published

  Scenario Outline: A sweep request that is not valid is a bad request
    When a POST is sent to "/missed-load-sweeps" with the body <body> and the Idempotency-Key "sweep-bad-request-0001"
    Then the problem is "bad-request" with status 400

    Examples:
      | case                  | body                                         |
      | no site                | {}                                           |
      | a cutoff that is not RFC 3339 | {"site_id":"SITE-1","cutoff":"yesterday"} |
      | a field the sweep does not know | {"site_id":"SITE-1","grace":5}          |

  Scenario: A package flagged by the sweep is not flagged again by the departure
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    And the cutoff sweep runs at the cutoff
    When trailer "trl-91" departs with seal "SL-4471"
    Then exactly 1 "PackageMissedLoad" event was published for package "pkg-2055"

  Scenario: The missed-loads read lists what was flagged and not what was loaded
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When the scheduler sweeps site "SITE-1" at cutoff "2026-11-06T16:00:00Z"
    Then the missed-loads read for site "SITE-1" lists exactly "pkg-2055"

  # --- DivertPackage -----------------------------------------------------

  Scenario Outline: An expected or staged package that is diverted becomes SHORT_SHIPPED
    Given package "pkg-2077" of order "order-88424" is manifested on the load
    And <setup>
    When package "pkg-2077" is diverted
    Then the consumer outcome is "applied"
    And package "pkg-2077" is "SHORT_SHIPPED"

    Examples:
      | setup                                      |
      | package "pkg-2077" stages at "STG-D04-A"   |
      | package "pkg-2077" is at version 1         |

  Scenario: A diversion of a package nobody manifested is ignored
    When package "pkg-9999" is diverted
    Then the consumer outcome is "ignored"
    And no package "pkg-9999" exists

  Scenario: A diversion that finds the package already loaded is ignored
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    When package "pkg-1029" is diverted
    Then the consumer outcome is "ignored"
    And package "pkg-1029" is "LOADED"

  Scenario: A diversion that finds the package already shipped is ignored
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And trailer "trl-91" departs with seal "SL-4471"
    When package "pkg-1029" is diverted
    Then the consumer outcome is "ignored"
    And package "pkg-1029" is "SHIPPED"

  Scenario: A second diversion of the same package is ignored
    Given package "pkg-2077" of order "order-88424" is manifested on the load
    And package "pkg-2077" is diverted
    When package "pkg-2077" is diverted
    Then the consumer outcome is "ignored"
    And package "pkg-2077" is "SHORT_SHIPPED"

  Scenario: The same diversion delivered twice is a duplicate
    Given package "pkg-2077" of order "order-88424" is manifested on the load
    And package "pkg-2077" is diverted
    When the same event is delivered again
    Then the consumer outcome is "duplicate"
