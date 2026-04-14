{{/*
Common labels applied to all nyx resources.
Usage: {{- include "nyx.labels" .name | nindent 4 }}
*/}}
{{- define "nyx.labels" -}}
app.kubernetes.io/name: {{ . }}
app.kubernetes.io/part-of: nyx
app.kubernetes.io/managed-by: helm
{{- end }}

{{/*
Agent component labels (nyx-agent).
Usage: {{- include "nyx.agentLabels" .name | nindent 4 }}
*/}}
{{- define "nyx.agentLabels" -}}
app.kubernetes.io/name: {{ . }}
app.kubernetes.io/component: nyx-agent
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
gm-{{ .agentName }}-{{ .context }}-{{ .dest | trimPrefix "/home/agent/" | replace "/" "-" | replace "." "" | trimSuffix "-" }}
{{- end }}

{{/*
Returns true if an agent has any git mappings (agent-level or backend-level).
Usage: {{- if include "nyx.hasMappings" . }}
*/}}
{{- define "nyx.hasMappings" -}}
{{- $has := false -}}
{{- if .gitMappings }}{{- $has = true }}{{- end -}}
{{- range .backends }}{{- if .gitMappings }}{{- $has = true }}{{- end }}{{- end -}}
{{- if $has }}true{{- end -}}
{{- end }}

{{/*
Git-mapping volume mounts for a given agent — mounts script, mappings ConfigMaps,
and emptyDir destinations. Rendered into git-sync sidecar and git-map-init containers.
Usage: {{- include "nyx.gitMappingMounts" . | nindent 12 }}
where . is the agent object.
*/}}
{{- define "nyx.gitMappingMounts" -}}
{{- $agentName := .name -}}
- name: nyx-git-sync-script
  mountPath: /nyx-scripts
{{- if .gitMappings }}
- name: nyx-{{ $agentName }}-git-mappings
  mountPath: /nyx-mappings/agent
{{- range .gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" "agent" "dest" .dest) }}
  mountPath: {{ .dest }}
{{- end }}
{{- end }}
{{- range .backends }}
{{- if .gitMappings }}
- name: nyx-{{ $agentName }}-{{ .name }}-git-mappings
  mountPath: /nyx-mappings/{{ .name }}
{{- $backendName := .name }}
{{- range .gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" $backendName "dest" .dest) }}
  mountPath: {{ .dest }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Git-mapping emptyDir volumes for an agent — one per unique mapped destination.
Usage: {{- include "nyx.gitMappingVolumes" . | nindent 8 }}
where . is the agent object.
*/}}
{{- define "nyx.gitMappingVolumes" -}}
{{- $agentName := .name -}}
- name: nyx-git-sync-script
  configMap:
    name: nyx-git-sync-script
{{- if .gitMappings }}
- name: nyx-{{ $agentName }}-git-mappings
  configMap:
    name: nyx-{{ $agentName }}-git-mappings
{{- range .gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" "agent" "dest" .dest) }}
  emptyDir: {}
{{- end }}
{{- end }}
{{- range .backends }}
{{- if .gitMappings }}
- name: nyx-{{ $agentName }}-{{ .name }}-git-mappings
  configMap:
    name: nyx-{{ $agentName }}-{{ .name }}-git-mappings
{{- $backendName := .name }}
{{- range .gitMappings }}
- name: {{ include "nyx.gmVolumeName" (dict "agentName" $agentName "context" $backendName "dest" .dest) }}
  emptyDir: {}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
