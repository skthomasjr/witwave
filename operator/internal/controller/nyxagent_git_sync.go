/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"crypto/sha1" //nolint:gosec // SHA-1 is used for short, non-cryptographic volume-name disambiguation — matches the chart's `nyx.gmVolumeName` helper so operator-rendered pods carry the same volume names chart-rendered pods do.
	"encoding/hex"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// Git-sync + git-mapping plumbing (#475).
//
// Mirrors charts/nyx/templates/deployment.yaml + _helpers.tpl so operator-
// rendered agents behave identically to chart-rendered agents:
//
//   - One init container per GitSync (one-time clone) plus one sidecar per
//     GitSync (long-running --period loop).
//   - A single shared /git emptyDir volume holds every repo, each under its
//     own /git/<name> subdirectory.
//   - A git-map-init init container runs the chart's git-sync.sh script once
//     to populate the per-mapping emptyDir volumes before the workload
//     containers start. The same script is wired as git-sync's
//     --exechook-command so mappings are refreshed on every subsequent pull.
//   - Per-mapping emptyDir volumes are mounted into both the workload
//     container at the declared `dest` and every git-sync container so the
//     exechook can rsync into them.
//   - The chart-provided git-sync ConfigMap (containing the script) is
//     reconciled by the operator so there is exactly one copy regardless of
//     install path (#475 comment "mount the chart-supplied ConfigMap rather
//     than inlining a second copy" — honoured by reconciling a CM whose
//     `data` is byte-identical to the chart's).

const (
	// gitSyncDataVolume is the shared /git emptyDir mounted into every
	// git-sync init/sidecar + every workload container that declares a
	// GitMapping. Matches the chart volume name so pod-spec diffs
	// between the two rendering paths stay readable.
	gitSyncDataVolume = "git-sync-data"
	gitSyncMountPath  = "/git"

	// gitScriptMountPath is where the git-sync.sh helper script lives
	// inside the git-map-init + sidecar containers. Matches the chart.
	gitScriptMountPath = "/nyx-scripts"

	// gitMappingsMountPath is the root where per-context mapping TSV
	// files are mounted. Matches the chart: `/nyx-mappings/agent` for
	// the agent-scoped mappings, `/nyx-mappings/<backend>` for each
	// backend-scoped mapping set. The script iterates `*/mappings.tsv`
	// so adding or removing entries doesn't require editing the script.
	gitMappingsMountPath = "/nyx-mappings"

	// gitSyncImageRepository is the default git-sync image repository
	// used when a GitSyncSpec omits Image. Matches the chart's
	// gitSync.image.repository default.
	gitSyncImageRepository = "ghcr.io/skthomasjr/images/git-sync"

	// gitSyncScriptCMSuffix is the ConfigMap name suffix holding the
	// rsync helper script. One CM per NyxAgent keeps the owner
	// reference 1:1 with the CR so GC cleans it up without cross-CR
	// reasoning.
	gitSyncScriptCMSuffix = "git-sync-script"

	// gitMappingsCMSuffix is the suffix for the agent-scoped mappings
	// ConfigMap. Backend-scoped mappings append `-<backend>` before
	// this suffix so multiple CMs coexist under one agent.
	gitMappingsCMSuffix = "git-mappings"

	// gitScriptMapKey is the file name the script lives under inside
	// the git-sync-script CM. Matches the chart.
	gitScriptMapKey = "git-sync.sh"
)

// gitSyncScript is the rsync helper script rendered into the git-sync-script
// ConfigMap. Byte-identical to charts/nyx/templates/configmap-git-mappings.yaml
// so the two rendering paths produce the same runtime behaviour (#475 comment
// "reuse the chart's existing git-sync-script so behaviour matches byte-for-byte").
const gitSyncScript = `#!/bin/sh
set -e
MAPPINGS_DIR="${1:-/nyx-mappings}"
# Failure marker path (risk #584): when any mapping fails, we drop a
# timestamped marker file here so that a readiness probe or sidecar check
# can surface the failure externally. The exechook itself only logs to
# stderr, which is invisible outside ` + "`" + `kubectl logs -c git-sync-<name>` + "`" + `.
# Consumers: mount an emptyDir at /var/run/git-sync shared between the
# git-sync sidecar and the harness container, and have the harness
# readiness endpoint fail if this file exists.
FAILURE_MARKER="/var/run/git-sync/failed"
failed=0
apply_mappings() {
  MAPPINGS_FILE="$1"
  # mappings.tsv format: one mapping per line, "<src>\t<dest>" (tab-separated).
  # Emitted by Helm at render time so shell never has to parse YAML. Tab is
  # used as the delimiter because ':' and whitespace are legal in paths but
  # tabs effectively never are; this removes the substring-match ambiguity
  # of the previous line-based parser (see risk #577).
  while IFS="$(printf '\t')" read -r src dest; do
    if [ -z "$src" ] && [ -z "$dest" ]; then
      continue
    fi
    if [ -z "$src" ] || [ -z "$dest" ]; then
      echo "ERROR: malformed mapping line (src='$src' dest='$dest') in $MAPPINGS_FILE" >&2
      failed=1
      continue
    fi
    case "$src" in
      */)
        echo "Syncing dir  $src -> $dest"
        if ! mkdir -p "$dest"; then
          echo "ERROR: failed to create dir $dest" >&2
          failed=1
        elif ! rsync -a --omit-dir-times --no-perms --delete --exclude='*.checkpoint' "$src" "$dest"; then
          echo "ERROR: failed to sync dir $src -> $dest" >&2
          failed=1
        fi
        ;;
      *)
        echo "Syncing file $src -> $dest"
        if ! mkdir -p "$(dirname "$dest")" || ! rsync -a --omit-dir-times --no-perms "$src" "$dest"; then
          echo "ERROR: failed to sync file $src -> $dest" >&2
          failed=1
        fi
        ;;
    esac
  done < "$MAPPINGS_FILE"
}
for f in "$MAPPINGS_DIR"/*/mappings.tsv; do
  [ -f "$f" ] || continue
  echo "Applying mappings from $f"
  apply_mappings "$f"
done
if [ "$failed" -ne 0 ]; then
  echo "ERROR: one or more mappings failed" >&2
  # Write failure marker (risk #584). Best-effort: if the marker
  # directory isn't mounted we still exit 1 so the pod-level signal is
  # preserved. Timestamp is ISO-8601 UTC to make the file self-describing.
  if mkdir -p "$(dirname "$FAILURE_MARKER")" 2>/dev/null; then
    date -u +"%Y-%m-%dT%H:%M:%SZ" > "$FAILURE_MARKER" 2>/dev/null || true
  fi
  exit 1
fi
# Clear any stale failure marker from a previous failed run so that
# readiness can recover once mappings succeed again.
rm -f "$FAILURE_MARKER" 2>/dev/null || true
`

// hasGitMappings reports whether the agent or any of its enabled backends
// declared any git-mappings. The git-map-init container, mapping CMs, and
// per-mapping emptyDir volumes are only rendered when this is true.
func hasGitMappings(agent *nyxv1alpha1.NyxAgent) bool {
	if len(agent.Spec.GitMappings) > 0 {
		return true
	}
	for _, b := range agent.Spec.Backends {
		if !backendEnabled(b) {
			continue
		}
		if len(b.GitMappings) > 0 {
			return true
		}
	}
	return false
}

// gmVolumeName mirrors the chart's `nyx.gmVolumeName` helper so operator-
// rendered pods and chart-rendered pods share identical per-mapping volume
// names. A sha1 prefix of the dest path disambiguates entries whose paths
// differ only in separators (#573 in chart). Capped at the DNS-1123 label
// limit (63 chars).
func gmVolumeName(agentName, context, dest string) string {
	sum := sha1.Sum([]byte(dest)) //nolint:gosec // not a security boundary, see package comment.
	hash := hex.EncodeToString(sum[:])[:10]
	name := fmt.Sprintf("gm-%s-%s-%s", agentName, context, hash)
	if len(name) > 63 {
		name = name[:63]
	}
	// Trim a trailing '-' that could result from the 63-char cap so the
	// final name stays a valid DNS-1123 label.
	for len(name) > 0 && name[len(name)-1] == '-' {
		name = name[:len(name)-1]
	}
	return name
}

// gitSyncScriptCMName is the per-agent ConfigMap holding the rsync helper
// script. One CM per NyxAgent so OwnerReferences GC works without
// cross-CR reasoning.
func gitSyncScriptCMName(agent *nyxv1alpha1.NyxAgent) string {
	return fmt.Sprintf("%s-%s", agent.Name, gitSyncScriptCMSuffix)
}

// gitMappingsCMName returns the ConfigMap name holding TSV mappings for the
// given context. context is "" for the agent-scoped CM (matches the chart's
// "agent" label under /nyx-mappings) or a backend name for a per-backend CM.
func gitMappingsCMName(agent *nyxv1alpha1.NyxAgent, backendName string) string {
	if backendName == "" {
		return fmt.Sprintf("%s-%s", agent.Name, gitMappingsCMSuffix)
	}
	return fmt.Sprintf("%s-%s-%s", agent.Name, backendName, gitMappingsCMSuffix)
}

// gitSyncImage resolves the container image for a GitSyncSpec, falling back
// to the built-in default when unset. Mirrors the chart's `nyx.gitSyncImage`
// helper — per-entry override takes precedence.
func gitSyncImage(entry nyxv1alpha1.GitSyncSpec, appVersion string) string {
	if entry.Image != nil && entry.Image.Repository != "" {
		return imageRef(*entry.Image, appVersion)
	}
	return imageRef(nyxv1alpha1.ImageSpec{Repository: gitSyncImageRepository}, appVersion)
}

// buildGitSyncScriptConfigMap returns the per-agent ConfigMap carrying the
// rsync helper script, or nil when the agent has no mappings (the script
// wouldn't be referenced by any container). The content is static and
// matches the chart's script byte-for-byte.
func buildGitSyncScriptConfigMap(agent *nyxv1alpha1.NyxAgent) *corev1.ConfigMap {
	if !hasGitMappings(agent) {
		return nil
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitSyncScriptCMName(agent),
			Namespace: agent.Namespace,
			Labels:    agentLabels(agent),
		},
		Data: map[string]string{
			gitScriptMapKey: gitSyncScript,
		},
	}
}

// buildGitMappingsConfigMaps returns every mappings ConfigMap the agent's
// spec currently calls for — one per non-empty context (agent + each
// backend with mappings). Each CM exposes a `mappings.tsv` key formatted
// identically to the chart's output so the script (which ships the same
// loop) reads it without modification.
func buildGitMappingsConfigMaps(agent *nyxv1alpha1.NyxAgent) []*corev1.ConfigMap {
	var out []*corev1.ConfigMap
	if len(agent.Spec.GitMappings) > 0 {
		out = append(out, buildMappingsCM(agent, "", agent.Spec.GitMappings))
	}
	// Sort backends by name so rendering is deterministic.
	backends := append([]nyxv1alpha1.BackendSpec(nil), agent.Spec.Backends...)
	sort.Slice(backends, func(i, j int) bool { return backends[i].Name < backends[j].Name })
	for _, b := range backends {
		if !backendEnabled(b) {
			continue
		}
		if len(b.GitMappings) == 0 {
			continue
		}
		out = append(out, buildMappingsCM(agent, b.Name, b.GitMappings))
	}
	return out
}

// buildMappingsCM renders one mappings ConfigMap. The TSV body contains one
// line per mapping: `<src>\t<dest>`. src is anchored at `/git/<gitSync>/<src>`
// so the script can rsync without knowing which repo the file came from.
func buildMappingsCM(agent *nyxv1alpha1.NyxAgent, backendName string, mappings []nyxv1alpha1.GitMappingSpec) *corev1.ConfigMap {
	var tsv string
	for _, m := range mappings {
		tsv += fmt.Sprintf("/git/%s/%s\t%s\n", m.GitSync, m.Src, m.Dest)
	}
	labels := agentLabels(agent)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitMappingsCMName(agent, backendName),
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			"mappings.tsv": tsv,
		},
	}
}

// gitSyncVolumes returns the pod-level Volumes needed for the agent's
// git-sync plumbing: the shared /git emptyDir, the script CM volume, one CM
// volume per mappings CM, and one emptyDir per unique (context, dest) pair.
// Emits nothing when the agent has no GitSyncs.
func gitSyncVolumes(agent *nyxv1alpha1.NyxAgent) []corev1.Volume {
	if len(agent.Spec.GitSyncs) == 0 {
		return nil
	}
	var vols []corev1.Volume
	vols = append(vols, corev1.Volume{
		Name: gitSyncDataVolume,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
	if !hasGitMappings(agent) {
		return vols
	}
	// Script CM volume (defaultMode 0755 so /bin/sh can exec it).
	mode := int32(0o755)
	vols = append(vols, corev1.Volume{
		Name: gitSyncScriptCMName(agent),
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: gitSyncScriptCMName(agent)},
				DefaultMode:          &mode,
			},
		},
	})
	// Agent-scoped mappings CM + its per-mapping emptyDirs.
	if len(agent.Spec.GitMappings) > 0 {
		vols = append(vols, corev1.Volume{
			Name: gitMappingsCMName(agent, ""),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: gitMappingsCMName(agent, "")},
				},
			},
		})
		for _, m := range agent.Spec.GitMappings {
			vols = append(vols, corev1.Volume{
				Name: gmVolumeName(agent.Name, "agent", m.Dest),
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			})
		}
	}
	// Per-backend mapping volumes — iterate in spec order for determinism
	// after sorting backends.
	backends := append([]nyxv1alpha1.BackendSpec(nil), agent.Spec.Backends...)
	sort.Slice(backends, func(i, j int) bool { return backends[i].Name < backends[j].Name })
	for _, b := range backends {
		if !backendEnabled(b) || len(b.GitMappings) == 0 {
			continue
		}
		vols = append(vols, corev1.Volume{
			Name: gitMappingsCMName(agent, b.Name),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: gitMappingsCMName(agent, b.Name)},
				},
			},
		})
		for _, m := range b.GitMappings {
			vols = append(vols, corev1.Volume{
				Name: gmVolumeName(agent.Name, b.Name, m.Dest),
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			})
		}
	}
	return vols
}

// gitMappingScriptMounts returns the VolumeMounts shared by every git-sync
// container that needs to exec the helper script: the shared /git data
// volume, the script CM, each mappings CM, and every per-mapping emptyDir.
// Mirrors the chart's `nyx.gitMappingMounts` helper.
func gitMappingScriptMounts(agent *nyxv1alpha1.NyxAgent) []corev1.VolumeMount {
	var mounts []corev1.VolumeMount
	mounts = append(mounts, corev1.VolumeMount{
		Name:      gitSyncDataVolume,
		MountPath: gitSyncMountPath,
	})
	if !hasGitMappings(agent) {
		return mounts
	}
	mounts = append(mounts, corev1.VolumeMount{
		Name:      gitSyncScriptCMName(agent),
		MountPath: gitScriptMountPath,
	})
	if len(agent.Spec.GitMappings) > 0 {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      gitMappingsCMName(agent, ""),
			MountPath: fmt.Sprintf("%s/agent", gitMappingsMountPath),
		})
		for _, m := range agent.Spec.GitMappings {
			mounts = append(mounts, corev1.VolumeMount{
				Name:      gmVolumeName(agent.Name, "agent", m.Dest),
				MountPath: m.Dest,
			})
		}
	}
	backends := append([]nyxv1alpha1.BackendSpec(nil), agent.Spec.Backends...)
	sort.Slice(backends, func(i, j int) bool { return backends[i].Name < backends[j].Name })
	for _, b := range backends {
		if !backendEnabled(b) || len(b.GitMappings) == 0 {
			continue
		}
		mounts = append(mounts, corev1.VolumeMount{
			Name:      gitMappingsCMName(agent, b.Name),
			MountPath: fmt.Sprintf("%s/%s", gitMappingsMountPath, b.Name),
		})
		for _, m := range b.GitMappings {
			mounts = append(mounts, corev1.VolumeMount{
				Name:      gmVolumeName(agent.Name, b.Name, m.Dest),
				MountPath: m.Dest,
			})
		}
	}
	return mounts
}

// gitSyncInitContainers returns one init container per GitSync (running a
// one-time --one-time clone) and, when any GitMapping exists, a final
// git-map-init container that runs the rsync helper once before the
// workload containers start. The workload containers can assume mapping
// targets are populated.
func gitSyncInitContainers(agent *nyxv1alpha1.NyxAgent, appVersion string) []corev1.Container {
	if len(agent.Spec.GitSyncs) == 0 {
		return nil
	}
	var inits []corev1.Container
	for _, gs := range agent.Spec.GitSyncs {
		args := []string{
			fmt.Sprintf("--repo=%s", gs.Repo),
			fmt.Sprintf("--root=%s", gitSyncMountPath),
			"--one-time",
		}
		if gs.Ref != "" {
			args = append(args, fmt.Sprintf("--ref=%s", gs.Ref))
		}
		if gs.Depth > 0 {
			args = append(args, fmt.Sprintf("--depth=%d", gs.Depth))
		}
		inits = append(inits, corev1.Container{
			Name:  fmt.Sprintf("git-sync-init-%s", gs.Name),
			Image: gitSyncImage(gs, appVersion),
			Args:  args,
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: boolPtr(false),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
			EnvFrom: gs.EnvFrom,
			VolumeMounts: []corev1.VolumeMount{{
				Name:      gitSyncDataVolume,
				MountPath: gitSyncMountPath,
			}},
		})
	}
	if hasGitMappings(agent) {
		inits = append(inits, corev1.Container{
			Name:    "git-map-init",
			Image:   gitSyncImage(nyxv1alpha1.GitSyncSpec{}, appVersion),
			Command: []string{"/bin/sh", fmt.Sprintf("%s/%s", gitScriptMountPath, gitScriptMapKey)},
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: boolPtr(false),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
			VolumeMounts: gitMappingScriptMounts(agent),
		})
	}
	return inits
}

// gitSyncSidecarContainers returns one long-running sidecar per GitSync.
// When any GitMapping exists, each sidecar wires the helper script as
// git-sync's `--exechook-command` so mappings refresh on every pull.
func gitSyncSidecarContainers(agent *nyxv1alpha1.NyxAgent, appVersion string) []corev1.Container {
	if len(agent.Spec.GitSyncs) == 0 {
		return nil
	}
	mapsPresent := hasGitMappings(agent)
	var sidecars []corev1.Container
	for _, gs := range agent.Spec.GitSyncs {
		period := gs.Period
		if period == "" {
			period = "60s"
		}
		args := []string{
			fmt.Sprintf("--repo=%s", gs.Repo),
			fmt.Sprintf("--root=%s", gitSyncMountPath),
			fmt.Sprintf("--period=%s", period),
		}
		if gs.Ref != "" {
			args = append(args, fmt.Sprintf("--ref=%s", gs.Ref))
		}
		if gs.Depth > 0 {
			args = append(args, fmt.Sprintf("--depth=%d", gs.Depth))
		}
		var mounts []corev1.VolumeMount
		if mapsPresent {
			args = append(args, fmt.Sprintf("--exechook-command=%s/%s", gitScriptMountPath, gitScriptMapKey))
			mounts = gitMappingScriptMounts(agent)
		} else {
			mounts = []corev1.VolumeMount{{
				Name:      gitSyncDataVolume,
				MountPath: gitSyncMountPath,
			}}
		}
		sidecars = append(sidecars, corev1.Container{
			Name:  fmt.Sprintf("git-sync-%s", gs.Name),
			Image: gitSyncImage(gs, appVersion),
			Args:  args,
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: boolPtr(false),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
			EnvFrom:      gs.EnvFrom,
			VolumeMounts: mounts,
		})
	}
	return sidecars
}

// agentGitMappingMounts returns the VolumeMounts the nyx-harness container
// needs: the shared /git volume plus one emptyDir mount per agent-scoped
// GitMapping at the declared `dest`. Per-backend mappings are handled by
// backendGitMappingMounts.
func agentGitMappingMounts(agent *nyxv1alpha1.NyxAgent) []corev1.VolumeMount {
	if len(agent.Spec.GitSyncs) == 0 {
		return nil
	}
	mounts := []corev1.VolumeMount{{
		Name:      gitSyncDataVolume,
		MountPath: gitSyncMountPath,
	}}
	for _, m := range agent.Spec.GitMappings {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      gmVolumeName(agent.Name, "agent", m.Dest),
			MountPath: m.Dest,
		})
	}
	return mounts
}

// backendGitMappingMounts returns the VolumeMounts for a single backend
// container: the shared /git volume plus one emptyDir mount per mapping
// declared on the backend at its `dest`. The shared /git mount lets the
// backend read other repo files directly at /git/<gitSync>/... if needed.
func backendGitMappingMounts(agent *nyxv1alpha1.NyxAgent, b nyxv1alpha1.BackendSpec) []corev1.VolumeMount {
	if len(agent.Spec.GitSyncs) == 0 {
		return nil
	}
	mounts := []corev1.VolumeMount{{
		Name:      gitSyncDataVolume,
		MountPath: gitSyncMountPath,
	}}
	for _, m := range b.GitMappings {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      gmVolumeName(agent.Name, b.Name, m.Dest),
			MountPath: m.Dest,
		})
	}
	return mounts
}
