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

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/witwave-ai/witwave-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "operator-system"

// serviceAccountName created for the project
const serviceAccountName = "operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccount": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			metricsOutput := getMetricsOutput()
			Expect(metricsOutput).To(ContainSubstring(
				"controller_runtime_reconcile_total",
			))
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})

	// WitwaveAgent reconciliation lifecycle (#628).
	//
	// Pairs with the envtest unit coverage added in #627. Where the unit tests
	// cover builder/reconciler logic in milliseconds, this spec exercises the
	// real kind API server end-to-end: apply a minimal WitwaveAgent, wait for the
	// operator to reconcile a Deployment + Service with the right ownerRefs
	// and pod readiness, then delete the WitwaveAgent and assert cascade teardown.
	//
	// The spec skips when kubeconfig / cluster access is unavailable (SKIP_E2E
	// set, or KUBECONFIG/~/.kube/config missing) so developers can run `go
	// test ./test/e2e/...` without a cluster.
	Context("WitwaveAgent reconciliation", func() {
		const (
			witwaveAgentName      = "e2e-witwaveagent"
			witwaveAgentNamespace = "operator-system"
			harnessImage      = "ghcr.io/skthomasjr/images/harness:latest"
			backendImage      = "ghcr.io/skthomasjr/images/claude:latest"
		)

		BeforeEach(func() {
			if os.Getenv("SKIP_E2E") != "" {
				Skip("SKIP_E2E set; skipping WitwaveAgent reconciliation spec")
			}
			if !kubeconfigAvailable() {
				Skip("no KUBECONFIG / ~/.kube/config available; skipping WitwaveAgent reconciliation spec")
			}
		})

		AfterEach(func() {
			// Best-effort cleanup in case a spec aborted mid-way. Ignore
			// errors — the resource may already be gone.
			cmd := exec.Command("kubectl", "delete", "witwaveagent", witwaveAgentName,
				"-n", witwaveAgentNamespace, "--ignore-not-found", "--wait=false")
			_, _ = utils.Run(cmd)
		})

		It("should reconcile a minimal WitwaveAgent to a ready Deployment + Service and cascade-delete", func() {
			By("applying a minimal WitwaveAgent CR")
			manifest := minimalWitwaveAgentManifest(witwaveAgentName, witwaveAgentNamespace, harnessImage, backendImage)
			manifestPath := filepath.Join(os.TempDir(), fmt.Sprintf("%s.yaml", witwaveAgentName))
			Expect(os.WriteFile(manifestPath, []byte(manifest), 0o644)).To(Succeed())
			defer func() { _ = os.Remove(manifestPath) }()

			cmd := exec.Command("kubectl", "apply", "-f", manifestPath)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply WitwaveAgent CR")

			By("waiting for the reconciler to create the agent Deployment")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", witwaveAgentName, "-n", witwaveAgentNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Deployment not yet created by reconciler")
			}, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("asserting the Deployment's ownerReference points at the WitwaveAgent")
			cmd = exec.Command("kubectl", "get", "deployment", witwaveAgentName, "-n", witwaveAgentNamespace,
				"-o", "jsonpath={.metadata.ownerReferences[0].kind}")
			ownerKind, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(ownerKind).To(Equal("WitwaveAgent"), "Deployment should be owned by WitwaveAgent for cascade delete")

			By("waiting for the reconciler to create the agent Service")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "service", witwaveAgentName, "-n", witwaveAgentNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Service not yet created by reconciler")
			}, 1*time.Minute, 2*time.Second).Should(Succeed())

			By("waiting for the Deployment to report available replicas")
			// Image pulls on kind can be slow, so give this a generous budget.
			// We only assert that the Deployment's rollout progresses far enough
			// for status.availableReplicas to become >= 1 — we don't require a
			// real in-cluster image, because the e2e harness doesn't preload
			// ghcr.io/skthomasjr/images/* into kind. If the image can't be
			// pulled, this will time out and the AfterEach dump will surface
			// the ImagePullBackOff event for the debugger.
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", witwaveAgentName, "-n", witwaveAgentNamespace,
					"-o", "jsonpath={.status.availableReplicas}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("1"), "Deployment has no available replicas yet")
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			By("asserting the WitwaveAgent status observedGeneration tracks spec.generation")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "witwaveagent", witwaveAgentName, "-n", witwaveAgentNamespace,
					"-o", "jsonpath={.status.observedGeneration}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty(), "status.observedGeneration should be set after reconcile")
				g.Expect(out).NotTo(Equal("0"), "status.observedGeneration should advance past 0 after reconcile")
			}, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("deleting the WitwaveAgent and waiting for owned resources to be garbage-collected")
			cmd = exec.Command("kubectl", "delete", "witwaveagent", witwaveAgentName, "-n", witwaveAgentNamespace, "--wait=true")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete WitwaveAgent CR")

			By("confirming the Deployment is gone (cascade)")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", witwaveAgentName, "-n", witwaveAgentNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Deployment should be deleted via ownerRef cascade")
			}, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("confirming the Service is gone (cascade)")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "service", witwaveAgentName, "-n", witwaveAgentNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Service should be deleted via ownerRef cascade")
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
		})
	})
})

// kubeconfigAvailable reports whether the test environment has a reachable
// kubeconfig. Used to skip cluster-requiring specs in local `go test` runs
// that aren't paired with a kind cluster.
func kubeconfigAvailable() bool {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		if _, err := os.Stat(kc); err == nil {
			return true
		}
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(home, ".kube", "config")); err == nil {
		return true
	}
	return false
}

// minimalWitwaveAgentManifest renders the smallest WitwaveAgent that passes CRD
// validation: one backend, required image repositories, everything else left
// to kubebuilder defaults. Kept inline so the spec isn't coupled to the
// `config/samples/` fixture (which may evolve independently).
func minimalWitwaveAgentManifest(name, namespace, harnessImage, backendImage string) string {
	return fmt.Sprintf(`apiVersion: witwave.ai/v1alpha1
kind: WitwaveAgent
metadata:
  name: %s
  namespace: %s
spec:
  image:
    repository: %s
  backends:
    - name: claude
      image:
        repository: %s
      model: claude-opus-4-6
`, name, namespace, trimTag(harnessImage), trimTag(backendImage))
}

// trimTag strips a `:tag` suffix from an image reference so the repository
// field of ImageSpec validates (repository is sans-tag; tag is a separate
// optional field).
func trimTag(image string) string {
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == ':' {
			return image[:i]
		}
		if image[i] == '/' {
			break
		}
	}
	return image
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
