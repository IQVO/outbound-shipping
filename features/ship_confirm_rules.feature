# Derived from use-cases.md (OnDeparture table, "Idempotency in three layers",
# "Invariants"), eventstorming.md (hotspots "trailer leaves before the load
# was closed", "A departure that has no seal"), business-context.md
# (lifecycle step 5) and troubleshooting.md ("Ship-confirm does not happen").
# Complements ship_confirm_on_departure.feature, which pins the headline
# paths; this file pins every other row of the OnDeparture table.
@bdd
Feature: Ship-confirm rules - what a TrailerDeparted does to each load state
  A departure confirms a CLOSED load that carries the yard's seal, and
  nothing else: no seal, no load, another site, an already confirmed load and
  a load that is still LOADING each have their own documented effect.

  Background:
    Given site "SITE-1" runs the "16:00" cutoff shipped "STDOUT-2DAY"
    And dock zone "DOCK-D" and staging lane "STG-D04-A" exist at site "SITE-1"
    And appointment "appt-2211" is booked at door "D04" for the "16:00" cutoff

  Scenario: A departure without a seal confirms nothing until one carrying the seal arrives
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When trailer "trl-91" departs without a seal
    Then the consumer outcome is "ignored"
    And load "load-bdd1" is "CLOSED"
    And package "pkg-1029" is "LOADED"
    And exactly 0 "ShipmentConfirmed" events were published
    And no bill of lading exists for load "load-bdd1"
    When trailer "trl-91" departs with seal "SL-4471"
    Then the consumer outcome is "applied"
    And load "load-bdd1" is "SHIP_CONFIRMED"
    And exactly 1 "BillOfLadingIssued" event was published with seal "SL-4471"

  Scenario: A departure of a trailer this service never saw is ignored
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When trailer "trl-77" departs with seal "SL-9000"
    Then the consumer outcome is "ignored"
    And load "load-bdd1" is "CLOSED"

  Scenario: A departure of the trailer from another site is ignored
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When trailer "trl-91" of site "SITE-2" departs with seal "SL-9000"
    Then the consumer outcome is "ignored"
    And load "load-bdd1" is "CLOSED"

  Scenario: A later departure with a new event id finds the load confirmed and does nothing
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And trailer "trl-91" departs with seal "SL-4471"
    When trailer "trl-91" departs with seal "SL-4472"
    Then the consumer outcome is "ignored"
    And exactly 1 "ShipmentConfirmed" event was published
    And exactly 1 "BillOfLadingIssued" event was published with seal "SL-4471"

  Scenario: The confirmation lists each order once, sorted
    Given package "pkg-3001" of order "order-88423" is manifested on the load
    And package "pkg-3002" of order "order-88421" is manifested on the load
    And package "pkg-3003" of order "order-88423" is manifested on the load
    And package "pkg-3001" loads through door "D04"
    And package "pkg-3002" loads through door "D04"
    And package "pkg-3003" loads through door "D04"
    And the load closes with 3 packages loaded
    When trailer "trl-91" departs with seal "SL-4471"
    Then the last "ShipmentConfirmed" event lists the orders "order-88421, order-88423"
    And the last "ShipmentConfirmed" event has "carrier" "STDOUT"
    And the last "ShipmentConfirmed" event has "trailer_id" "trl-91"
    And the last "ShipmentConfirmed" event has "seal_number" "SL-4471"

  Scenario: A package taken off a closed load before the truck leaves is not shipped
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" loads through door "D04"
    And the load closes with 2 packages loaded
    And the dock operator takes package "pkg-2055" back off the load because "carrier refused it"
    When trailer "trl-91" departs with seal "SL-4471"
    Then package "pkg-1029" is "SHIPPED"
    And package "pkg-2055" is "STAGED"
    And the last "ShipmentConfirmed" event lists the orders "order-88421"
    And the last "BillOfLadingIssued" event has "package_count" "1"
    And the last "BillOfLadingIssued" event has "total_weight_g" "1200"

  Scenario: A package manifested without an order id ships but names no order on the confirmation
    Given package "pkg-3001" of order "" is manifested on the load
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-3001" loads through door "D04"
    And package "pkg-2055" loads through door "D04"
    And the load closes with 2 packages loaded
    When trailer "trl-91" departs with seal "SL-4471"
    Then package "pkg-3001" is "SHIPPED"
    And the last "ShipmentConfirmed" event lists the orders "order-88423"
    And the last "BillOfLadingIssued" event has "package_count" "2"

  Scenario: A short-shipped package stays behind and is not counted on the confirmation
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    And the supervisor short-ships package "pkg-2055" with reason "NO-STOCK" and closes the load
    When trailer "trl-91" departs with seal "SL-4471"
    Then package "pkg-1029" is "SHIPPED"
    And package "pkg-2055" is "SHORT_SHIPPED"
    And the last "ShipmentConfirmed" event lists the orders "order-88421"
    And the last "BillOfLadingIssued" event has "package_count" "1"

  Scenario: A departure that finds the load still loading flags expected and staged packages once, however often it repeats
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    And package "pkg-3001" of order "order-88425" is manifested on the load
    When trailer "trl-91" departs with seal "SL-4471"
    And trailer "trl-91" departs with seal "SL-4472"
    Then load "load-bdd1" is "LOADING"
    And exactly 1 "PackageMissedLoad" event was published for package "pkg-2055"
    And exactly 1 "PackageMissedLoad" event was published for package "pkg-3001"
    And exactly 0 "PackageMissedLoad" events were published for package "pkg-1029"
    And exactly 0 "ShipmentConfirmed" events were published
    And no bill of lading exists for load "load-bdd1"

  Scenario: A reopened load that the truck then leaves is flagged, never confirmed
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" stages at "STG-D04-A"
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And the supervisor reopens load "load-bdd1"
    And the dock operator takes package "pkg-1029" back off the load because "wrong lane"
    When trailer "trl-91" departs with seal "SL-4471"
    Then load "load-bdd1" is "LOADING"
    And exactly 1 "PackageMissedLoad" event was published for package "pkg-1029"
    And exactly 0 "ShipmentConfirmed" events were published

  Scenario: A confirm that cannot issue its bill of lading publishes nothing
    Given the appointment names no carrier
    And package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    When trailer "trl-91" departs with seal "SL-4471" but the confirm cannot complete
    Then exactly 0 "ShipmentConfirmed" events were published
    And exactly 0 "BillOfLadingIssued" events were published

  Scenario Outline: A departure that names no trailer or no site is refused as invalid
    When a departure without a <field> arrives
    Then the event is refused as invalid

    Examples:
      | field      |
      | trailer id |
      | site       |
