{{/*
_helpers.tpl — shared template helpers for the security-atlas chart.
Standard helm-create style name/fullname/label helpers plus a few
chart-specific ones for the per-component (atlas / web / nats / minio)
naming and the DSN / secret-name resolution.
*/}}

{{/* Base name, truncated to the 63-char K8s name limit. */}}
{{- define "security-atlas.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. If fullnameOverride is set it wins; otherwise
release-name + chart-name, de-duplicated when the release name already
contains the chart name (the helm-create convention).
*/}}
{{- define "security-atlas.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Chart name + version, for the helm.sh/chart label. */}}
{{- define "security-atlas.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels applied to every object. */}}
{{- define "security-atlas.labels" -}}
helm.sh/chart: {{ include "security-atlas.chart" . }}
{{ include "security-atlas.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Selector labels — the stable subset, never mutated after first apply. */}}
{{- define "security-atlas.selectorLabels" -}}
app.kubernetes.io/name: {{ include "security-atlas.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Per-component selector labels. Pass a dict with `context` (root) and
`component` (e.g. "atlas", "web", "nats", "minio"). Used so each
Deployment / StatefulSet selects only its own pods.
*/}}
{{- define "security-atlas.componentSelectorLabels" -}}
{{- $ctx := .context -}}
app.kubernetes.io/name: {{ include "security-atlas.name" $ctx }}
app.kubernetes.io/instance: {{ $ctx.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* Per-component full labels (common labels + component label). */}}
{{- define "security-atlas.componentLabels" -}}
{{- $ctx := .context -}}
helm.sh/chart: {{ include "security-atlas.chart" $ctx }}
app.kubernetes.io/name: {{ include "security-atlas.name" $ctx }}
app.kubernetes.io/instance: {{ $ctx.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- if $ctx.Chart.AppVersion }}
app.kubernetes.io/version: {{ $ctx.Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ $ctx.Release.Service }}
{{- end -}}

{{/*
Name of the Secret the atlas / web / migration pods read credentials from.
When `secrets.existingSecret` is set the operator pre-created it (the
production path — no inline secrets in values); otherwise the chart renders
one named "<fullname>-secrets" from inline values (the dev convenience path).
*/}}
{{- define "security-atlas.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "security-atlas.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* Name of the non-secret env ConfigMap. */}}
{{- define "security-atlas.configMapName" -}}
{{- printf "%s-config" (include "security-atlas.fullname" .) -}}
{{- end -}}

{{/* In-cluster NATS URL the atlas server connects to. */}}
{{- define "security-atlas.natsUrl" -}}
{{- printf "nats://%s-nats:4222" (include "security-atlas.fullname" .) -}}
{{- end -}}

{{/*
S3 endpoint the atlas server uses for the artifact store. When the bundled
MinIO is enabled, point at the in-cluster MinIO Service; otherwise use the
operator-supplied external endpoint from values.
*/}}
{{- define "security-atlas.s3Endpoint" -}}
{{- if .Values.minio.enabled -}}
{{- printf "http://%s-minio:9000" (include "security-atlas.fullname" .) -}}
{{- else -}}
{{- required "artifacts.s3Endpoint is required when minio.enabled is false" .Values.artifacts.s3Endpoint -}}
{{- end -}}
{{- end -}}

{{/* ServiceAccount name used by the workload pods. */}}
{{- define "security-atlas.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "security-atlas.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
