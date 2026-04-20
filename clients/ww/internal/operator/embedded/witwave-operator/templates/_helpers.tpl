{{/*
Expand the name of the chart.
*/}}
{{- define "witwave-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "witwave-operator.fullname" -}}
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
{{- define "witwave-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "witwave-operator.labels" -}}
helm.sh/chart: {{ include "witwave-operator.chart" . }}
{{ include "witwave-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "witwave-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "witwave-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
witwave-operator.otelEnv — emit OTEL_* env list entries when
observability.tracing is enabled (#634). Paired with the witwave chart's
witwave.otelEnv helper; the operator's Go bootstrap in
operator/internal/tracing/otel.go honours the same env vars.

Usage:
  {{- include "witwave-operator.otelEnv" . | nindent 12 }}
*/}}
{{- define "witwave-operator.otelEnv" -}}
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
{{- define "witwave-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "witwave-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
