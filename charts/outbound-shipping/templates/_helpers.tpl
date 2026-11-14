{{/*
Expand the name of the chart.
*/}}
{{- define "outbound-shipping.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "outbound-shipping.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "outbound-shipping.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "outbound-shipping.labels" -}}
helm.sh/chart: {{ include "outbound-shipping.chart" . }}
{{ include "outbound-shipping.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Shared by every pod this release creates, so every
Deployment and Service ALSO pins app.kubernetes.io/component (api, mcp): a
Service selecting on these two alone would select both components.
*/}}
{{- define "outbound-shipping.selectorLabels" -}}
app.kubernetes.io/name: {{ include "outbound-shipping.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "outbound-shipping.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "outbound-shipping.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "outbound-shipping.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "outbound-shipping.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Fully qualified name of the MCP server deployment/service (cmd/mcp).
*/}}
{{- define "outbound-shipping.mcpFullname" -}}
{{- include "outbound-shipping.fullname" . }}-mcp
{{- end }}

{{/*
Image reference shared by both workloads (api and mcp): the image carries
/app/api and /app/mcp.
*/}}
{{- define "outbound-shipping.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
MANDATORY: the service runs on real Postgres only. cmd/api and cmd/mcp exit at
boot without DATABASE_URL; there is no in-memory mode. Fails the render naming
the values instead of crash-looping.
*/}}
{{- define "outbound-shipping.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "outbound-shipping requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: the service runs on real Kafka only. cmd/api exits at boot without
KAFKA_BROKERS (the outbox relay, the six consumers and /readyz all need it).
There is no default broker and no way to switch Kafka off.
*/}}
{{- define "outbound-shipping.requireKafka" -}}
{{- $_ := required "outbound-shipping requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- end -}}

{{/*
The six consumers always run inside cmd/api, each under a STABLE consumer group
id that must come from values (cmd/api exits at boot without
FULFILLMENT_/DOCK_/YARD_/CPT_/LAYOUT_/ANALYTICS_CONSUMER_GROUP). A stable id is
what lets a rescheduled pod resume from its committed offsets.
*/}}
{{- define "outbound-shipping.requireConsumerGroups" -}}
{{- if not .Values.config.fulfillmentConsumerGroup -}}
{{- fail "config.fulfillmentConsumerGroup is required (FULFILLMENT_CONSUMER_GROUP): the fulfillment consumer (warehouse.fulfillment.events) always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE id." -}}
{{- end -}}
{{- if not .Values.config.dockConsumerGroup -}}
{{- fail "config.dockConsumerGroup is required (DOCK_CONSUMER_GROUP): the dock-scheduling consumer (warehouse.dock-scheduling.events) always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE id." -}}
{{- end -}}
{{- if not .Values.config.yardConsumerGroup -}}
{{- fail "config.yardConsumerGroup is required (YARD_CONSUMER_GROUP): the yard-management consumer (warehouse.yard-management.events) always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE id." -}}
{{- end -}}
{{- if not .Values.config.cptConsumerGroup -}}
{{- fail "config.cptConsumerGroup is required (CPT_CONSUMER_GROUP): the process-path-management consumer (warehouse.process-path-management.events) always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE id." -}}
{{- end -}}
{{- if not .Values.config.layoutConsumerGroup -}}
{{- fail "config.layoutConsumerGroup is required (LAYOUT_CONSUMER_GROUP): the facility-layout consumer (warehouse.facility.events) always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE id." -}}
{{- end -}}
{{- if not .Values.config.analyticsConsumerGroup -}}
{{- fail "config.analyticsConsumerGroup is required (ANALYTICS_CONSUMER_GROUP): the projector consumer (warehouse.outbound-shipping.analytics) always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE id." -}}
{{- end -}}
{{- end -}}

{{/*
Shutdown room (docs/docs/operations/runbook.md, Shutdown): the binary flips
/readyz, stops the consumers and the relay (each waits for its in-flight
handler), then drains HTTP for up to 10 s, with no drain delay of its own. The
preStop sleep runs BEFORE SIGTERM, so terminationGracePeriodSeconds must cover
preStopSleepSeconds + 10 s of HTTP drain + 10 s for a consumer handler, or a
rolling update SIGKILLs a pod mid-drain.
*/}}
{{- define "outbound-shipping.requireShutdownBudget" -}}
{{- $need := add (int .Values.config.preStopSleepSeconds) 20 -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) $need -}}
{{- fail (printf "terminationGracePeriodSeconds is %d but the binary's shutdown sequence needs at least %d (preStop sleep %ds + HTTP drain 10s + a consumer handler 10s); a rolling update would SIGKILL a pod mid-drain" (int .Values.terminationGracePeriodSeconds) $need (int .Values.config.preStopSleepSeconds)) -}}
{{- end -}}
{{- end -}}

{{/*
service.version of every component: serviceVersion, else the image tag, else
the chart appVersion.
*/}}
{{- define "outbound-shipping.serviceVersion" -}}
{{- .Values.serviceVersion | default .Values.image.tag | default .Chart.AppVersion -}}
{{- end -}}

{{/*
otel.enabled needs an endpoint and a service name: an empty OTLP endpoint would
silently switch the SDK off in the binary, which is what otel.enabled=false is
for.
*/}}
{{- define "outbound-shipping.requireOtel" -}}
{{- if .Values.otel.enabled -}}
{{- if not .Values.otel.endpoint -}}
{{- fail "otel.endpoint is required while otel.enabled is true (OTEL_EXPORTER_OTLP_ENDPOINT); set otel.enabled=false to run without telemetry." -}}
{{- end -}}
{{- if not .Values.otel.serviceName -}}
{{- fail "otel.serviceName is required while otel.enabled is true (OTEL_SERVICE_NAME)." -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The telemetry variables the chart owns, rendered into the env of EVERY
component Deployment that runs a binary. Call with (dict "root" . "suffix" "")
for the api and (dict "root" . "suffix" "-mcp") for mcp: the suffix keeps the
two service.name values (and so their metric series) apart.
*/}}
{{- define "outbound-shipping.telemetryEnv" -}}
- name: ENVIRONMENT
  value: {{ .root.Values.environment | quote }}
- name: SERVICE_VERSION
  value: {{ include "outbound-shipping.serviceVersion" .root | quote }}
{{- if .root.Values.otel.enabled }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ .root.Values.otel.endpoint | quote }}
- name: OTEL_SERVICE_NAME
  value: {{ printf "%s%s" .root.Values.otel.serviceName .suffix | quote }}
{{- end }}
{{- end -}}

{{/*
extraEnv / mcp.extraEnv must not set a variable this chart owns: a second
entry for the same name breaks ArgoCD's diff and one of the two silently
loses. Call with (dict "env" <list> "where" <values key>).
*/}}
{{- define "outbound-shipping.requireOwnedEnv" -}}
{{- $where := .where -}}
{{- range .env -}}
{{- if has .name (list "OTEL_EXPORTER_OTLP_ENDPOINT" "OTEL_SERVICE_NAME" "SERVICE_VERSION" "ENVIRONMENT") -}}
{{- fail (printf "%s sets %s, which the chart owns: use otel.enabled / otel.endpoint / otel.serviceName / serviceVersion / environment instead" $where .name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment (always rendered) includes it, so a mis-set release fails
`helm template` / `helm install` whichever component is enabled.
*/}}
{{- define "outbound-shipping.requireRealRuntime" -}}
{{- include "outbound-shipping.requireDatabase" . -}}
{{- include "outbound-shipping.requireKafka" . -}}
{{- include "outbound-shipping.requireConsumerGroups" . -}}
{{- include "outbound-shipping.requireShutdownBudget" . -}}
{{- include "outbound-shipping.requireOtel" . -}}
{{- include "outbound-shipping.requireOwnedEnv" (dict "env" .Values.extraEnv "where" "extraEnv") -}}
{{- include "outbound-shipping.requireOwnedEnv" (dict "env" .Values.mcp.extraEnv "where" "mcp.extraEnv") -}}
{{- end -}}

{{/*
Comma-joined names of this chart's enabled KongPlugins, for the
konghq.com/plugins annotation on every HTTPRoute this chart renders.
Empty string (annotation omitted) when none are enabled: standalone
installs without Kong are unaffected.
*/}}
{{- define "outbound-shipping.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "outbound-shipping.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end }}
