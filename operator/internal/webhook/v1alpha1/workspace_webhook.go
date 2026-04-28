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

package v1alpha1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// +kubebuilder:webhook:path=/validate-witwave-ai-v1alpha1-workspace,mutating=false,failurePolicy=fail,sideEffects=None,groups=witwave.ai,resources=workspaces,verbs=create;update,versions=v1alpha1,name=vworkspace.kb.io,admissionReviewVersions=v1

// WorkspaceCustomValidator enforces invariants on Workspace objects that
// the structural CRD schema can't express:
//
//   - Volume names unique within Spec.Volumes
//   - In v1alpha1, StorageType must be `pvc` (hostPath is reserved)
//   - In v1alpha1, AccessMode must be ReadWriteMany (RWO + RWOP rejected
//     with a clear pointer at the v1.x roadmap item)
//   - ConfigFile entries must set exactly one of `configMap` or `inline`
//   - ConfigFile mount-path uniqueness within Spec.ConfigFiles
//   - Secret env / mount-path uniqueness, mutual exclusion of
//     `mountPath` and `envFrom: true` on a single entry
type WorkspaceCustomValidator struct{}

var _ webhook.CustomValidator = &WorkspaceCustomValidator{}

var workspaceGR = schema.GroupResource{Group: "witwave.ai", Resource: "workspaces"}

// ValidateCreate runs the static checks against a freshly-submitted CR.
func (v *WorkspaceCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	ws, ok := obj.(*witwavev1alpha1.Workspace)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *Workspace, got %T", obj))
	}
	if err := validateWorkspaceSpec(ws); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateUpdate runs the same checks on the new spec; arbitrary
// transitions are allowed provided the new spec is itself valid.
func (v *WorkspaceCustomValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	ws, ok := newObj.(*witwavev1alpha1.Workspace)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *Workspace, got %T", newObj))
	}
	if err := validateWorkspaceSpec(ws); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *WorkspaceCustomValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateWorkspaceSpec(ws *witwavev1alpha1.Workspace) error {
	if err := validateWorkspaceVolumes(ws); err != nil {
		return err
	}
	if err := validateWorkspaceSecrets(ws); err != nil {
		return err
	}
	if err := validateWorkspaceConfigFiles(ws); err != nil {
		return err
	}
	return nil
}

func validateWorkspaceVolumes(ws *witwavev1alpha1.Workspace) error {
	seen := make(map[string]int, len(ws.Spec.Volumes))
	mountPaths := make(map[string]int, len(ws.Spec.Volumes))
	for i, vol := range ws.Spec.Volumes {
		if prev, dup := seen[vol.Name]; dup {
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.volumes[%d].name %q duplicates spec.volumes[%d].name; volume names must be unique within a Workspace",
				i, vol.Name, prev,
			))
		}
		seen[vol.Name] = i

		// hostPath is reserved for v1.x — reject in v1alpha1 with a
		// pointer at the design doc's "Out of scope for v1" list.
		if vol.StorageType == witwavev1alpha1.WorkspaceStorageTypeHostPath {
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.volumes[%d].storageType=hostPath is reserved for a future v1.x; only `pvc` is honoured today (use SharedStorage on the agent for single-node hostPath)",
				i,
			))
		}

		// Access mode: only RWM in v1.
		mode := vol.AccessMode
		if mode == "" {
			mode = corev1.ReadWriteMany
		}
		if mode != corev1.ReadWriteMany {
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.volumes[%d].accessMode=%q rejected: v1 hard-requires ReadWriteMany; RWO single-node fallback is tracked for v1.x",
				i, mode,
			))
		}

		// Mount path uniqueness across declared paths (the controller
		// derives a default when MountPath is empty, so only check
		// explicitly-set paths here).
		if vol.MountPath != "" {
			if prev, dup := mountPaths[vol.MountPath]; dup {
				return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
					"spec.volumes[%d].mountPath %q duplicates spec.volumes[%d].mountPath; mount paths must be unique within a Workspace",
					i, vol.MountPath, prev,
				))
			}
			mountPaths[vol.MountPath] = i
		}
	}
	return nil
}

func validateWorkspaceSecrets(ws *witwavev1alpha1.Workspace) error {
	mountPaths := make(map[string]int, len(ws.Spec.Secrets))
	envSeen := make(map[string]int, len(ws.Spec.Secrets))
	for i, sec := range ws.Spec.Secrets {
		if sec.MountPath != "" && sec.EnvFrom {
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.secrets[%d] (name=%q): set exactly one of mountPath or envFrom — they are mutually exclusive on a single entry",
				i, sec.Name,
			))
		}
		if sec.MountPath == "" && !sec.EnvFrom {
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.secrets[%d] (name=%q): must set either mountPath or envFrom — an entry with neither has no effect",
				i, sec.Name,
			))
		}
		if sec.MountPath != "" {
			if prev, dup := mountPaths[sec.MountPath]; dup {
				return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
					"spec.secrets[%d].mountPath %q duplicates spec.secrets[%d].mountPath",
					i, sec.MountPath, prev,
				))
			}
			mountPaths[sec.MountPath] = i
		}
		if sec.EnvFrom {
			if prev, dup := envSeen[sec.Name]; dup {
				return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
					"spec.secrets[%d].name %q duplicates spec.secrets[%d].name with envFrom=true; reference each Secret as envFrom at most once",
					i, sec.Name, prev,
				))
			}
			envSeen[sec.Name] = i
		}
	}
	return nil
}

func validateWorkspaceConfigFiles(ws *witwavev1alpha1.Workspace) error {
	mountPaths := make(map[string]int, len(ws.Spec.ConfigFiles))
	for i, cf := range ws.Spec.ConfigFiles {
		hasCM := cf.ConfigMap != ""
		hasInline := cf.Inline != nil
		if hasCM == hasInline { // both set or both unset
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.configFiles[%d]: exactly one of configMap or inline must be set (got configMap=%t, inline=%t)",
				i, hasCM, hasInline,
			))
		}
		if cf.MountPath == "" {
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.configFiles[%d].mountPath: required (must be an absolute path)",
				i,
			))
		}
		if prev, dup := mountPaths[cf.MountPath]; dup {
			return apierrors.NewForbidden(workspaceGR, ws.Name, fmt.Errorf(
				"spec.configFiles[%d].mountPath %q duplicates spec.configFiles[%d].mountPath",
				i, cf.MountPath, prev,
			))
		}
		mountPaths[cf.MountPath] = i
	}
	return nil
}

// SetupWorkspaceWebhookWithManager registers the validator with the
// controller-runtime manager. Call this from main.go alongside the
// WitwaveAgent / WitwavePrompt webhook setups.
func SetupWorkspaceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&witwavev1alpha1.Workspace{}).
		WithValidator(&WorkspaceCustomValidator{}).
		Complete()
}
