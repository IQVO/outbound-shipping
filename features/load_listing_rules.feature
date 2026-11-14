# Derived from use-cases.md ("Queries": keyset pagination, limit 1 to 200
# default 50; GetLoad, ListLoads, ListLoadPackages), troubleshooting.md
# ("404 load-not-found") and apis/openapi.yaml (filters). Two cutoffs plan two
# loads: load-bdd1 (16:00 STDOUT-2DAY, cpt-1600) and load-bdd2 (20:00 EXPRESS,
# cpt-2000), both on 2026-11-06; a third on 2026-11-07.
@bdd
Feature: Load reads - filters, pages and packages of a load
  The list of loads filters by site, state and operating date and is keyset
  paged; the packages of a load filter by package state.

  Background:
    Given site "SITE-1" in timezone "UTC" runs these cutoffs:
      | cpt      | local_time | ship_method | days |
      | cpt-1600 | 16:00      | STDOUT-2DAY |      |
      | cpt-2000 | 20:00      | EXPRESS     |      |
    And package "pkg-1029" of order "order-88421" is manifested for the cutoff "2026-11-06T16:00:00Z"
    And package "pkg-2055" of order "order-88423" is manifested for the cutoff "2026-11-06T20:00:00Z"
    And package "pkg-3001" of order "order-88425" is manifested for the cutoff "2026-11-07T16:00:00Z"

  Scenario: The list filters by state
    Given an "OUTBOUND" appointment "appt-2211" for cpt "cpt-2000" at door "D04" carried by "ACME" is booked
    And trailer "trl-91" docks at door "D04"
    When GET "/loads?state=LOADING" is requested
    Then the response list "items" has 1 item
    And the response field "items.0.id" is "load-bdd2"
    When GET "/loads?state=PLANNED" is requested
    Then the response list "items" has 2 items

  Scenario: The list filters by operating date and by site
    When GET "/loads?operating_date=2026-11-07" is requested
    Then the response list "items" has 1 item
    And the response field "items.0.id" is "load-bdd3"
    When GET "/loads?site_id=SITE-9" is requested
    Then the response list "items" has 0 items
    When GET "/loads?site_id=SITE-1&operating_date=2026-11-06" is requested
    Then the response list "items" has 2 items

  Scenario: A page that holds every load has no next cursor
    When GET "/loads?limit=200" is requested
    Then the response list "items" has 3 items
    And the response field "nextCursor" is "null"

  Scenario: A page that is full carries a cursor and the next page continues after it
    When GET "/loads?limit=1" is requested
    Then the response list "items" has 1 item
    When the next page of "/loads" is requested with limit 1
    Then the response list "items" has 1 item
    And the page does not repeat the previous page's first item

  Scenario: The packages of a load filter by package state
    Given zone "zone-1" belongs to site "SITE-1"
    And location "STG-D04-A" is registered in zone "zone-1" with role "Staging"
    And the dock operator scans package "pkg-1029" into "STG-D04-A"
    When GET "/loads/load-bdd1/packages?state=EXPECTED" is requested
    Then the response list "items" has 0 items
    When GET "/loads/load-bdd1/packages?state=STAGED" is requested
    Then the response list "items" has 1 item
    And the response field "items.0.id" is "pkg-1029"
    When GET "/loads/load-bdd1/packages" is requested
    Then the response list "items" has 1 item

  Scenario: Reading a load, or the packages of a load, that does not exist is not found
    When GET "/loads/load-9999" is requested
    Then the problem is "load-not-found" with status 404
    When GET "/loads/load-9999/packages" is requested
    Then the problem is "load-not-found" with status 404
