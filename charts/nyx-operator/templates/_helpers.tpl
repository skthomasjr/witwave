{{/*
Expand the name of the chart.
*/}}
{{- define "nyx-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "nyx-operator.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "nyx-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "nyx-operator.labels" -}}
helm.sh/chart: {{ include "nyx-operator.chart" . }}
{{ include "nyx-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "nyx-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nyx-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
nyx-operator.otelEnv — emit OTEL_* env list entries when
observability.tracing is enabled (#634). Paired with the nyx chart's
nyx.otelEnv helper; the operator's Go bootstrap in
operator/internal/tracing/otel.go honours the same env vars.

Usage:
  {{- include "nyx-operator.otelEnv" . | nindent 12 }}
*/}}
{{- define "nyx-operator.otelEnv" -}}
{{- $tracing := ((.Values.observability).tracing) -}}
{{- if and $tracing $tracing.enabled -}}
{{- $endpoint := $tracing.endpoint | default "" }}
- name: OTEL_ENABLED
  value: "true"
{{- if $endpoint }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ $endpoint | quote }}
{{- end }}
{{- if $tracing.sampler }}
- name: OTEL_TRACES_SAMPLER
  value: {{ $tracing.sampler | quote }}
{{- end }}
{{- if $tracing.samplerArg }}
- name: OTEL_TRACES_SAMPLER_ARG
  value: {{ $tracing.samplerArg | quote }}
{{- end }}
{{- if $tracing.serviceName }}
- name: OTEL_SERVICE_NAME
  value: {{ $tracing.serviceName | quote }}
{{- end }}
{{- end -}}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "nyx-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "nyx-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
