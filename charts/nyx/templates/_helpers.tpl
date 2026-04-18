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
Chart label value (used by helm.sh/chart).
Usage: {{ include "nyx.chart" . }}
*/}}
{{- define "nyx.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Agent component labels (harness).
Emits the full Kubernetes Recommended Labels set. NOT suitable for use in
selector.matchLabels (includes `app.kubernetes.io/version` and `helm.sh/chart`,
which change on chart upgrade). For selectors, use `nyx.agentSelectorLabels`.
Usage: {{- include "nyx.agentLabels" (dict "name" .name "root" $) | nindent 4 }}
Legacy (no recommended labels): {{- include "nyx.agentLabels" .name | nindent 4 }}
*/}}
{{- define "nyx.agentLabels" -}}
{{- if kindIs "map" . -}}
{{- $root := .root -}}
helm.sh/chart: {{ include "nyx.chart" $root }}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ $root.Release.Name }}
app.kubernetes.io/component: harness
app.kubernetes.io/part-of: nyx
app.kubernetes.io/managed-by: {{ $root.Release.Service }}
{{- if $root.Chart.AppVersion }}
app.kubernetes.io/version: {{ $root.Chart.AppVersion | quote }}
{{- end }}
{{- else -}}
app.kubernetes.io/name: {{ . }}
app.kubernetes.io/component: harness
app.kubernetes.io/part-of: nyx
app.kubernetes.io/managed-by: helm
{{- end -}}
{{- end }}

{{/*
Agent selector labels — stable across upgrades (safe for selector.matchLabels).
Intentionally omits `app.kubernetes.io/version` and `helm.sh/chart`.
Usage: {{- include "nyx.agentSelectorLabels" .name | nindent 6 }}
*/}}
{{- define "nyx.agentSelectorLabels" -}}
app.kubernetes.io/name: {{ . }}
app.kubernetes.io/component: harness
app.kubernetes.io/part-of: nyx
{{- end }}

{{/*
Backend component labels.
Emits the full Kubernetes Recommended Labels set. NOT suitable for
selector.matchLabels. For selectors, use `nyx.backendSelectorLabels`.
Usage: {{- include "nyx.backendLabels" (dict "agentName" .name "backendName" .backendName "root" $) | nindent 4 }}
Legacy (no recommended labels): {{- include "nyx.backendLabels" (dict "agentName" .name "backendName" .backendName) | nindent 4 }}
*/}}
{{- define "nyx.backendLabels" -}}
{{- $root := .root -}}
{{- if $root -}}
helm.sh/chart: {{ include "nyx.chart" $root }}
app.kubernetes.io/name: {{ .agentName }}
app.kubernetes.io/instance: {{ $root.Release.Name }}
app.kubernetes.io/component: {{ .backendName }}-backend
app.kubernetes.io/part-of: nyx
app.kubernetes.io/managed-by: {{ $root.Release.Service }}
{{- if $root.Chart.AppVersion }}
app.kubernetes.io/version: {{ $root.Chart.AppVersion | quote }}
{{- end }}
{{- else -}}
app.kubernetes.io/name: {{ .agentName }}
app.kubernetes.io/component: {{ .backendName }}-backend
app.kubernetes.io/part-of: nyx
app.kubernetes.io/managed-by: helm
{{- end -}}
{{- end }}

{{/*
Backend selector labels — stable across upgrades (safe for selector.matchLabels).
Usage: {{- include "nyx.backendSelectorLabels" (dict "agentName" .name "backendName" .backendName) | nindent 6 }}
*/}}
{{- define "nyx.backendSelectorLabels" -}}
app.kubernetes.io/name: {{ .agentName }}
app.kubernetes.io/component: {{ .backendName }}-backend
app.kubernetes.io/part-of: nyx
{{- end }}

{{/*
Generate a git-mapping emptyDir volume name from agent, context, and dest path.
The dest is hashed (sha1sum, truncated to 10 chars) rather than slug-translated
so that paths differing only by `/` vs `-` vs `.` produce distinct names (#573).
The final name is capped to 63 chars to satisfy Kubernetes' DNS-1123 label limit.
Usage: {{- include "nyx.gmVolumeName" (dict "agentName" $agentName "context" "agent" "dest" .dest) }}
*/}}
{{- define "nyx.gmVolumeName" -}}
{{- $hash := printf "%s" .dest | sha1sum | trunc 10 -}}
{{- printf "gm-%s-%s-%s" .agentName .context $hash | trunc 63 | trimSuffix "-" -}}
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
Resolve resources for the harness container (#553). Order of precedence:
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
nyx.otelEnv — emit OTEL_* env list entries for a container when
observability.tracing is enabled (#634). Renders nothing when tracing is
disabled, so the caller can unconditionally include it.

This chart does NOT deploy a collector — the endpoint must be a
user-provided OTLP target (opentelemetry-operator-managed collector,
direct Jaeger/Tempo/cloud backend, etc.). Matches the idiomatic operator
pattern across Strimzi, cert-manager, Istio, Knative, Argo, Crossplane
and others.

Wiring:
  - OTEL_ENABLED              master toggle read by shared/otel.py +
                              operator/internal/tracing/otel.go
  - OTEL_EXPORTER_OTLP_ENDPOINT  read by the OTel SDK directly; only
                                 emitted when observability.tracing.endpoint
                                 is set
  - OTEL_TRACES_SAMPLER[_ARG] forwarded verbatim when set
  - OTEL_SERVICE_NAME         per-container service name; omitted when
                              the caller doesn't supply one (each
                              backend main.py already derives a sensible
                              default from AGENT_OWNER)

Usage:
  {{- include "nyx.otelEnv" (dict "root" $ "serviceName" (printf "harness-%s" .name)) | nindent 12 }}
*/}}
{{- define "nyx.otelEnv" -}}
{{- $root := .root -}}
{{- $serviceName := .serviceName -}}
{{- $tracing := (((($root.Values).observability)).tracing) -}}
{{- if and $tracing $tracing.enabled -}}
{{- $endpoint := $tracing.endpoint | default "" -}}
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
{{- if $serviceName }}
- name: OTEL_SERVICE_NAME
  value: {{ $serviceName | quote }}
{{- end }}
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

{{/*
nyx.resolveCredentials — unified dev-friendly / production-friendly
credentials resolver used by gitSync (GITSYNC_USERNAME / GITSYNC_PASSWORD)
and by each backend (CLAUDE_CODE_OAUTH_TOKEN, OPENAI_API_KEY, …).

Call site passes a dict with:
  .creds        — the per-entry credentials block (may be empty)
  .default      — the chart-global fallback credentials block (may be empty)
  .secretName   — the name the chart-rendered Secret should use in inline mode
                  (e.g. "bob-claude-credentials", "bob-autonomous-agent-gitsync-credentials")
  .context      — string used in error messages to identify the caller
                  (e.g. "agents[0].backends[0] (bob/claude)")

The helper fails render with {{- fail ... }} when:
  - inline values (username/token or secrets map) are populated without
    acknowledgeInsecureInline=true
  - nothing at all resolves (no existingSecret, no inline, no default)
    AND .required is true

Returns the NAME of the Secret the caller should envFrom (either the
existingSecret or the chart-rendered one). Empty string when no
credentials are needed. Callers use the return to wire envFrom.
*/}}
{{- define "nyx.resolveCredentials" -}}
{{- $creds := .creds | default dict -}}
{{- $def := .default | default dict -}}
{{- $ctx := .context | default "(unknown)" -}}
{{- $existing := "" -}}
{{- if $creds.existingSecret -}}
  {{- $existing = $creds.existingSecret -}}
{{- else if $def.existingSecret -}}
  {{- $existing = $def.existingSecret -}}
{{- end -}}
{{- $inlineUser := or $creds.username $def.username -}}
{{- $inlineTok  := or $creds.token $def.token -}}
{{- $inlineSecs := $creds.secrets | default $def.secrets | default dict -}}
{{- $hasInline  := or (or $inlineUser $inlineTok) (gt (len $inlineSecs) 0) -}}
{{- $ack := or $creds.acknowledgeInsecureInline $def.acknowledgeInsecureInline -}}
{{- if and $existing $hasInline -}}
  {{- /* existingSecret wins; inline is ignored. Emit a NOTES.txt-side warning via .Warnings could go here. */ -}}
{{- end -}}
{{- if $existing -}}
{{- $existing -}}
{{- else if $hasInline -}}
  {{- if not $ack -}}
    {{- fail (printf "charts/nyx: %s has inline credential values set but acknowledgeInsecureInline is false. Inline tokens land in helm release history, `helm get values`, and etcd. Set acknowledgeInsecureInline: true to confirm the risk (dev/smoke only) OR use existingSecret to reference a pre-created Secret (production)." $ctx) -}}
  {{- end -}}
{{- .secretName -}}
{{- else -}}
{{- /* no credentials resolved — empty return means "don't render envFrom" */ -}}
{{- end -}}
{{- end -}}

{{/*
nyx.inlineCredentialData — returns the stringData map for a chart-rendered
Secret in inline mode. Caller handles Secret metadata + passes result into
stringData:. Merges default + entry maps so either/or chart-global vs
per-entry works. gitSync credentials map into GITSYNC_USERNAME /
GITSYNC_PASSWORD keys; backend credentials map open-ended env-var names.
*/}}
{{- define "nyx.inlineCredentialData" -}}
{{- $creds := .creds | default dict -}}
{{- $def := .default | default dict -}}
{{- $kind := .kind -}} {{/* "gitsync" or "backend" */}}
{{- $user := or $creds.username $def.username -}}
{{- $tok := or $creds.token $def.token -}}
{{- $secs := $creds.secrets | default $def.secrets | default dict -}}
{{- $out := dict -}}
{{- if eq $kind "gitsync" -}}
  {{- if $user -}}{{- $_ := set $out "GITSYNC_USERNAME" $user -}}{{- end -}}
  {{- if $tok  -}}{{- $_ := set $out "GITSYNC_PASSWORD" $tok  -}}{{- end -}}
{{- else if eq $kind "backend" -}}
  {{- range $k, $v := $secs -}}
    {{- $_ := set $out $k $v -}}
  {{- end -}}
{{- end -}}
{{- toYaml $out -}}
{{- end -}}

{{/*
nyx.renderGitSyncEnvFrom — emits the envFrom block for a gitSync entry,
resolving credentials via nyx.resolveCredentials. Falls back to the
entry's legacy `envFrom:` list when no credentials/default resolve, so
deployments that haven't adopted the new shape keep working verbatim.

Caller passes:
  .gs        — the single gitSyncs[] entry
  .agent     — the enclosing agent dict (for name)
  .default   — the chart-global gitSync.credentials fallback
  .release   — $.Release.Name

Writes a full `envFrom:` block including the leading key + indentation
when there's anything to emit; nothing otherwise. Caller should NOT
wrap this in `if` — the helper decides internally.
*/}}
{{- define "nyx.renderGitSyncEnvFrom" -}}
{{- $gs := .gs -}}
{{- $agent := .agent -}}
{{- $def := .default | default dict -}}
{{- $creds := $gs.credentials | default dict -}}
{{- $ctx := printf "agent %q gitSync %q" $agent.name $gs.name -}}
{{- $secretName := printf "%s-%s-%s-gitsync-credentials" .release $agent.name $gs.name | trunc 253 -}}
{{- $resolved := include "nyx.resolveCredentials" (dict "creds" $creds "default" $def "secretName" $secretName "context" $ctx) -}}
{{- $legacy := $gs.envFrom -}}
{{- if $resolved }}
envFrom:
  - secretRef:
      name: {{ $resolved | quote }}
{{- else if $legacy }}
envFrom:
{{ toYaml $legacy | indent 2 }}
{{- end -}}
{{- end -}}

{{/*
nyx.renderBackendEnvFrom — same pattern for agents[].backends[] entries.
Inline "secrets:" map (vs gitSync's username/password) is the dev path.
*/}}
{{- define "nyx.renderBackendEnvFrom" -}}
{{- $b := .backend -}}
{{- $agent := .agent -}}
{{- $def := .default | default dict -}}
{{- $creds := $b.credentials | default dict -}}
{{- $ctx := printf "agent %q backend %q" $agent.name $b.name -}}
{{- $secretName := printf "%s-%s-%s-backend-credentials" .release $agent.name $b.name | trunc 253 -}}
{{- $resolved := include "nyx.resolveCredentials" (dict "creds" $creds "default" $def "secretName" $secretName "context" $ctx) -}}
{{- $legacy := $b.envFrom -}}
{{- if $resolved }}
envFrom:
  - secretRef:
      name: {{ $resolved | quote }}
{{- else if $legacy }}
envFrom:
{{ toYaml $legacy | indent 2 }}
{{- end -}}
{{- end -}}

{{/*
nyx.hasInlineCredentials — true when the creds-or-default combo has any
inline secret material set. Used by the credential-secret templates to
decide whether to render a Secret at all.
*/}}
{{- define "nyx.hasInlineCredentials" -}}
{{- $creds := .creds | default dict -}}
{{- $def := .default | default dict -}}
{{- $kind := .kind -}}
{{- if eq $kind "gitsync" -}}
  {{- if or (or $creds.username $def.username) (or $creds.token $def.token) -}}true{{- end -}}
{{- else if eq $kind "backend" -}}
  {{- $secs := $creds.secrets | default $def.secrets | default dict -}}
  {{- if gt (len $secs) 0 -}}true{{- end -}}
{{- end -}}
{{- end -}}
