# Derived from use-cases.md (VoidBillOfLading, OnDeparture "bill of lading is
# issued with the carrier and SCAC read from the appointment copy, the seal,
# the package count and total weight"), aggregate-design-canvas.md
# (BillOfLading states and invariants) and the troubleshooting REST table.
# Every package of the shared harness weighs 1200 g; the appointment names
# carrier "STDOUT" (SCAC "STDOUT"); the first BOL of the harness is
# BOL-2026-11-06-01.
@bdd
Feature: Bill of lading rules - issued once with the yard's seal, voided without undoing the shipment
  The document of a confirmed load certifies carrier, trailer, seal, the
  shipped package count and their weight. A supervisor can void it once, with
  a reason, and the shipment stands.

  Background:
    Given site "SITE-1" runs the "16:00" cutoff shipped "STDOUT-2DAY"
    And dock zone "DOCK-D" and staging lane "STG-D04-A" exist at site "SITE-1"
    And appointment "appt-2211" is booked at door "D04" for the "16:00" cutoff
    And package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And package "pkg-2055" loads through door "D04"
    And the load closes with 2 packages loaded
    And trailer "trl-91" departs with seal "SL-4471"

  Scenario: The bill of lading certifies carrier, trailer, seal, packages and weight
    When GET "/bills-of-lading/BOL-2026-11-06-01" is requested
    Then the response status is 200
    And the response field "state" is "ISSUED"
    And the response field "version" is "1"
    And the response field "load_id" is "load-bdd1"
    And the response field "carrier" is "STDOUT"
    And the response field "carrier_scac" is "STDOUT"
    And the response field "trailer_id" is "trl-91"
    And the response field "seal_number" is "SL-4471"
    And the response field "package_count" is "2"
    And the response field "total_weight_g" is "2400"
    And the response field "voided_at" is absent

  Scenario: Voiding an issued bill of lading records the reason and leaves the shipment standing
    When the supervisor voids bill of lading "BOL-2026-11-06-01" because "re-issued with the corrected seal"
    Then the response status is 200
    And the response field "state" is "VOIDED"
    And the response field "version" is "2"
    And the response field "void_reason" is "re-issued with the corrected seal"
    And exactly 1 "BillOfLadingVoided" event was published
    And the last "BillOfLadingVoided" event has "reason" "re-issued with the corrected seal"
    And load "load-bdd1" is "SHIP_CONFIRMED"
    And package "pkg-1029" is "SHIPPED"
    And package "pkg-2055" is "SHIPPED"

  Scenario: A voided bill of lading is not issued again by a later departure
    Given the supervisor voids bill of lading "BOL-2026-11-06-01" because "re-issued"
    When trailer "trl-91" departs with seal "SL-4472"
    Then the consumer outcome is "ignored"
    And exactly 1 "BillOfLadingIssued" event was published

  Scenario: A bill of lading is voided only once
    Given the supervisor voids bill of lading "BOL-2026-11-06-01" because "re-issued"
    When the supervisor voids bill of lading "BOL-2026-11-06-01" because "again"
    Then the problem is "bol-already-voided" with status 409
    And exactly 1 "BillOfLadingVoided" event was published

  Scenario: A void without a reason is a bad request and the document stays issued
    When the supervisor voids bill of lading "BOL-2026-11-06-01" because ""
    Then the problem is "bad-request" with status 400
    When GET "/bills-of-lading/BOL-2026-11-06-01" is requested
    Then the response field "state" is "ISSUED"
    And exactly 0 "BillOfLadingVoided" events were published

  Scenario: Voiding or reading a bill of lading that does not exist is not found
    When the supervisor voids bill of lading "BOL-2026-11-06-99" because "typo"
    Then the problem is "bol-not-found" with status 404
    When GET "/bills-of-lading/BOL-2026-11-06-99" is requested
    Then the problem is "bol-not-found" with status 404
