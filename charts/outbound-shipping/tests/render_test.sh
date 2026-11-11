#!/usr/bin/env bash
# Chart render assertions for outbound-shipping: the real runtime (Postgres and
# Kafka only, six consumer groups from values), every env var the cmd/*
# binaries read is the env the chart renders (and nothing else), the optional
# mcp component stays off the default render, and no component this service
# does not have (projector, reports, sweep, a2a, frontend, relay) is rendered.
# Run: bash charts/outbound-shipping/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=outbound-shipping
# Dummy values, no password: the chart refuses to render without a database
# source and Kafka brokers. None is ever dialled. The consumer groups come from
# the chart defaults (service-prefixed, fixed names).
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092)
everything=("${base[@]}"
  --set mcp.enabled=true --set mcp.route.enabled=true --set gatewayApi.enabled=true
  --set kongPlugins.cors.enabled=true --set kongPlugins.prometheus.enabled=true
  --set kongPlugins.opentelemetry.enabled=true
  --set config.outboxIntervalMs=500 --set config.outboxBatch=50
  --set config.outboxMaxAttempts=5)

fail() { echo "FAIL: $*" >&2; exit 1; }

render() { helm template "$release" "$chart_dir" "$@"; }
env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}

configmap="$(render "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(render "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# The api Deployment (the only workload whose binary reads config.*) must mount
# the chart ConfigMap with envFrom, or a value that is only in the ConfigMap
# silently does nothing.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"

# The mandatory runtime is rendered from values into BOTH the Deployment and
# the ConfigMap; the six groups are the fixed service-prefixed names.
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap does not carry KAFKA_BROKERS"
for kv in 'FULFILLMENT_CONSUMER_GROUP=outbound-shipping-fulfillment' 'DOCK_CONSUMER_GROUP=outbound-shipping-dock' \
  'YARD_CONSUMER_GROUP=outbound-shipping-yard' 'CPT_CONSUMER_GROUP=outbound-shipping-cpt' \
  'LAYOUT_CONSUMER_GROUP=outbound-shipping-layout' 'ANALYTICS_CONSUMER_GROUP=outbound-shipping-analytics'; do
  [[ "$(env_value "$api_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the api Deployment does not render $kv"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$configmap" || fail "the ConfigMap does not carry $kv"
done
[[ "$(env_value "$api_deployment" ADDR)" == ":8080" ]] || fail "ADDR must default to :8080"
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
grep -A5 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' \
  || fail "MIGRATIONS_DATABASE_URL must be an optional secretKeyRef (the binary falls back to DATABASE_URL)"
if grep -qE '^          command:' <<<"$api_deployment"; then fail "the api runs the image ENTRYPOINT (./api): no command"; fi

# The outbox knobs: not rendered while empty (the binary defaults apply),
# rendered into both the Deployment and the ConfigMap when set.
for v in OUTBOX_INTERVAL_MS OUTBOX_BATCH OUTBOX_MAX_ATTEMPTS; do
  if grep -q "$v" <<<"$api_deployment$configmap"; then fail "an empty config value for $v must not render"; fi
done
tuned_cm="$(render "${everything[@]}" --show-only templates/configmap.yaml)"
tuned_dep="$(render "${everything[@]}" --show-only templates/deployment.yaml)"
for kv in OUTBOX_INTERVAL_MS=500 OUTBOX_BATCH=50 OUTBOX_MAX_ATTEMPTS=5; do
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$tuned_cm" || fail "${kv%%=*} is not rendered into the ConfigMap"
  [[ "$(env_value "$tuned_dep" "${kv%%=*}")" == "${kv#*=}" ]] || fail "${kv%%=*} is not rendered into the Deployment"
done

# --- the mcp component ---------------------------------------------------------
render_all="$(render "${everything[@]}")"
default_render="$(render "${base[@]}")"
if grep -q -- '-mcp' <<<"$default_render"; then fail "an mcp resource rendered with default values (mcp.enabled is false)"; fi
if grep -q 'kind: HTTPRoute' <<<"$default_render"; then fail "an HTTPRoute rendered with default values (gatewayApi.enabled is false)"; fi
if render "${everything[@]}" --show-only templates/mcp-deployment.yaml | grep -q 'configMapRef:'; then
  fail "mcp-deployment.yaml must not mount the api ConfigMap"
fi
mcp_dep="$(render "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-deployment.yaml)"
grep -q 'command: \["/app/mcp"\]' <<<"$mcp_dep" || fail "the mcp Deployment must run /app/mcp"
grep -A4 'name: DATABASE_URL$' <<<"$mcp_dep" | grep -q 'secretKeyRef:' || fail "the mcp DATABASE_URL must come from a Secret"
grep -A5 'name: MIGRATIONS_DATABASE_URL$' <<<"$mcp_dep" | grep -q 'optional: true' || fail "the mcp MIGRATIONS_DATABASE_URL must be optional"
[[ "$(env_value "$mcp_dep" MCP_ADDR)" == ":8090" ]] || fail "the mcp Deployment must set MCP_ADDR from mcp.httpAddr"
for stray in KAFKA_BROKERS ADDR FULFILLMENT_CONSUMER_GROUP DOCK_CONSUMER_GROUP YARD_CONSUMER_GROUP CPT_CONSUMER_GROUP \
  LAYOUT_CONSUMER_GROUP ANALYTICS_CONSUMER_GROUP OUTBOX_INTERVAL_MS OUTBOX_BATCH OUTBOX_MAX_ATTEMPTS; do
  if grep -q "name: $stray\$" <<<"$mcp_dep"; then fail "the mcp Deployment must not render $stray (cmd/mcp reads neither Kafka, consumers nor outbox knobs)"; fi
done
if grep -q 'path: /readyz' <<<"$mcp_dep"; then fail "cmd/mcp serves only /healthz: no /readyz probe"; fi
mcp_route="$(render "${everything[@]}" --show-only templates/mcp-route.yaml)"
grep -q 'value: /api/mcp/outbound-shipping' <<<"$mcp_route" || fail "the mcp route must match /api/mcp/outbound-shipping"
grep -q 'replacePrefixMatch: /mcp' <<<"$mcp_route" || fail "the mcp route must rewrite the prefix to /mcp"
grep -q 'port: 8090' <<<"$mcp_route" || fail "the mcp route must target the mcp Service port 8090"
api_route="$(render "${everything[@]}" --show-only templates/httproute.yaml)"
grep -q 'value: /api/outbound-shipping' <<<"$api_route" || fail "the api route must match /api/outbound-shipping"
grep -q 'replacePrefixMatch: /$' <<<"$api_route" || fail "the api route must strip its prefix"
grep -q 'port: 80$' <<<"$api_route" || fail "the api route must target the api Service port 80"
grep -q 'targetPort: http' <<<"$render_all" || fail "the api Service must target the named container port"

# --- components this service does NOT have are never rendered -------------------
for kind in CronJob Job HorizontalPodAutoscaler Ingress; do
  if grep -q "^kind: $kind\$" <<<"$render_all"; then fail "a $kind rendered: this chart has no such component"; fi
done
for absent in -relay -projector -reports -sweep -a2a -frontend; do
  if grep -qE "^  name: .*$absent\$" <<<"$render_all"; then fail "a $absent resource rendered: outbound-shipping has no such binary"; fi
done
if grep -qE '/app/(relay|projector|reports|sweep|a2a)' <<<"$render_all"; then fail "the render runs a binary the image does not have"; fi
deployments="$(grep -c '^kind: Deployment$' <<<"$render_all")"
[[ "$deployments" == 2 ]] || fail "expected exactly 2 Deployments (api, mcp), got $deployments"

# --- chart vs binary: every env var a cmd/* binary reads is rendered ------------
# The authoritative list is what the Go code reads: Getenv/getenv/or/intOr
# literals in cmd/*, the groupVars table of cmd/api/config.go and the Env*
# constants of the postgres adapter (migrations DSN). The everything-set render
# must carry each one, and carry NO other variable (nothing invented).
code_vars="$( { grep -rhoE --include='*.go' --exclude='*_test.go' '(Getenv|getenv|or|intOr)\("[A-Z][A-Z0-9_]+"' "$repo_dir/cmd" \
    | sed -E 's/.*\("([A-Z0-9_]+)"/\1/'
  sed -n '/^var groupVars/,/^}/p' "$repo_dir/cmd/api/config.go" | grep -oE '"[A-Z][A-Z0-9_]+_CONSUMER_GROUP"' | tr -d '"'
  grep -hoE 'Env[A-Za-z]+ += "[A-Z][A-Z0-9_]+"' "$repo_dir/internal/adapters/outbound/postgres/migrate.go" | sed -E 's/.*"([A-Z0-9_]+)"/\1/'
} | sort -u)"
n_code_vars="$(grep -c . <<<"$code_vars")"
[[ "$n_code_vars" -ge 15 ]] || fail "found only $n_code_vars env names in the Go code (want >= 15): the scan is looking in the wrong place"
for var in $code_vars; do
  grep -q "name: $var\$" <<<"$render_all" || fail "the everything-set render does not carry $var, which the Go code reads"
done
rendered_vars="$(grep -E '^\s+- name: [A-Z][A-Z0-9_]+$' <<<"$render_all" | awk '{print $3}' | sort -u)"
for var in $rendered_vars; do
  grep -qx "$var" <<<"$code_vars" || fail "the render sets $var, which no binary reads (invented env)"
done
# ... and every ConfigMap key is read by a binary.
for key in $(grep -E '^  [A-Z][A-Z0-9_]+:' <<<"$tuned_cm" | sed -E 's/^  ([A-Z0-9_]+):.*/\1/'); do
  grep -qx "$key" <<<"$code_vars" || fail "ConfigMap key $key is read by no binary"
done

# --- refusals: the mandatory values name themselves ----------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':\n$err"
}
db=(--set database.url=postgres://u@h/db)
brokers=(--set kafka.brokers=k:9092)
expect_refusal "no database source" "database.url or database.existingSecret" "${brokers[@]}"
expect_refusal "no brokers" "kafka.brokers" "${db[@]}"
expect_refusal "no database source with mcp enabled" "database.url or database.existingSecret" --set mcp.enabled=true "${brokers[@]}"
for kv in fulfillmentConsumerGroup:FULFILLMENT dockConsumerGroup:DOCK yardConsumerGroup:YARD \
  cptConsumerGroup:CPT layoutConsumerGroup:LAYOUT analyticsConsumerGroup:ANALYTICS; do
  expect_refusal "an empty config.${kv%%:*}" "config.${kv%%:*}" "${db[@]}" "${brokers[@]}" --set "config.${kv%%:*}="
  expect_refusal "an empty config.${kv%%:*} names its variable" "${kv#*:}_CONSUMER_GROUP" "${db[@]}" "${brokers[@]}" --set "config.${kv%%:*}="
done

# --- pod hardening and shutdown room (docs/docs/operations/before-first-release.md row 7)
grep -q 'runAsNonRoot: true' <<<"$render_all" || fail "every pod must run as non-root"
grep -q 'readOnlyRootFilesystem: true' <<<"$render_all" || fail "the root filesystem must be mounted read-only (the service writes nothing to disk)"
grep -q 'allowPrivilegeEscalation: false' <<<"$render_all" || fail "privilege escalation must be disabled"
grep -A1 'drop:' <<<"$render_all" | grep -q 'ALL' || fail "all capabilities must be dropped"
grep -q 'terminationGracePeriodSeconds: 45' <<<"$api_deployment" || fail "terminationGracePeriodSeconds must default to 45s"
grep -q 'command: \["sleep", "5"\]' <<<"$api_deployment" || fail "the api must sleep in preStop (the binary has no drain delay)"
expect_refusal "a grace period below the shutdown sequence" "terminationGracePeriodSeconds" "${db[@]}" "${brokers[@]}" --set terminationGracePeriodSeconds=20
grep -q 'path: /readyz' <<<"$api_deployment" || fail "the api readiness probe must use /readyz"
grep -q 'path: /healthz' <<<"$api_deployment" || fail "the api liveness probe must use /healthz"

# --- Kong: cors, opentelemetry, prometheus, and NEVER an auth plugin ------------
kong="$(render "${everything[@]}" --show-only templates/kong-plugins.yaml)"
for p in cors opentelemetry prometheus; do grep -q "^plugin: $p\$" <<<"$kong" || fail "the $p KongPlugin did not render"; done
if grep -qiE '^plugin: (key-auth|jwt|oauth2|basic-auth|hmac-auth|ldap-auth|openid-connect|acl|ip-restriction)' <<<"$kong"; then
  fail "the fleet is unauthenticated: no auth plugin may ever render"
fi
if grep -qE '"\*"' <<<"$kong"; then fail "the CORS origin must be exact, never *"; fi
grep -q 'konghq.com/plugins: "outbound-shipping-cors,outbound-shipping-opentelemetry,outbound-shipping-prometheus"' <<<"$render_all" \
  || fail "the HTTPRoutes must be annotated with the three plugin names"

# --- no in-memory / permissive / log mode is ever rendered ----------------------
if grep -qiE 'value: ?"?(permissive|log|off)"?$' <<<"$render_all"; then fail "the render carries permissive/log/off, which no binary of this service has"; fi
if grep -qiE 'in-memory|silently runs' <<<"$render_all"; then fail "the render mentions an in-memory mode"; fi
if grep -qE 'name: [A-Z_]+_MODE' <<<"$render_all"; then fail "no *_MODE env var exists in this service (the fleet removed them)"; fi

# --- the ci values files (what chart-testing lints) still render -----------------
helm template "$release" "$chart_dir" -f "$chart_dir/ci/default-values.yaml" >/dev/null || fail "ci/default-values.yaml no longer renders"
helm template "$release" "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml" >/dev/null || fail "ci/all-components-values.yaml no longer renders"

echo "PASS: outbound-shipping chart render assertions"
