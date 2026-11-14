# Derived from use-cases.md ("Conventions": Idempotency-Key on every POST,
# problem+json catalogue, "Every event is two outbox rows"), troubleshooting.md
# ("REST refusals", "Boot": /readyz), CLAUDE.md hard rules 2 and 9 and
# apis/openapi.yaml (limits). The first-answer-wins replay is pinned on the
# stage command; the middleware is the same for every POST.
@bdd
Feature: API conventions - idempotent POSTs, problem documents, probes and event envelopes
  Every POST carries an Idempotency-Key of at most 128 characters; the same
  key and request replays the stored answer, another request under the key
  is refused. Errors are RFC 7807 problems with a stable slug. Every event is
  a CloudEvents message written twice under one id.

  Background:
    Given site "SITE-1" runs the "16:00" cutoff shipped "STDOUT-2DAY"
    And dock zone "DOCK-D" and staging lane "STG-D04-A" exist at site "SITE-1"
    And appointment "appt-2211" is booked at door "D04" for the "16:00" cutoff

  # --- Idempotency-Key ---------------------------------------------------

  Scenario: A POST without an Idempotency-Key is refused
    When a POST is sent to "/missed-load-sweeps" with the body {"site_id":"SITE-1"} and no Idempotency-Key
    Then the problem is "idempotency-key-required" with status 400

  Scenario Outline: A key of 1 to 128 characters is accepted
    When a POST is sent to "/missed-load-sweeps" with the body {"site_id":"SITE-1"} and an Idempotency-Key of <length> characters
    Then the response status is 200

    Examples:
      | length |
      | 1      |
      | 128    |

  Scenario: A key of 129 characters is refused
    When a POST is sent to "/missed-load-sweeps" with the body {"site_id":"SITE-1"} and an Idempotency-Key of 129 characters
    Then the problem is "idempotency-key-required" with status 400
    And the problem detail mentions "128"

  Scenario: The same key and request replays the stored answer and stages nothing twice
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When a POST is sent to "/packages/pkg-1029/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-1029-20261106-req-01"
    Then the response status is 200
    And the response was not replayed from the stored answer
    When a POST is sent to "/packages/pkg-1029/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-1029-20261106-req-01"
    Then the response status is 200
    And the response was replayed from the stored answer
    And the answer is byte-identical to the first one sent with the Idempotency-Key "stage-pkg-1029-20261106-req-01"
    And exactly 1 "PackageStaged" event was published for package "pkg-1029"
    And package "pkg-1029" is at version 2

  Scenario: The same key with another body is refused and runs nothing
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And location "STG-D04-B" is registered in zone "zone-1" with role "Staging"
    And a POST is sent to "/packages/pkg-1029/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-1029-20261106-req-02"
    When a POST is sent to "/packages/pkg-1029/stagings" with the body {"staging_location":"STG-D04-B"} and the Idempotency-Key "stage-pkg-1029-20261106-req-02"
    Then the problem is "idempotency-key-reused" with status 409
    And package "pkg-1029" is staged at "STG-D04-A"

  Scenario: The same key on another path is refused
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    And a POST is sent to "/packages/pkg-1029/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-1029-20261106-req-03"
    When a POST is sent to "/packages/pkg-2055/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-1029-20261106-req-03"
    Then the problem is "idempotency-key-reused" with status 409

  Scenario: A rejected request replays its rejection under the same key, a new key runs again
    When a POST is sent to "/packages/pkg-3001/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-3001-20261106-req-01"
    Then the problem is "package-not-found" with status 404
    Given package "pkg-3001" of order "order-88425" is manifested on the load
    When a POST is sent to "/packages/pkg-3001/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-3001-20261106-req-01"
    Then the problem is "package-not-found" with status 404
    And the response was replayed from the stored answer
    When a POST is sent to "/packages/pkg-3001/stagings" with the body {"staging_location":"STG-D04-A"} and the Idempotency-Key "stage-pkg-3001-20261106-req-02"
    Then the response status is 200
    And package "pkg-3001" is "STAGED"

  # --- bodies and problems ----------------------------------------------

  Scenario Outline: A body the operation cannot read is a bad request
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When a POST is sent to "/packages/pkg-1029/stagings" with the body <body> and the Idempotency-Key "malformed-body-key-0001"
    Then the problem is "bad-request" with status 400
    And package "pkg-1029" is "EXPECTED"

    Examples:
      | case                  | body                                              |
      | unterminated JSON     | {"staging_location":                              |
      | not an object         | []                                                |
      | an unknown field      | {"staging_location":"STG-D04-A","extra":1}        |
      | trailing data         | {"staging_location":"STG-D04-A"} {}               |
      | no body at all        | (empty)                                           |

  Scenario: A problem document names its slug, status, title and the request path
    When GET "/loads/load-9999" is requested
    Then the problem is "load-not-found" with status 404
    And the response field "title" is "Outbound load not found"
    And the response field "instance" is "/loads/load-9999"

  # --- query parameters --------------------------------------------------

  Scenario Outline: A query the route does not accept is refused with its own slug
    When GET "<path>" is requested
    Then the problem is "<slug>" with status 400

    Examples:
      | path                                         | slug                    |
      | /loads?bogus=1                               | unknown-query-parameter |
      | /loads/load-bdd1/packages?bogus=1            | unknown-query-parameter |
      | /reports/load-adherence?bogus=1              | unknown-query-parameter |
      | /loads?state=FLYING                          | invalid-parameter       |
      | /loads/load-bdd1/packages?state=FLYING       | invalid-parameter       |
      | /loads?operating_date=2026-13-45             | invalid-parameter       |
      | /reports/load-adherence?operating_date=06-11-2026 | invalid-parameter  |
      | /loads?limit=0                               | invalid-parameter       |
      | /loads?limit=201                             | invalid-parameter       |
      | /loads?limit=abc                             | invalid-parameter       |
      | /loads/load-bdd1/packages?limit=201          | invalid-parameter       |

  Scenario Outline: A page size of 1 and of 200 is accepted
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When GET "/loads?limit=<limit>" is requested
    Then the response status is 200
    And the response list "items" has 1 item

    Examples:
      | limit |
      | 1     |
      | 200   |

  # --- probes ------------------------------------------------------------

  Scenario: Readiness answers 200 while every dependency is reachable
    When GET "/readyz" is requested
    Then the response status is 200
    And the response field "status" is "ready"

  Scenario: An unreachable dependency makes readiness 503 and names it, while liveness stays 200
    Given the "kafka" dependency is unreachable
    When GET "/readyz" is requested
    Then the problem is "dependency-unavailable" with status 503
    And the problem detail mentions "kafka"
    When GET "/healthz" is requested
    Then the response status is 200

  Scenario: A draining service is not ready while it is still alive
    Given the service starts draining
    When GET "/readyz" is requested
    Then the response status is 503
    And the response field "status" is "not_ready"
    When GET "/healthz" is requested
    Then the response status is 200

  # --- the event envelope ------------------------------------------------

  Scenario: A published event is a CloudEvents message keyed by its aggregate and written twice under one id
    Given package "pkg-1029" of order "order-88421" is manifested on the load
    When the dock operator scans package "pkg-1029" into "STG-D04-A"
    Then the "PackageStaged" event is typed "com.warehouse.wms.outbound-shipping.shippingpackage.PackageStaged" with source "/warehouse/outbound-shipping" and keyed by "pkg-1029"
    And every "PackageStaged" event was also written to the analytics topic under the same id
    And the last "PackageStaged" event has "version" "2"
    And the "LoadPlanned" event is typed "com.warehouse.wms.outbound-shipping.outboundload.LoadPlanned" with source "/warehouse/outbound-shipping" and keyed by "load-bdd1"
    And every "LoadPlanned" event was also written to the analytics topic under the same id
