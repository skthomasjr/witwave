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
// can render the full resource set for a WitwaveAgent spec without
// touching a cluster. These wrappers are pure — they consume only the
// in-memory WitwaveAgent CR, matching controller's build-layer contract,
// and add no new public reconcile surface.

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// BuildDeploymentForPlan renders the agent Deployment as the operator
// would on a fresh install (no prompt bindings; prompts require live
// WitwavePrompt state and aren't available in plan mode).
func BuildDeploymentForPlan(agent *witwavev1alpha1.WitwaveAgent) *appsv1.Deployment {
	return buildDeployment(agent, DefaultImageTag, nil)
}

// BuildServiceForPlan renders the agent Service.
func BuildServiceForPlan(agent *witwavev1alpha1.WitwaveAgent) *corev1.Service {
	return buildService(agent)
}

// BuildConfigMapsForPlan renders every ConfigMap the reconciler would
// apply from spec.config and spec.backends[].config, in the same order
// reconcileConfigMaps processes them.
func BuildConfigMapsForPlan(agent *witwavev1alpha1.WitwaveAgent) []*corev1.ConfigMap {
	var out []*corev1.ConfigMap
	if cm := buildConfigMap(agent, agentConfigMapName(agent, ""), agent.Spec.Config); cm != nil {
		out = append(out, cm)
	}
	for _, b := range agent.Spec.Backends {
		// #1169: honour backendEnabled() here so plan-mode output
		// matches reconcileConfigMaps (line 737-744), which skips
		// disabled backends before emitting their ConfigMap. Without
		// this guard, `plan` promised ConfigMaps that the live
		// reconciler would never write.
		if !backendEnabled(b) {
			continue
		}
		if cm := buildConfigMap(agent, agentConfigMapName(agent, b.Name), b.Config); cm != nil {
			out = append(out, cm)
		}
	}
	return out
}

// BuildBackendPVCsForPlan renders the per-backend PVCs. The second
// return value holds build errors (e.g. unparseable storage.size) so
// callers can surface warnings without aborting the render.
func BuildBackendPVCsForPlan(agent *witwavev1alpha1.WitwaveAgent) ([]*corev1.PersistentVolumeClaim, []*PVCBuildError) {
	return buildBackendPVCs(agent)
}

// BuildSharedStoragePVCForPlan renders the shared-storage PVC if
// spec.sharedStorage is configured. Returns (nil, nil) when no shared
// storage is requested.
func BuildSharedStoragePVCForPlan(agent *witwavev1alpha1.WitwaveAgent) (*corev1.PersistentVolumeClaim, error) {
	return buildSharedStoragePVC(agent)
}

// BuildHPAForPlan renders the HorizontalPodAutoscaler when
// spec.autoscaling.enabled=true; otherwise returns nil.
func BuildHPAForPlan(agent *witwavev1alpha1.WitwaveAgent) *autoscalingv2.HorizontalPodAutoscaler {
	return buildHPA(agent)
}

// BuildPDBForPlan renders the PodDisruptionBudget when spec.pdb is set.
func BuildPDBForPlan(agent *witwavev1alpha1.WitwaveAgent) *policyv1.PodDisruptionBudget {
	return buildPDB(agent)
}

// BuildDashboardConfigMapForPlan renders the dashboard nginx config
// map when spec.dashboard.enabled=true.
func BuildDashboardConfigMapForPlan(agent *witwavev1alpha1.WitwaveAgent) *corev1.ConfigMap {
	return buildDashboardConfigMap(agent)
}

// BuildDashboardDeploymentForPlan renders the dashboard Deployment.
func BuildDashboardDeploymentForPlan(agent *witwavev1alpha1.WitwaveAgent) *appsv1.Deployment {
	return buildDashboardDeployment(agent, DefaultImageTag)
}

// BuildDashboardServiceForPlan renders the dashboard Service.
func BuildDashboardServiceForPlan(agent *witwavev1alpha1.WitwaveAgent) *corev1.Service {
	return buildDashboardService(agent)
}

// BuildPrometheusRuleForParity is exposed so the chart-vs-operator
// PrometheusRule diff check (operator/scripts/check-prometheusrule-parity.sh)
// can render the operator's alert set without standing up a fake
// reconciler. Returns nil when spec.prometheusRule.enabled is false.
func BuildPrometheusRuleForParity(agent *witwavev1alpha1.WitwaveAgent) *unstructured.Unstructured {
	return buildPrometheusRule(agent)
}

// BuildManifestConfigMapForPlan renders the manifest ConfigMap as if
// the agent were the sole team member (plan mode cannot list peer
// CRs). A live team of N members would produce a larger CM.
//
// #1176: the previous signature returned only *corev1.ConfigMap,
// silently discarding any build failure (the internal builder returns
// a body+hash pair whose body can theoretically collapse to nothing
// under pathological input). The signature now returns an explicit
// error so `operator plan` can surface the failure on stderr and exit
// non-zero rather than emitting an incomplete stream. The underlying
// builder does not currently return an error, so we synthesise one
// when the rendered ConfigMap comes back nil — that keeps the contract
// forward-compatible for follow-ups that add real validation.
func BuildManifestConfigMapForPlan(agent *witwavev1alpha1.WitwaveAgent) (*corev1.ConfigMap, error) {
	port := agent.Spec.Port
	if port == 0 {
		port = 8000
	}
	members := []manifestMember{{Name: agent.Name, Port: port}}
	cm, _ := buildManifestConfigMap(agent, []*witwavev1alpha1.WitwaveAgent{agent}, members)
	if cm == nil {
		return nil, fmt.Errorf("build manifest ConfigMap for %s/%s: builder returned nil", agent.Namespace, agent.Name)
	}
	return cm, nil
}
