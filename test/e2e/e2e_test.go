/*
Copyright 2025.

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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/conforma/controller/test/utils"
)

// namespace where the project is deployed in
const namespace = "conforma"

// serviceAccountName created for the project
const serviceAccountName = "conforma-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "conforma-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "conforma-controller-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("ensuring manager namespace exists")
		// Create namespace if it doesn't exist, or ignore if it already exists
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
		}

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("kubectl", "apply", "-f", "test/fixtures/")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("setting up metrics access for e2e tests")
		// Create ClusterRoleBinding for metrics access
		cmd = exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
			"--clusterrole=conforma-controller-metrics-reader",
			fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
		)
		_, err = utils.Run(cmd)
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")
		}

		By("waiting for the metrics endpoint to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating persistent curl-metrics pod for test suite")
		// Get service account token
		token, err := serviceAccountToken()
		Expect(err).NotTo(HaveOccurred(), "Failed to get service account token")
		Expect(token).NotTo(BeEmpty(), "Service account token should not be empty")

		// Clean up any existing curl-metrics pod first
		cmd = exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		//nolint:lll
		// Create the persistent curl-metrics pod for the entire test suite
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
						"args": ["while true; do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics; echo '---METRICS_END---'; sleep 5; done"],
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
		Expect(err).NotTo(HaveOccurred(), "Failed to create persistent curl-metrics pod")

		By("waiting for curl-metrics pod to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
				"-o", "jsonpath={.status.phase}",
				"-n", namespace)
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Running"), "curl-metrics pod should be running")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying metrics accessibility")
		Eventually(func(g Gomega) {
			metricsOutput := getMetricsFromPod()
			g.Expect(metricsOutput).To(ContainSubstring("controller_runtime_reconcile_total"), "Metrics should be accessible")
		}, 30*time.Second, 5*time.Second).Should(Succeed())
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("cleaning up the metrics ClusterRoleBinding")
		cmd = exec.Command("kubectl", "delete", "clusterrolebinding", metricsRoleBindingName, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("waiting for controller deployment to be deleted")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods",
				"-l", "control-plane=controller-manager",
				"-n", namespace,
				"-o", "name",
			)
			output, err := utils.Run(cmd)
			if err != nil {
				// If kubectl fails, assume resources are deleted
				return
			}
			g.Expect(output).To(BeEmpty(), "Controller pods should be deleted")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("uninstalling CRDs")
		cmd = exec.Command("kubectl", "delete", "crds", "--all")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("waiting for namespace to be completely deleted")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "ns", namespace)
			_, err := utils.Run(cmd)
			g.Expect(err).To(HaveOccurred(), "Namespace should be deleted")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("cleaning up temporary token files")
		secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
		tokenRequestFile := filepath.Join("/tmp", secretName)
		_ = os.Remove(tokenRequestFile)
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
				g.Expect(controllerPodName).To(ContainSubstring("manager"))

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
			By("validating that the metrics service is available")
			cmd := exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("verifying that the controller manager is serving the metrics server")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("getting the metrics from the persistent curl-metrics pod")
			metricsOutput := getMetricsFromPod()
			Expect(metricsOutput).To(ContainSubstring(
				"controller_runtime_reconcile_total",
			))
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		It("should not reconcile when PipelineRun is completed but not signed", func() { //nolint:dupl
			By("creating a completed but unsigned PipelineRun")
			pipelineRun := createTestPipelineRun("test-unsigned-pr", false, false)

			By("applying the PipelineRun to the cluster")
			cmd := exec.Command("kubectl", "apply", "-f", "-", "-n", namespace)
			cmd.Stdin = strings.NewReader(pipelineRun)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create unsigned PipelineRun")

			By("waiting a reasonable time to ensure no reconciliation occurs")
			time.Sleep(10 * time.Second)

			By("verifying no TaskRun was created")
			cmd = exec.Command(
				"kubectl", "get", "taskruns",
				"-l", "app.kubernetes.io/created-by=conforma-controller",
				"-n", namespace,
				"-o", "name",
			)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(BeEmpty(), "No TaskRun should be created for unsigned PipelineRun")

			By("verifying metrics show no reconciliation for this PipelineRun")
			metricsOutput := getMetricsFromPod()
			// Should not contain successful reconciliation for pipelinerun controller
			Expect(metricsOutput).To(ContainSubstring(`controller_runtime_reconcile_total{controller="pipelinerun",result="success"} 0`), "Metrics should show 0 successes") //nolint:lll

			By("cleaning up the PipelineRun")
			cmd = exec.Command("kubectl", "delete", "pipelinerun", "test-unsigned-pr",
				"-n", namespace,
				"--ignore-not-found=true",
			)
			_, _ = utils.Run(cmd)
		})

		It("should not reconcile when PipelineRun is completed, signed, and Conforma cli execution is already triggered", func() { //nolint:dupl,lll
			By("creating a completed, signed PipelineRun with Conforma cli execution triggered")
			pipelineRun := createTestPipelineRun("test-conforma-triggered-pr", true, true)

			By("applying the PipelineRun to the cluster")
			cmd := exec.Command("kubectl", "apply", "-f", "-", "-n", namespace)
			cmd.Stdin = strings.NewReader(pipelineRun)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Conforma cli execution triggered PipelineRun")

			By("waiting a reasonable time to ensure no reconciliation occurs")
			time.Sleep(10 * time.Second)

			By("verifying no TaskRun was created")
			cmd = exec.Command(
				"kubectl", "get", "taskruns",
				"-l", "app.kubernetes.io/created-by=conforma-controller",
				"-n", namespace,
				"-o", "name",
			)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(BeEmpty(), "No TaskRun should be created for Conforma cli execution triggered PipelineRun")

			By("verifying metrics show no significant reconciliation activity")
			metricsOutput := getMetricsFromPod()
			// Verify metrics are accessible and controller is running
			Expect(metricsOutput).To(ContainSubstring(`controller_runtime_reconcile_total{controller="pipelinerun",result="success"} 0`), "Metrics should show 0 successes") //nolint:lll

			By("cleaning up the PipelineRun")
			cmd = exec.Command(
				"kubectl", "delete", "pipelinerun", "test-conforma-triggered-pr",
				"-n", namespace,
				"--ignore-not-found=true",
			)
			_, _ = utils.Run(cmd)
		})

		It("should reconcile when PipelineRun is completed, signed, and Conforma cli execution is not triggered", func() {
			By("creating the required ConfigMap for Conforma parameters")
			configMap := createConformaConfigMap()
			cmd := exec.Command("kubectl", "apply", "-f", "-", "-n", namespace)
			cmd.Stdin = strings.NewReader(configMap)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Conforma ConfigMap")

			By("creating a completed and signed PipelineRun without Conforma cli execution triggered")
			pipelineRun := createTestPipelineRun("test-reconcile-pr", true, false)

			By("applying the PipelineRun to the cluster")
			cmd = exec.Command("kubectl", "apply", "-f", "-", "-n", namespace)
			cmd.Stdin = strings.NewReader(pipelineRun)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create reconcilable PipelineRun")

			By("updating the PipelineRun status to mark it as completed")
			statusPatch := `{
				"status": {
					"completionTime": "2024-01-15T10:30:00Z",
					"conditions": [{
						"lastTransitionTime": "2024-01-15T10:30:00Z",
						"message": "Tasks Completed: 1 (Failed: 0, Cancelled 0), Skipped: 0",
						"reason": "Succeeded",
						"status": "True",
						"type": "Succeeded"
					}],
					"results": [{
						"name": "IMAGE_URL",
						"value": "quay.io/example/test-image"
					}, {
						"name": "IMAGE_DIGEST", 
						"value": "sha256:abcd1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
					}],
					"startTime": "2024-01-15T10:25:00Z"
				}
			}`
			cmd = exec.Command("kubectl", "patch", "pipelinerun", "test-reconcile-pr", "-n", namespace,
				"--type=merge", "--subresource=status", "--patch", statusPatch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to update PipelineRun status")

			By("waiting for the controller to reconcile and create a TaskRun")
			var taskRunName string
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "taskruns",
					"-l", "app.kubernetes.io/created-by=conforma-controller",
					"-l", "conforma.dev/pipelinerun-name=test-reconcile-pr",
					"-n", namespace,
					"-o", "go-template={{ range .items }}{{ .metadata.name }}{{ \"\\n\" }}{{ end }}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				taskRuns := utils.GetNonEmptyLines(output)
				g.Expect(taskRuns).To(HaveLen(1), "Expected exactly one TaskRun to be created")
				taskRunName = taskRuns[0]
				g.Expect(taskRunName).To(ContainSubstring("conforma-verify-"), "TaskRun should have correct name prefix")
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("verifying the TaskRun has correct labels and references")
			cmd = exec.Command("kubectl", "get", "taskrun", taskRunName, "-n", namespace, "-o", "json")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			var taskRun map[string]interface{}
			err = json.Unmarshal([]byte(output), &taskRun)
			Expect(err).NotTo(HaveOccurred())

			metadata := taskRun["metadata"].(map[string]interface{})
			labels := metadata["labels"].(map[string]interface{})
			Expect(labels["app.kubernetes.io/created-by"]).To(Equal("conforma-controller"))
			Expect(labels["conforma.dev/pipelinerun-name"]).To(Equal("test-reconcile-pr"))

			By("verifying the PipelineRun is marked as Conforma cli execution triggered")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipelinerun", "test-reconcile-pr", "-n", namespace, "-o", "json")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				var pr map[string]interface{}
				err = json.Unmarshal([]byte(output), &pr)
				g.Expect(err).NotTo(HaveOccurred())

				metadata := pr["metadata"].(map[string]interface{})
				annotations := metadata["annotations"].(map[string]interface{})
				g.Expect(annotations["conforma.dev/triggered-on"]).To(Not(BeEmpty()))
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("verifying reconciliation metrics show successful processing")
			metricsOutput := getMetricsFromPod()
			Expect(metricsOutput).To(ContainSubstring(
				`controller_runtime_reconcile_total{controller="pipelinerun",result="success"} 1`,
			), "Metrics should show successful pipelinerun reconciliation")

			By("cleaning up test resources")
			cmd = exec.Command(
				"kubectl", "delete", "pipelinerun", "test-reconcile-pr",
				"-n", namespace, "--ignore-not-found=true",
			)
			_, _ = utils.Run(cmd)
			cmd = exec.Command(
				"kubectl", "delete", "taskrun", taskRunName,
				"-n", namespace, "--ignore-not-found=true",
			)
			_, _ = utils.Run(cmd)
			cmd = exec.Command(
				"kubectl", "delete", "configmap", "enterprise-contract-conforma-params",
				"-n", namespace, "--ignore-not-found=true",
			)
			_, _ = utils.Run(cmd)
		})
	})
})

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

	// Ensure cleanup of temporary file
	defer func() {
		_ = os.Remove(tokenRequestFile)
	}()

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

// getMetricsFromPod retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsFromPod() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace, "--tail=300")
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")

	// Find the most recent complete HTTP response
	// Look for the latest "< HTTP/1.1 200 OK" and get everything until the next "---METRICS_END---"
	lines := strings.Split(metricsOutput, "\n")
	var latestResponse []string
	lastHTTPIndex := -1

	// Find the last occurrence of HTTP response start
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "< HTTP/1.1 200 OK") {
			lastHTTPIndex = i
			break
		}
	}

	if lastHTTPIndex >= 0 {
		// Collect from the HTTP response start until the end marker
		for i := lastHTTPIndex; i < len(lines); i++ {
			latestResponse = append(latestResponse, lines[i])
			if strings.Contains(lines[i], "---METRICS_END---") {
				break
			}
		}

		if len(latestResponse) > 0 {
			result := strings.Join(latestResponse, "\n")
			if strings.Contains(result, "< HTTP/1.1 200 OK") {
				return result
			}
		}
	}

	// Fallback: return full output if it contains HTTP response
	if strings.Contains(metricsOutput, "< HTTP/1.1 200 OK") {
		return metricsOutput
	}

	// If no HTTP response found, fail the test
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"), "Metrics output should contain HTTP 200 response")
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}

// createTestPipelineRun creates a PipelineRun YAML for testing (without status, since kubectl apply ignores it)
func createTestPipelineRun(name string, signed bool, conformaTriggered bool) string {
	annotations := make(map[string]string)
	if signed {
		annotations["chains.tekton.dev/signed"] = "true"
	}
	if conformaTriggered {
		annotations["conforma.dev/triggered-on"] = time.Now().Format(time.RFC3339)
	}

	// Convert annotations to YAML format
	annotationsYAML := ""
	if len(annotations) > 0 {
		annotationsYAML = "  annotations:\n"
		for key, value := range annotations {
			annotationsYAML += fmt.Sprintf("    %s: \"%s\"\n", key, value)
		}
	}

	return fmt.Sprintf(`apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: %s
  namespace: %s
%sspec:
  pipelineSpec:
    tasks:
    - name: build-task
      taskSpec:
        steps:
        - name: echo
          image: registry.redhat.io/ubi8/ubi-minimal:latest
          script: echo "build complete"
`, name, namespace, annotationsYAML)
}

// createConformaConfigMap creates the ConfigMap YAML that the controller expects
func createConformaConfigMap() string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: enterprise-contract-conforma-params
  namespace: %s
data:
  GIT_URL: "https://github.com/conforma/cli"
  GIT_REVISION: "main"
  GIT_PATH: "tasks/verify-enterprise-contract/0.1/verify-enterprise-contract.yaml"
  IGNORE_REKOR: "false"
  TIMEOUT: "10m"
  WORKERS: "1"
  POLICY_CONFIGURATION: |
    {
      "name": "Default policy",
      "description": "Default Conforma policy configuration",
      "publicKey": "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE...\n-----END PUBLIC KEY-----"
    }
  PUBLIC_KEY: |
    -----BEGIN PUBLIC KEY-----
    MFkwEwYHKoZIzj0CAQYIKoZIz0DAQcDQgAE...
    -----END PUBLIC KEY-----
`, namespace)
}
