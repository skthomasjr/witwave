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

// +kubebuilder:webhook:path=/validate-witwave-ai-v1alpha1-witwaveworkspace,mutating=false,failurePolicy=fail,sideEffects=None,groups=witwave.ai,resources=witwaveworkspaces,verbs=create;update,versions=v1alpha1,name=vwitwaveworkspace.kb.io,admissionReviewVersions=v1

// WitwaveWorkspaceCustomValidator enforces invariants on WitwaveWorkspace objects that
// the structural CRD schema can't express:
//
//   - Volume names unique within Spec.Volumes
//   - In v1alpha1, StorageType must be `pvc` (hostPath is reserved)
//   - AccessMode must be one of ReadWriteMany, ReadWriteOnce, or
//     ReadWriteOncePod. RWM is the cross-node default; RWO + RWOP are
//     accepted as the single-node fallback (Docker Desktop / single-node
//     k3s / etc.) where the cluster's default storage class has no RWM
//     option. Operators picking RWO accept that all binding agent pods
//     must land on the same node.
//   - ConfigFile entries must set exactly one of `configMap` or `inline`
//   - ConfigFile mount-path uniqueness within Spec.ConfigFiles
//   - Secret env / mount-path uniqueness, mutual exclusion of
//     `mountPath` and `envFrom: true` on a single entry
type WitwaveWorkspaceCustomValidator struct{}

var _ webhook.CustomValidator = &WitwaveWorkspaceCustomValidator{}

var witwaveWorkspaceGR = schema.GroupResource{Group: "witwave.ai", Resource: "witwaveworkspaces"}

// ValidateCreate runs the static checks against a freshly-submitted CR.
func (v *WitwaveWorkspaceCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	ws, ok := obj.(*witwavev1alpha1.WitwaveWorkspace)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *WitwaveWorkspace, got %T", obj))
	}
	if err := validateWitwaveWorkspaceSpec(ws); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateUpdate runs the same checks on the new spec; arbitrary
// transitions are allowed provided the new spec is itself valid.
func (v *WitwaveWorkspaceCustomValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	ws, ok := newObj.(*witwavev1alpha1.WitwaveWorkspace)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *WitwaveWorkspace, got %T", newObj))
	}
	if err := validateWitwaveWorkspaceSpec(ws); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *WitwaveWorkspaceCustomValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateWitwaveWorkspaceSpec(ws *witwavev1alpha1.WitwaveWorkspace) error {
	if err := validateWitwaveWorkspaceVolumes(ws); err != nil {
		return err
	}
	if err := validateWitwaveWorkspaceSecrets(ws); err != nil {
		return err
	}
	if err := validateWitwaveWorkspaceConfigFiles(ws); err != nil {
		return err
	}
	return nil
}

func validateWitwaveWorkspaceVolumes(ws *witwavev1alpha1.WitwaveWorkspace) error {
	seen := make(map[string]int, len(ws.Spec.Volumes))
	mountPaths := make(map[string]int, len(ws.Spec.Volumes))
	for i, vol := range ws.Spec.Volumes {
		if prev, dup := seen[vol.Name]; dup {
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.volumes[%d].name %q duplicates spec.volumes[%d].name; volume names must be unique within a WitwaveWorkspace",
				i, vol.Name, prev,
			))
		}
		seen[vol.Name] = i

		// hostPath is reserved for v1.x — reject in v1alpha1 with a
		// pointer at the design doc's "Out of scope for v1" list.
		if vol.StorageType == witwavev1alpha1.WitwaveWorkspaceStorageTypeHostPath {
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.volumes[%d].storageType=hostPath is reserved for a future v1.x; only `pvc` is honoured today (use SharedStorage on the agent for single-node hostPath)",
				i,
			))
		}

		// Access mode: RWM (cross-node default), RWO (single-node
		// fallback), and RWOP (single-pod) are all accepted. Anything
		// else is a typo or an unsupported PV mode.
		mode := vol.AccessMode
		if mode == "" {
			mode = corev1.ReadWriteMany
		}
		switch mode {
		case corev1.ReadWriteMany, corev1.ReadWriteOnce, corev1.ReadWriteOncePod:
			// accepted
		default:
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.volumes[%d].accessMode=%q rejected: must be one of ReadWriteMany, ReadWriteOnce, ReadWriteOncePod",
				i, mode,
			))
		}

		// Mount path uniqueness across declared paths (the controller
		// derives a default when MountPath is empty, so only check
		// explicitly-set paths here).
		if vol.MountPath != "" {
			if prev, dup := mountPaths[vol.MountPath]; dup {
				return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
					"spec.volumes[%d].mountPath %q duplicates spec.volumes[%d].mountPath; mount paths must be unique within a WitwaveWorkspace",
					i, vol.MountPath, prev,
				))
			}
			mountPaths[vol.MountPath] = i
		}
	}
	return nil
}

func validateWitwaveWorkspaceSecrets(ws *witwavev1alpha1.WitwaveWorkspace) error {
	mountPaths := make(map[string]int, len(ws.Spec.Secrets))
	envSeen := make(map[string]int, len(ws.Spec.Secrets))
	for i, sec := range ws.Spec.Secrets {
		if sec.MountPath != "" && sec.EnvFrom {
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.secrets[%d] (name=%q): set exactly one of mountPath or envFrom — they are mutually exclusive on a single entry",
				i, sec.Name,
			))
		}
		if sec.MountPath == "" && !sec.EnvFrom {
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.secrets[%d] (name=%q): must set either mountPath or envFrom — an entry with neither has no effect",
				i, sec.Name,
			))
		}
		if sec.MountPath != "" {
			if prev, dup := mountPaths[sec.MountPath]; dup {
				return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
					"spec.secrets[%d].mountPath %q duplicates spec.secrets[%d].mountPath",
					i, sec.MountPath, prev,
				))
			}
			mountPaths[sec.MountPath] = i
		}
		if sec.EnvFrom {
			if prev, dup := envSeen[sec.Name]; dup {
				return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
					"spec.secrets[%d].name %q duplicates spec.secrets[%d].name with envFrom=true; reference each Secret as envFrom at most once",
					i, sec.Name, prev,
				))
			}
			envSeen[sec.Name] = i
		}
	}
	return nil
}

func validateWitwaveWorkspaceConfigFiles(ws *witwavev1alpha1.WitwaveWorkspace) error {
	mountPaths := make(map[string]int, len(ws.Spec.ConfigFiles))
	for i, cf := range ws.Spec.ConfigFiles {
		hasCM := cf.ConfigMap != ""
		hasInline := cf.Inline != nil
		if hasCM == hasInline { // both set or both unset
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.configFiles[%d]: exactly one of configMap or inline must be set (got configMap=%t, inline=%t)",
				i, hasCM, hasInline,
			))
		}
		if cf.MountPath == "" {
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.configFiles[%d].mountPath: required (must be an absolute path)",
				i,
			))
		}
		if prev, dup := mountPaths[cf.MountPath]; dup {
			return apierrors.NewForbidden(witwaveWorkspaceGR, ws.Name, fmt.Errorf(
				"spec.configFiles[%d].mountPath %q duplicates spec.configFiles[%d].mountPath",
				i, cf.MountPath, prev,
			))
		}
		mountPaths[cf.MountPath] = i
	}
	return nil
}

// SetupWitwaveWorkspaceWebhookWithManager registers the validator with the
// controller-runtime manager. Call this from main.go alongside the
// WitwaveAgent / WitwavePrompt webhook setups.
func SetupWitwaveWorkspaceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&witwavev1alpha1.WitwaveWorkspace{}).
		WithValidator(&WitwaveWorkspaceCustomValidator{}).
		Complete()
}
