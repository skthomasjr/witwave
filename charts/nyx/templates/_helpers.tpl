{{/*
Default git-sync image, resolved as repository:tag where tag falls back to
.Chart.AppVersion (matching every other image in this chart). Per-entry
.gitSyncs[].image overrides still take precedence and accept any string.
Backwards-compat: if a user still has the legacy `gitSync.image: <string>`
shape, the string is returned verbatim.
Usage: {{ include "nyx.gitSyncImage" . }}
*/}}
{{- define "nyx.gitSyncImage" -}}
{{- if kindIs "string" .Values.gitSync.image -}}
{{ .Values.gitSync.image }}
{{- else -}}
{{ .Values.gitSync.image.repository }}:{{ .Values.gitSync.image.tag | default .Chart.AppVersion }}
{{- end -}}
{{- end }}

{{/*
Agent component labels (nyx-harness).
Usage: {{- include "nyx.agentLabels" .name | nindent 4 }}
*/}}
{{- define "nyx.agentLabels" -}}
app.kubernetes.io/name: {{ . }}
app.kubernetes.io/component: nyx-harness
app.kubernetes.io/part-of: nyx
app.kubernetes.io/managed-by: helm
{{- end }}

{{/*
Backend component labels.
Usage: {{- include "nyx.backendLabels" (dict "agentName" .name "backendName" .backendName) | nindent 4 }}
*/}}
{{- define "nyx.backendLabels" -}}
app.kubernetes.io/name: {{ .agentName }}
app.kubernetes.io/component: {{ .backendName }}-backend
app.kubernetes.io/part-of: nyx
app.kubernetes.io/managed-by: helm
{{- end }}

{{/*
Generate a git-mapping emptyDir volume name from agent, context, and dest path.
Usage: {{- include "nyx.gmVolumeName" (dict "agentName" $agentName "context" "agent" "dest" .dest) }}
*/}}
{{- define "nyx.gmVolumeName" -}}
gm-{{ .agentName }}-{{ .context }}-{{ .dest | trimPrefix "/home/agent/" | trimPrefix "." | replace "/" "-" | replace "." "-" | trimSuffix "-" }}
{{- end }}

{{/*
Returns true if an agent has any git mappings (agent-level or backend-level).
Usage: {{- if include "nyx.hasMappings" . }}
*/}}
{{- define "nyx.hasMappings" -}}
{{- $has := false -}}
{{- if .gitMappings }}{{- $has = true }}{{- end -}}
{{- range .backends }}
{{- if eq (include "nyx.enabled" .) "true" }}
{{- if .gitMappings }}{{- $has = true }}{{- end }}
{{- end }}
{{- end -}}
{{- if $has }}true{{- end -}}
{{- end }}

{{/*
Git-mapping volume mounts for a given agent — mounts script, mappings ConfigMaps,
and emptyDir destinations. Rendered into git-sync sidecar and git-map-init containers.
Usage: {{- include "nyx.gitMappingMounts" (dict "agent" $agent "release" .Release.Name) | nindent 12 }}
*/}}
{{- define "nyx.gitMappingMounts" -}}
{{- $agentName := .agent.name -}}
{{- $release := .release -}}
- name: {{ $release }}-git-sync-script
  mountPath: /nyx-scripts
{{- if .agent.gitMappings }}
- name: {{ $release }}-{{ $agentName }}-git-mappings
  mountPath: /nyx-mappings/agent
{{- range .agent.gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" "agent" "dest" .dest) }}
  mountPath: {{ .dest }}
{{- end }}
{{- end }}
{{- range .agent.backends }}
{{- if eq (include "nyx.enabled" .) "true" }}
{{- if .gitMappings }}
- name: {{ $release }}-{{ $agentName }}-{{ .name }}-git-mappings
  mountPath: /nyx-mappings/{{ .name }}
{{- $backendName := .name }}
{{- range .gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" $backendName "dest" .dest) }}
  mountPath: {{ .dest }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Git-mapping emptyDir volumes for an agent — one per unique mapped destination.
Usage: {{- include "nyx.gitMappingVolumes" (dict "agent" . "release" $.Release.Name) | nindent 8 }}
*/}}
{{- define "nyx.gitMappingVolumes" -}}
{{- $agentName := .agent.name -}}
{{- $release := .release -}}
- name: {{ $release }}-git-sync-script
  configMap:
    name: {{ $release }}-git-sync-script
    defaultMode: 0755
{{- if .agent.gitMappings }}
- name: {{ $release }}-{{ $agentName }}-git-mappings
  configMap:
    name: {{ $release }}-{{ $agentName }}-git-mappings
{{- range .agent.gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" "agent" "dest" .dest) }}
  emptyDir: {}
{{- end }}
{{- end }}
{{- range .agent.backends }}
{{- if eq (include "nyx.enabled" .) "true" }}
{{- if .gitMappings }}
- name: {{ $release }}-{{ $agentName }}-{{ .name }}-git-mappings
  configMap:
    name: {{ $release }}-{{ $agentName }}-{{ .name }}-git-mappings
{{- $backendName := .name }}
{{- range .gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" $backendName "dest" .dest) }}
  emptyDir: {}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Resolve resources for the nyx-harness container (#553). Order of precedence:
  1. agent.resources (per-agent override in values.yaml)
  2. .Values.defaults.resources.harness (chart-shipped default)
Returns the empty string when neither is set, so the caller can branch on it.
Usage:
  {{- $res := include "nyx.harnessResources" (dict "agent" . "Values" $.Values) }}
  {{- if $res }}
  resources:
    {{- $res | nindent 12 }}
  {{- end }}
*/}}
{{- define "nyx.harnessResources" -}}
{{- $agent := .agent -}}
{{- $Values := .Values -}}
{{- if $agent.resources -}}
{{ toYaml $agent.resources }}
{{- else if and $Values.defaults $Values.defaults.resources $Values.defaults.resources.harness -}}
{{ toYaml $Values.defaults.resources.harness }}
{{- end -}}
{{- end }}

{{/*
Resolve resources for a backend sidecar (#553). Order of precedence:
  1. backend.resources (per-backend override in values.yaml)
  2. .Values.defaults.resources.backends[<backend-name>] (per-backend-type default,
     keyed by backend.name — e.g. "claude", "codex", "gemini")
  3. .Values.defaults.resources.backend (shared fallback for any unknown backend)
Returns the empty string when none of these are set.
Usage:
  {{- $res := include "nyx.backendResources" (dict "backend" . "Values" $.Values) }}
  {{- if $res }}
  resources:
    {{- $res | nindent 12 }}
  {{- end }}
*/}}
{{- define "nyx.backendResources" -}}
{{- $backend := .backend -}}
{{- $Values := .Values -}}
{{- $defaults := dict -}}
{{- if and $Values.defaults $Values.defaults.resources -}}{{- $defaults = $Values.defaults.resources -}}{{- end -}}
{{- if $backend.resources -}}
{{ toYaml $backend.resources }}
{{- else if and $defaults.backends (index $defaults.backends $backend.name) -}}
{{ toYaml (index $defaults.backends $backend.name) }}
{{- else if $defaults.backend -}}
{{ toYaml $defaults.backend }}
{{- end -}}
{{- end }}

{{/*
nyx.enabled — returns "true" or "false" for a scope's `enabled` field,
defaulting to "true" when the key is absent. Use via `eq (include
"nyx.enabled" .) "true"`. This exists because `default true .enabled`
returns "true" even when .enabled is literally false (sprig's `default`
treats the boolean false as an "empty" value). Added in beta.32 for the
per-agent and per-backend enabled flags.
*/}}
{{- define "nyx.enabled" -}}
{{- if hasKey . "enabled" -}}{{- .enabled -}}{{- else -}}true{{- end -}}
{{- end -}}
