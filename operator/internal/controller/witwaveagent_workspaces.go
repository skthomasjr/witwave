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
	"context"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// workspaceVolumePodName returns the pod-level Volume name used when
// stamping a Workspace volume onto an agent pod. Stable across reconciles
// so the rendered Deployment converges on the same volume graph.
func workspaceVolumePodName(workspaceName, volumeName string) string {
	return fmt.Sprintf("workspace-%s-%s", workspaceName, volumeName)
}

// workspaceSecretPodName mirrors workspaceVolumePodName for Secret-mounted
// workspace entries.
func workspaceSecretPodName(workspaceName, secretName string) string {
	return fmt.Sprintf("workspace-%s-secret-%s", workspaceName, secretName)
}

// workspaceConfigFilePodName mirrors workspaceVolumePodName for ConfigMap-
// backed workspace entries. The index disambiguates multiple ConfigFile
// entries that reference the same ConfigMap at different mount paths.
func workspaceConfigFilePodName(workspaceName, cmRef string, idx int) string {
	return fmt.Sprintf("workspace-%s-cf-%d-%s", workspaceName, idx, cmRef)
}

// workspaceVolumeMountPath returns the effective MountPath for a workspace
// volume entry, defaulting to /workspaces/<workspace>/<volume> when the CR
// leaves MountPath empty.
func workspaceVolumeMountPath(ws *witwavev1alpha1.Workspace, vol *witwavev1alpha1.WorkspaceVolume) string {
	if vol.MountPath != "" {
		return vol.MountPath
	}
	return fmt.Sprintf("/workspaces/%s/%s", ws.Name, vol.Name)
}

// fetchWorkspacesForAgent loads every Workspace referenced by the agent's
// Spec.WorkspaceRefs. Missing workspaces are skipped silently — the watch
// on Workspace CRs will re-enqueue the agent once the referenced resource
// appears. The returned slice is sorted by workspace name so the rendered
// Deployment is byte-stable across reconciles.
func fetchWorkspacesForAgent(ctx context.Context, c client.Reader, agent *witwavev1alpha1.WitwaveAgent) ([]witwavev1alpha1.Workspace, error) {
	if len(agent.Spec.WorkspaceRefs) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{}
	out := make([]witwavev1alpha1.Workspace, 0, len(agent.Spec.WorkspaceRefs))
	for _, ref := range agent.Spec.WorkspaceRefs {
		if _, dup := seen[ref.Name]; dup {
			continue
		}
		seen[ref.Name] = struct{}{}
		ws := &witwavev1alpha1.Workspace{}
		err := c.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: ref.Name}, ws)
		if apierrors.IsNotFound(err) {
			// Log the missing reference so an operator investigating
			// "why isn't my workspace mounted" sees which refs the
			// reconcile is waiting on. The Workspace watch
			// (witwaveagent_controller.go) re-enqueues the agent on
			// Workspace creation, so this is recoverable without
			// additional intervention.
			logf.FromContext(ctx).Info(
				"workspace ref not found; will re-reconcile when it appears",
				"workspace", ref.Name, "namespace", agent.Namespace, "agent", agent.Name,
			)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get Workspace %q: %w", ref.Name, err)
		}
		out = append(out, *ws)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// stampWorkspacesOnDeployment mutates the rendered Deployment to add every
// participating workspace's Volumes, Secrets, and ConfigFiles onto the
// pod's volume graph and onto every backend container's mounts.
//
// Collision rules (mirrors the design doc's "Resolution rules so adopting
// a workspace never silently shadows a per-agent override" section):
//
//   - Per-agent mount paths win on collision: when a workspace mount path
//     is already claimed by a backend container's existing mount, the
//     workspace mount is skipped silently. The admission webhook handles
//     cross-workspace collisions on the agent side, so cases that reach
//     this function should already be conflict-free.
//   - Workspace pod-level Volumes use a name prefixed with
//     "workspace-<workspace>-" so they cannot collide with per-agent
//     volumes regardless of how the operator chose them.
//
// The harness container is left alone — workspace mounts only land on
// backend containers per the design doc (workspaces are for backend
// collaboration, not harness-side state).
func stampWorkspacesOnDeployment(dep *appsv1.Deployment, workspaces []witwavev1alpha1.Workspace) {
	if dep == nil || len(workspaces) == 0 {
		return
	}
	pod := &dep.Spec.Template.Spec
	existingVols := map[string]struct{}{}
	for _, v := range pod.Volumes {
		existingVols[v.Name] = struct{}{}
	}

	type secretMount struct {
		volumeName string
		mountPath  string
	}
	type secretEnv struct {
		secretName string
	}
	var (
		volumesToAdd  []corev1.Volume
		backendMounts []corev1.VolumeMount
		secretEnvs    []secretEnv
	)

	for i := range workspaces {
		ws := &workspaces[i]

		// Volumes.
		for j := range ws.Spec.Volumes {
			vol := ws.Spec.Volumes[j]
			// Reject hostPath at the stamping layer too — admission
			// already rejects it but defence in depth keeps an old
			// CR (saved before the webhook went live) from silently
			// growing a hostPath mount.
			if vol.StorageType == witwavev1alpha1.WorkspaceStorageTypeHostPath {
				continue
			}
			podVolName := workspaceVolumePodName(ws.Name, vol.Name)
			if _, dup := existingVols[podVolName]; dup {
				continue
			}
			existingVols[podVolName] = struct{}{}
			volumesToAdd = append(volumesToAdd, corev1.Volume{
				Name: podVolName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: WorkspaceVolumePVCName(ws.Name, vol.Name),
					},
				},
			})
			backendMounts = append(backendMounts, corev1.VolumeMount{
				Name:      podVolName,
				MountPath: workspaceVolumeMountPath(ws, &vol),
			})
		}

		// Secrets.
		for _, sec := range ws.Spec.Secrets {
			if sec.EnvFrom {
				secretEnvs = append(secretEnvs, secretEnv{secretName: sec.Name})
				continue
			}
			if sec.MountPath == "" {
				// Neither EnvFrom nor MountPath set — admission
				// rejects this; skip defensively.
				continue
			}
			podVolName := workspaceSecretPodName(ws.Name, sec.Name)
			if _, dup := existingVols[podVolName]; dup {
				continue
			}
			existingVols[podVolName] = struct{}{}
			volumesToAdd = append(volumesToAdd, corev1.Volume{
				Name: podVolName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: sec.Name,
					},
				},
			})
			backendMounts = append(backendMounts, corev1.VolumeMount{
				Name:      podVolName,
				MountPath: sec.MountPath,
				ReadOnly:  true,
			})
		}

		// ConfigFiles.
		for cfIdx, cf := range ws.Spec.ConfigFiles {
			cmRef := cf.ConfigMap
			if cf.Inline != nil {
				cmRef = WorkspaceInlineConfigMapName(ws.Name, cf.Inline.Name)
			}
			if cmRef == "" {
				continue
			}
			podVolName := workspaceConfigFilePodName(ws.Name, cmRef, cfIdx)
			if _, dup := existingVols[podVolName]; dup {
				continue
			}
			existingVols[podVolName] = struct{}{}
			volumesToAdd = append(volumesToAdd, corev1.Volume{
				Name: podVolName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: cmRef},
					},
				},
			})
			mount := corev1.VolumeMount{
				Name:      podVolName,
				MountPath: cf.MountPath,
			}
			if cf.SubPath != "" {
				mount.SubPath = cf.SubPath
			}
			backendMounts = append(backendMounts, mount)
		}
	}

	if len(volumesToAdd) == 0 && len(secretEnvs) == 0 && len(backendMounts) == 0 {
		return
	}
	pod.Volumes = append(pod.Volumes, volumesToAdd...)

	for ci := range pod.Containers {
		c := &pod.Containers[ci]
		// Workspaces project onto backend containers only — the
		// harness container is left untouched. The harness container
		// is always named "harness" by buildDeployment.
		if c.Name == "harness" {
			continue
		}
		for _, m := range backendMounts {
			if mountPathClaimed(c.VolumeMounts, m.MountPath) {
				continue
			}
			c.VolumeMounts = append(c.VolumeMounts, m)
		}
		for _, se := range secretEnvs {
			c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: se.secretName},
				},
			})
		}
	}
}

// mountPathClaimed returns true when any existing VolumeMount in `mounts`
// already targets `path`. Used to enforce per-agent-wins on path collision.
func mountPathClaimed(mounts []corev1.VolumeMount, path string) bool {
	for _, m := range mounts {
		if m.MountPath == path {
			return true
		}
	}
	return false
}
