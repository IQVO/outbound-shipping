#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are
identical on every pod a release creates (api, mcp). A Service that selects on
those two alone selects ALL of them -- verified live in other fleet charts,
where a Kong request for an api /healthz was answered by the wrong pod. Every
Deployment here carries `app.kubernetes.io/component` in selector.matchLabels
and its pod labels, and every Service selects on it, so each Service selects
EXACTLY ONE Deployment (and its targetPort resolves to a named container port
that Deployment declares). It additionally checks each Deployment renders the
env its binary reads and never the env of the sibling binary (the
chart-vs-binary contract of cmd/api/config.go and cmd/mcp/main.go), with no
duplicates, and that the HTTPRoutes point at an existing Service port.

Run: python3 charts/outbound-shipping/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "outbound-shipping"  # contains the chart name, so fullname == RELEASE

# Dummy values, no password: the chart refuses to render without a database
# source and Kafka brokers. The six consumer groups use the chart defaults.
BASE = [
    "--set", "database.url=postgres://u@example.invalid:5432/db",
    "--set", "kafka.brokers=kafka.example.invalid:9092",
]
ENABLE_EVERYTHING = BASE + [
    "--set", "mcp.enabled=true",
    "--set", "mcp.route.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "config.outboxIntervalMs=500",
    "--set", "config.outboxBatch=50",
    "--set", "config.outboxMaxAttempts=5",
]

# Env each Deployment's binary actually reads. Everything else must NOT appear
# on that Deployment.
# OpenTelemetry (internal/adapters/outbound/telemetry, cmd/api/config.go,
# cmd/mcp/telemetry.go): the chart renders these for EVERY component that runs
# a binary.
OTEL_ENV = {
    "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_SERVICE_NAME", "SERVICE_VERSION", "ENVIRONMENT",
}
# service.name of each Deployment: the mcp one carries a -mcp suffix so the
# two never share a metric series.
WANT_SERVICE_NAME = {RELEASE: "outbound-shipping", f"{RELEASE}-mcp": "outbound-shipping-mcp"}
WANT_ENV: dict[str, set[str]] = {
    RELEASE: {  # cmd/api (cmd/api/config.go) + the migrations DSN + OpenTelemetry
        "ADDR", "LOG_LEVEL", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "KAFKA_BROKERS",
        "FULFILLMENT_CONSUMER_GROUP", "DOCK_CONSUMER_GROUP", "YARD_CONSUMER_GROUP",
        "CPT_CONSUMER_GROUP", "LAYOUT_CONSUMER_GROUP", "ANALYTICS_CONSUMER_GROUP",
        "OUTBOX_INTERVAL_MS", "OUTBOX_BATCH", "OUTBOX_MAX_ATTEMPTS",
    } | OTEL_ENV,
    f"{RELEASE}-mcp": {  # cmd/mcp: no Kafka, no consumers, no outbox
        "MCP_ADDR", "LOG_LEVEL", "DATABASE_URL", "MIGRATIONS_DATABASE_URL",
    } | OTEL_ENV,
}
# Required on the everything-set render (set through values), so a rename in
# values.yaml cannot silently stop rendering a var the binary reads.
REQUIRED_WHEN_SET = {
    RELEASE: WANT_ENV[RELEASE],
    f"{RELEASE}-mcp": WANT_ENV[f"{RELEASE}-mcp"],
}
COMMANDS = {f"{RELEASE}-mcp": ["/app/mcp"]}  # the api uses the image ENTRYPOINT
COMPONENTS = {RELEASE: "api", f"{RELEASE}-mcp": "mcp"}


def render(extra_args: list[str]) -> list[dict]:
    result = subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def container_of(dep: dict) -> dict:
    return dep["spec"]["template"]["spec"]["containers"][0]


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in container_of(dep).get("env", [])]


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}
    routes = [d for d in docs if d.get("kind") == "HTTPRoute"]

    for name, component in COMPONENTS.items():
        if name not in services:
            failures.append(f"the {component} Service {name} was not rendered")
        elif selector_of(services[name]).get("app.kubernetes.io/component") != component:
            failures.append(f"the {component} Service selector must pin component={component}")
        if name not in deployments:
            failures.append(f"the {component} Deployment {name} was not rendered")
    for kind, found in (("Service", services), ("Deployment", deployments)):
        extra = sorted(set(found) - set(COMPONENTS))
        if extra:
            failures.append(f"unexpected {kind} {extra}: outbound-shipping has only api and mcp")

    # Every Deployment's own selector must pin a component too, and be
    # satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")
        container = container_of(dep)
        if container.get("command") != COMMANDS.get(name):
            failures.append(f"Deployment {name} must run command {COMMANDS.get(name)}, got {container.get('command')}")
        env = env_names(dep)
        want = WANT_ENV.get(name)
        if want is None:
            failures.append(f"unexpected Deployment {name}: extend WANT_ENV in this test")
            continue
        stray = set(env) - want
        if stray:
            failures.append(f"Deployment {name} renders env its binary never reads: {sorted(stray)}")
        missing = REQUIRED_WHEN_SET.get(name, set()) - set(env)
        if missing:
            failures.append(f"Deployment {name} does not render {sorted(missing)} on the everything-set render")
        duplicates = sorted({n for n in env if env.count(n) > 1})
        if duplicates:
            failures.append(f"Deployment {name} renders env vars twice: {duplicates}")
        values = {e["name"]: e.get("value") for e in container.get("env", [])}
        if values.get("OTEL_SERVICE_NAME") != WANT_SERVICE_NAME[name]:
            failures.append(f"Deployment {name} OTEL_SERVICE_NAME is {values.get('OTEL_SERVICE_NAME')!r}, want {WANT_SERVICE_NAME[name]!r}")
        if not container.get("securityContext", {}).get("readOnlyRootFilesystem"):
            failures.append(f"Deployment {name} must mount the root filesystem read-only")

    # The real invariant: each Service selects exactly one Deployment, and its
    # targetPort names a container port that Deployment declares.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")
            continue
        ports = {p.get("name") for p in container_of(deployments[hit[0]]).get("ports", [])}
        for port in svc["spec"]["ports"]:
            if port["targetPort"] not in ports:
                failures.append(f"Service {svc_name} targetPort {port['targetPort']} is not a container port of {hit[0]}: {sorted(ports)}")

    # The api Service is 80 -> 8080 ClusterIP, the api container listens on 8080.
    api = services.get(RELEASE)
    if api is not None:
        if api["spec"].get("type") != "ClusterIP" or api["spec"]["ports"][0]["port"] != 80:
            failures.append("the api Service must be ClusterIP on port 80")
        if container_of(deployments[RELEASE])["ports"][0]["containerPort"] != 8080:
            failures.append("the api container must listen on 8080")

    # Every HTTPRoute backend is an existing Service on a port that Service exposes.
    if len(routes) != 2:
        failures.append(f"expected 2 HTTPRoutes (api, mcp) on the everything-set render, got {len(routes)}")
    for route in routes:
        for rule in route["spec"]["rules"]:
            for backend in rule["backendRefs"]:
                svc = services.get(backend["name"])
                if svc is None:
                    failures.append(f"HTTPRoute {route['metadata']['name']} targets missing Service {backend['name']}")
                elif backend["port"] not in [p["port"] for p in svc["spec"]["ports"]]:
                    failures.append(f"HTTPRoute {route['metadata']['name']} targets port {backend['port']} that {backend['name']} does not expose")

    # Optional component and routes stay off the default render.
    default = render(BASE)
    names = [d["metadata"]["name"] for d in default]
    if any(n.endswith("-mcp") for n in names):
        failures.append("the optional mcp component rendered on the default render")
    if any(d.get("kind") == "HTTPRoute" for d in default):
        failures.append("an HTTPRoute rendered on the default render")


def main() -> None:
    failures: list[str] = []
    check_components(render(ENABLE_EVERYTHING), failures)
    if failures:
        for f in failures:
            print(f"FAIL: {f}", file=sys.stderr)
        raise SystemExit(1)
    print("PASS: outbound-shipping Service selectors isolate every component")


if __name__ == "__main__":
    main()
