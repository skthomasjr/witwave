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

// Plan-mode exports (#1111). Thin wrappers over the private buildXxx
// functions so ``operator/cmd/plan`` (and future validator tooling)
// can render the full resource set for a NyxAgent spec without
// touching a cluster. These wrappers are pure — they consume only the
// in-memory NyxAgent CR, matching controller's build-layer contract,
// and add no new public reconcile surface.

import (
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// BuildDeploymentForPlan renders the agent Deployment as the operator
// would on a fresh install (no prompt bindings; prompts require live
// NyxPrompt state and aren't available in plan mode).
func BuildDeploymentForPlan(agent *nyxv1alpha1.NyxAgent) *appsv1.Deployment {
	return buildDeployment(agent, DefaultImageTag, nil)
}

// BuildServiceForPlan renders the agent Service.
func BuildServiceForPlan(agent *nyxv1alpha1.NyxAgent) *corev1.Service {
	return buildService(agent)
}

// BuildConfigMapsForPlan renders every ConfigMap the reconciler would
// apply from spec.config and spec.backends[].config, in the same order
// reconcileConfigMaps processes them.
func BuildConfigMapsForPlan(agent *nyxv1alpha1.NyxAgent) []*corev1.ConfigMap {
	var out []*corev1.ConfigMap
	if cm := buildConfigMap(agent, agentConfigMapName(agent, ""), agent.Spec.Config); cm != nil {
		out = append(out, cm)
	}
	for _, b := range agent.Spec.Backends {
		if cm := buildConfigMap(agent, agentConfigMapName(agent, b.Name), b.Config); cm != nil {
			out = append(out, cm)
		}
	}
	return out
}

// BuildBackendPVCsForPlan renders the per-backend PVCs. The second
// return value holds build errors (e.g. unparseable storage.size) so
// callers can surface warnings without aborting the render.
func BuildBackendPVCsForPlan(agent *nyxv1alpha1.NyxAgent) ([]*corev1.PersistentVolumeClaim, []*PVCBuildError) {
	return buildBackendPVCs(agent)
}

// BuildSharedStoragePVCForPlan renders the shared-storage PVC if
// spec.sharedStorage is configured. Returns (nil, nil) when no shared
// storage is requested.
func BuildSharedStoragePVCForPlan(agent *nyxv1alpha1.NyxAgent) (*corev1.PersistentVolumeClaim, error) {
	return buildSharedStoragePVC(agent)
}

// BuildHPAForPlan renders the HorizontalPodAutoscaler when
// spec.autoscaling.enabled=true; otherwise returns nil.
func BuildHPAForPlan(agent *nyxv1alpha1.NyxAgent) *autoscalingv2.HorizontalPodAutoscaler {
	return buildHPA(agent)
}

// BuildPDBForPlan renders the PodDisruptionBudget when spec.pdb is set.
func BuildPDBForPlan(agent *nyxv1alpha1.NyxAgent) *policyv1.PodDisruptionBudget {
	return buildPDB(agent)
}

// BuildDashboardConfigMapForPlan renders the dashboard nginx config
// map when spec.dashboard.enabled=true.
func BuildDashboardConfigMapForPlan(agent *nyxv1alpha1.NyxAgent) *corev1.ConfigMap {
	return buildDashboardConfigMap(agent)
}

// BuildDashboardDeploymentForPlan renders the dashboard Deployment.
func BuildDashboardDeploymentForPlan(agent *nyxv1alpha1.NyxAgent) *appsv1.Deployment {
	return buildDashboardDeployment(agent, DefaultImageTag)
}

// BuildDashboardServiceForPlan renders the dashboard Service.
func BuildDashboardServiceForPlan(agent *nyxv1alpha1.NyxAgent) *corev1.Service {
	return buildDashboardService(agent)
}

// BuildManifestConfigMapForPlan renders the manifest ConfigMap as if
// the agent were the sole team member (plan mode cannot list peer
// CRs). A live team of N members would produce a larger CM.
func BuildManifestConfigMapForPlan(agent *nyxv1alpha1.NyxAgent) *corev1.ConfigMap {
	port := agent.Spec.Port
	if port == 0 {
		port = 8000
	}
	members := []manifestMember{{Name: agent.Name, Port: port}}
	cm, _ := buildManifestConfigMap(agent, []*nyxv1alpha1.NyxAgent{agent}, members)
	return cm
}
