# Derived from: PLAN.md wave 5 BDD list (all six scenarios);
# docs/adr/0002 (ship-confirm follows the departure, incl. the out-of-order
# note), docs/adr/0003 (correlation), docs/adr/0005 (event names).
# Wave 5: the scenarios drive the REAL service harness -- the same use cases
# cmd/api wires (intake dedupe, policies, transactional outbox), over the
# in-memory doubles, with the outbox recorder standing in for the relay.
# Producer facts arrive as fixture events; the yard facts' shape is proven
# against the producer's pinned contract (contracts/yard-management.asyncapi.yaml).
@bdd
Feature: Ship-confirm on departure - the load leaves with what was loaded
  A CLOSED load confirms when TrailerDeparted arrives; a LOADING load never
  confirms — what is not loaded raises PackageMissedLoad once. A re-delivered
  departure confirms nothing twice, a departure that arrived before the load
  closed is handled without ever confirming, and a diverted package still
  lets the load close.

  Background:
    Given site "SITE-1" runs the "16:00" cutoff shipped "STDOUT-2DAY"
    And dock zone "DOCK-D" and staging lane "STG-D04-A" exist at site "SITE-1"
    And appointment "appt-2211" is booked at door "D04" for the "16:00" cutoff

  Scenario: Manifest, stage, load, close, depart - confirmed with one BOL
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And trailer "trl-91" departs with seal "SL-4471"
    Then the load is "SHIP_CONFIRMED"
    And package "pkg-1029" is "SHIPPED"
    And exactly 1 "ShipmentConfirmed" event was published, listing order "order-88421"
    And exactly 1 "BillOfLadingIssued" event was published with seal "SL-4471"

  Scenario: Departure while LOADING never confirms and flags what missed
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When trailer "trl-91" departs with seal "SL-4471"
    Then the load is "LOADING"
    And exactly 1 "PackageMissedLoad" event was published for package "pkg-2055"
    And exactly 0 "ShipmentConfirmed" events were published
    And no bill of lading exists

  Scenario: A departure arriving before the load closes is handled, never confirmed
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When trailer "trl-91" departs with seal "SL-4471" early, before the load closes
    Then the load is "LOADING"
    And exactly 1 "PackageMissedLoad" event was published for package "pkg-2055"
    And exactly 0 "ShipmentConfirmed" events were published
    And no bill of lading exists
    When the supervisor short-ships package "pkg-2055" with reason "LEFT-BEHIND" and closes the load
    Then the load is "CLOSED"
    And exactly 0 "ShipmentConfirmed" events were published
    And no bill of lading exists
    When the same departure is re-delivered
    Then the load is "CLOSED"
    And exactly 0 "ShipmentConfirmed" events were published
    And no bill of lading exists

  Scenario: TrailerDeparted delivered twice confirms once
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When trailer "trl-91" departs with seal "SL-4471"
    And trailer "trl-91" departs with seal "SL-4471" again
    Then the load is "SHIP_CONFIRMED"
    And exactly 1 "ShipmentConfirmed" event was published, listing order "order-88421"
    And exactly 1 "BillOfLadingIssued" event was published with seal "SL-4471"

  Scenario: A diverted package short-ships and the load still closes
    Given package "pkg-2077" of order "order-88424" is manifested on the load
    And package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    When package "pkg-2077" is diverted before the cutoff
    Then package "pkg-2077" is "SHORT_SHIPPED"
    And the load closes with 1 package loaded
    And the load is "CLOSED"

  Scenario: The missed-load sweep flags once, even on a re-run
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    When the cutoff sweep runs at the cutoff
    And the cutoff sweep runs at the cutoff
    Then exactly 1 "PackageMissedLoad" event was published for package "pkg-2055"
    And package "pkg-2055" is "STAGED"
