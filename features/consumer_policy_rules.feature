# Derived from use-cases.md ("Consumer policies": ExpectPackage, BookAppointment,
# DockTrailer, RecordDoorWork, ApplyCptSchedule, ApplyLayout, "Consumers return
# an outcome"), troubleshooting.md ("A load is not planned, or does not
# progress", "Appointment before the load") and business-context.md (lifecycle
# steps 1 and 2). Producer facts arrive as fixture events with explicit
# CloudEvents ids; no Background of the shared harness.
@bdd
Feature: Consumer policy rules - which facts plan a load, assign a door and start loading
  A manifest plans or joins the one open load of its cutoff key, or is
  ignored; an outbound appointment names the door of the open load of its
  cpt; a docking at that door starts loading. Redeliveries are duplicates and
  payloads that can never be applied are refused as invalid.

  # --- ExpectPackage: resolving the cutoff key ---------------------------

  Scenario: A manifest without a site_id is ignored and plans nothing
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When a manifest of package "pkg-1029" of order "order-88421" for the cutoff "2026-11-06T16:00:00Z" arrives without a "site_id"
    Then the consumer outcome is "ignored"
    And no package "pkg-1029" exists
    And the open loads of site "SITE-1" number 0
    And nothing was published

  Scenario: A manifest without a cpt is ignored and plans nothing
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When a manifest of package "pkg-1029" of order "order-88421" for the cutoff "2026-11-06T16:00:00Z" arrives without a "cpt"
    Then the consumer outcome is "ignored"
    And no package "pkg-1029" exists
    And nothing was published

  Scenario: A manifest for a site whose schedule copy is unknown is ignored
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then the consumer outcome is "ignored"
    And no package "pkg-1029" exists
    And nothing was published

  Scenario: A manifest whose cutoff matches no schedule entry is ignored
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T17:00:00Z"
    Then the consumer outcome is "ignored"
    And no package "pkg-1029" exists
    And the open loads of site "SITE-1" number 0

  Scenario Outline: A schedule entry that lists weekdays matches only those days (2026-11-06 is a Friday)
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days   |
      | cpt-1600 | 16:00      | STDOUT-2DAY | <days> |
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then the consumer outcome is "<outcome>"
    And the open loads of site "SITE-1" number <loads>

    Examples:
      | days    | outcome | loads |
      | Mon,Tue | ignored | 0     |
      | Mon     | ignored | 0     |
      | Fri     | applied | 1     |
      | Mon,fri | applied | 1     |
      |         | applied | 1     |

  Scenario: The cutoff instant is read in the site's own timezone
    Given site "SITE-1" in timezone "America/Sao_Paulo" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T19:00:00Z"
    Then the consumer outcome is "applied"
    When GET "/loads/load-bdd1" is requested
    Then the response field "operating_date" is "2026-11-06"
    And the response field "cutoff" is "2026-11-06T19:00:00Z"
    And the response field "cpt_id" is "cpt-1600"

  Scenario: An instant that is 16:00 in UTC is 13:00 in São Paulo and matches no entry
    Given site "SITE-1" in timezone "America/Sao_Paulo" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then the consumer outcome is "ignored"
    And no package "pkg-1029" exists

  Scenario: The operating date is the local date, even when UTC is already the next day
    Given site "SITE-1" in timezone "America/Sao_Paulo" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-2330 | 23:30      | EXPRESS     |      |
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-07T02:30:00Z"
    Then the consumer outcome is "applied"
    When GET "/loads/load-bdd1" is requested
    Then the response field "operating_date" is "2026-11-06"
    And the response field "ship_method" is "EXPRESS"

  # --- ExpectPackage: planning, joining, redelivery ---------------------

  Scenario: The first manifest of a cutoff plans the load and the next one joins it
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And package "pkg-2055" of order "order-88423" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then the open loads of site "SITE-1" number 1
    And package "pkg-1029" is on load "load-bdd1"
    And package "pkg-2055" is on load "load-bdd1"
    And exactly 1 "LoadPlanned" event was published
    And the last "LoadPlanned" event has "cpt_id" "cpt-1600"
    And the last "LoadPlanned" event has "operating_date" "2026-11-06"
    And the last "LoadPlanned" event has "ship_method" "STDOUT-2DAY"
    And the last "LoadPlanned" event has "cutoff" "2026-11-06T16:00:00Z"
    And the last "LoadPlanned" event has "version" "1"

  Scenario: Manifests of two different cutoffs plan two loads, each with its own ship method
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
      | cpt-2000 | 20:00      | EXPRESS     |      |
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And package "pkg-2055" of order "order-88423" is manifested for the cutoff "2026-11-06T20:00:00Z"
    Then the open loads of site "SITE-1" number 2
    And package "pkg-1029" is on load "load-bdd1"
    And package "pkg-2055" is on load "load-bdd2"
    When GET "/loads/load-bdd2" is requested
    Then the response field "cpt_id" is "cpt-2000"
    And the response field "ship_method" is "EXPRESS"

  Scenario: The same package manifested again under a new event id is a no-op
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When package "pkg-1029" of order "order-88421" is manifested again for the cutoff "2026-11-06T16:00:00Z" under a new event id
    Then the consumer outcome is "applied"
    And package "pkg-1029" is at version 1
    And the open loads of site "SITE-1" number 1
    And exactly 1 "LoadPlanned" event was published

  Scenario: The same manifest delivered twice is a duplicate
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When the same event is delivered again
    Then the consumer outcome is "duplicate"
    And exactly 1 "LoadPlanned" event was published

  Scenario: An ignored manifest still claims its event id, so replaying it stays a duplicate
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then the consumer outcome is "ignored"
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When the same event is delivered again
    Then the consumer outcome is "duplicate"
    And no package "pkg-1029" exists
    When package "pkg-1029" of order "order-88421" is manifested again for the cutoff "2026-11-06T16:00:00Z" under a new event id
    Then the consumer outcome is "applied"
    And package "pkg-1029" is on load "load-bdd1"

  Scenario: A manifest without a package id is refused as invalid
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When a manifest without a package id for the cutoff "2026-11-06T16:00:00Z" arrives
    Then the event is refused as invalid
    And the open loads of site "SITE-1" number 0

  # --- ApplyCptSchedule / ApplyLayout ------------------------------------

  Scenario: A new schedule replaces the whole old one; the planned load stands but the old cutoff is gone
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-2000 | 20:00      | EXPRESS     |      |
    When package "pkg-2055" of order "order-88423" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then the consumer outcome is "ignored"
    And no package "pkg-2055" exists
    And the open loads of site "SITE-1" number 1

  Scenario Outline: A schedule change that can never be applied is refused and the old schedule stays
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When a schedule change for site "SITE-1" <defect> arrives
    Then the event is refused as invalid
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then the consumer outcome is "applied"

    Examples:
      | defect                                   |
      | with an entry lacking a ship method      |
      | at version 0                             |

  Scenario: A location registered without its zone is refused as invalid
    When a location registration without a zone id arrives
    Then the event is refused as invalid

  # --- BookAppointment ---------------------------------------------------

  Scenario: An outbound appointment naming the cpt assigns its door to the open load
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    Then the consumer outcome is "applied"
    And load "load-bdd1" has door "D04"
    And exactly 1 "LoadDoorAssigned" event was published
    And the last "LoadDoorAssigned" event has "appointment_id" "appt-2211"
    And the last "LoadDoorAssigned" event has "door_code" "D04"

  Scenario: An appointment is assigned to the first open load of its cpt only
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And package "pkg-3001" of order "order-88425" is manifested for the cutoff "2026-11-07T16:00:00Z"
    When an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    Then load "load-bdd1" has door "D04"
    And load "load-bdd2" has no door
    And exactly 1 "LoadDoorAssigned" event was published

  Scenario Outline: An appointment that does not name the open load's cpt only updates the copy
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When <booking>
    Then the consumer outcome is "ignored"
    And the appointment "<appointment>" is saved in the local copy
    And load "load-bdd1" has no door
    And exactly 0 "LoadDoorAssigned" events were published

    Examples:
      | appointment | booking                                                                                          |
      | appt-9001   | an "INBOUND" appointment "appt-9001" for cpt "cpt-1600" at door "D06" carried by "ACME" is booked |
      | appt-9002   | an "OUTBOUND" appointment "appt-9002" at door "D06" carried by "ACME" is booked                   |
      | appt-9003   | an "OUTBOUND" appointment "appt-9003" for cpt "cpt-2000" at door "D06" carried by "ACME" is booked |

  Scenario: A later appointment for the same cpt re-assigns the door, also while loading
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    When an "OUTBOUND" appointment "appt-2212" for cpt "cpt-1600" at door "D06" carried by "STDOUT" is booked
    Then load "load-bdd1" has door "D06"
    And trailer "trl-91" docks at door "D06"
    And load "load-bdd1" is "LOADING"
    When an "OUTBOUND" appointment "appt-2213" for cpt "cpt-1600" at door "D07" carried by "STDOUT" is booked
    Then load "load-bdd1" has door "D07"
    And exactly 3 "LoadDoorAssigned" events were published

  # The documented known gap (troubleshooting.md "Appointment before the load
  # (planned)"): the door is not assigned from the saved copy when the load is
  # planned later; another appointment event for the cpt assigns it.
  Scenario: An appointment that arrives before the load leaves the load without a door until another appointment arrives
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    When an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    Then the consumer outcome is "ignored"
    And the appointment "appt-2211" is saved in the local copy
    When package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    Then load "load-bdd1" has no door
    When an "OUTBOUND" appointment "appt-2212" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    Then the consumer outcome is "applied"
    And load "load-bdd1" has door "D04"

  # --- DockTrailer / RecordDoorWork --------------------------------------

  Scenario: A trailer docking at the load's door attaches it and starts loading
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    When trailer "trl-91" docks at door "D04"
    Then the consumer outcome is "applied"
    And load "load-bdd1" is "LOADING"
    And load "load-bdd1" has trailer "trl-91"
    And exactly 1 "LoadingStarted" event was published
    And the last "LoadingStarted" event has "trailer_id" "trl-91"
    And the last "LoadingStarted" event has "door_code" "D04"
    And the last "LoadingStarted" event has "version" "3"

  Scenario: A docking before any door is assigned is ignored, and loading starts once the door and a new docking arrive
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When trailer "trl-91" docks at door "D04"
    Then the consumer outcome is "ignored"
    And load "load-bdd1" is "PLANNED"
    And exactly 0 "LoadingStarted" events were published
    When an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    And trailer "trl-91" docks at door "D04"
    Then the consumer outcome is "applied"
    And load "load-bdd1" is "LOADING"

  Scenario: A docking that names no door is refused as invalid and attaches nothing to a load without a door
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    When trailer "trl-91" docks without naming a door
    Then the event is refused as invalid
    And load "load-bdd1" is "PLANNED"
    And load "load-bdd1" has trailer ""

  Scenario: A docking at a door no open load uses is ignored
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    When trailer "trl-91" docks at door "D09"
    Then the consumer outcome is "ignored"
    And load "load-bdd1" is "PLANNED"
    And load "load-bdd1" has trailer ""

  Scenario: A trailer that docks again at a loading load changes nothing and raises nothing
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    And trailer "trl-91" docks at door "D04"
    When trailer "trl-91" docks at door "D04"
    Then the consumer outcome is "applied"
    And load "load-bdd1" is "LOADING"
    And exactly 1 "LoadingStarted" event was published

  Scenario: Door work completed is recorded and changes no load state
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And an "OUTBOUND" appointment "appt-2211" for cpt "cpt-1600" at door "D04" carried by "STDOUT" is booked
    And trailer "trl-91" docks at door "D04"
    When door work completes for trailer "trl-91" at door "D04"
    Then the consumer outcome is "applied"
    And the door work of trailer "trl-91" is recorded
    And load "load-bdd1" is "LOADING"
    And exactly 0 "LoadClosed" events were published
