# Derived from use-cases.md (ProjectBoard, "AdherenceReport ... reads
# load_board; adherence = loads_closed / loads_total", LoadsAtRisk "open loads
# whose cutoff is within the horizon (default 4 h)", MissedLoads),
# ubiquitous-language.md (Load adherence, Loads at risk) and
# troubleshooting.md ("Read models and MCP"). The board is fed by this
# service's own analytics stream: every scenario folds the published events
# through the real analytics handlers. Clock 14:00Z, cutoff 16:00Z.
@bdd
Feature: Load board rules - what the adherence report and the risk read show
  The adherence report counts, per site, operating date and cutoff, the
  loads planned and the loads closed, and the packages that missed; a load
  at risk is an open load whose cutoff is near.

  Background:
    Given site "SITE-1" runs the "16:00" cutoff shipped "STDOUT-2DAY"
    And dock zone "DOCK-D" and staging lane "STG-D04-A" exist at site "SITE-1"
    And appointment "appt-2211" is booked at door "D04" for the "16:00" cutoff

  Scenario: Before anything is projected the report has no rows
    When GET "/reports/load-adherence" is requested
    Then the response status is 200
    And the response list "rows" has 0 items
    And the response field "generated_at" is "2026-11-06T14:00:00Z"

  Scenario: A planned load that has not closed counts as a total with adherence 0
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And the analytics projector folds every published event
    When GET "/reports/load-adherence?site_id=SITE-1" is requested
    Then the response list "rows" has 1 item
    And the response field "rows.0.cpt_id" is "cpt-1600"
    And the response field "rows.0.operating_date" is "2026-11-06"
    And the response field "rows.0.loads_total" is "1"
    And the response field "rows.0.loads_closed" is "0"
    And the response field "rows.0.adherence" is "0"
    And the response field "rows.0.packages_missed" is "0"

  Scenario: A closed load counts as closed and adherence is closed over total
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And the analytics projector folds every published event
    When GET "/reports/load-adherence?site_id=SITE-1" is requested
    Then the response field "rows.0.loads_closed" is "1"
    And the response field "rows.0.adherence" is "1"

  Scenario: A load planned for the same cutoff after the first closed makes adherence one half
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And package "pkg-3009" of order "order-88429" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And the analytics projector folds every published event
    When GET "/reports/load-adherence?site_id=SITE-1&operating_date=2026-11-06" is requested
    Then the response list "rows" has 1 item
    And the response field "rows.0.loads_total" is "2"
    And the response field "rows.0.loads_closed" is "1"
    And the response field "rows.0.adherence" is "0.5"

  Scenario: A package that missed its load is counted once, however many paths flagged it
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    And package "pkg-2055" stages at "STG-D04-A"
    And the cutoff sweep runs at the cutoff
    And trailer "trl-91" departs with seal "SL-4471"
    And the analytics projector folds every published event
    When GET "/reports/load-adherence?site_id=SITE-1" is requested
    Then the response field "rows.0.packages_missed" is "1"

  Scenario: Folding the same events a second time changes nothing
    Given package "pkg-2055" of order "order-88423" is manifested on the load
    And the cutoff sweep runs at the cutoff
    And the analytics projector folds every published event
    And the analytics projector folds every published event
    When GET "/reports/load-adherence?site_id=SITE-1" is requested
    Then the response field "rows.0.loads_total" is "1"
    And the response field "rows.0.packages_missed" is "1"

  Scenario Outline: The report is filtered by site and by operating date
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And the analytics projector folds every published event
    When GET "/reports/load-adherence?<filter>" is requested
    Then the response list "rows" has <rows> items

    Examples:
      | filter                    | rows |
      | site_id=SITE-1            | 1    |
      | site_id=SITE-2            | 0    |
      | operating_date=2026-11-06 | 1    |
      | operating_date=2026-11-07 | 0    |

  Scenario: Only open loads whose cutoff is within the horizon are at risk
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
      | cpt-2000 | 20:00      | EXPRESS     |      |
    And package "pkg-3001" of order "order-88425" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And package "pkg-3002" of order "order-88426" is manifested for the cutoff "2026-11-06T20:00:00Z"
    And the analytics projector folds every published event
    Then the loads at risk for site "SITE-1" number 1
    And the loads at risk for site "SITE-1" within 8 hours number 2
    And the loads at risk for site "SITE-1" within 1 hours number 0

  Scenario: A load that closed is no longer at risk
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And package "pkg-1029" loads through door "D04"
    And the load closes with 1 package loaded
    And the analytics projector folds every published event
    Then the loads at risk for site "SITE-1" number 0
