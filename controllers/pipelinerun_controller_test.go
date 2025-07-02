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

package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("PipelineRun Controller", func() {
	const (
		PipelineRunName      = "test-pipelinerun"
		PipelineRunNamespace = "default"
		timeout              = time.Second * 10
		interval             = time.Millisecond * 250
	)

	var configMap *corev1.ConfigMap

	BeforeEach(func() {
		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "conforma-controller-conforma-params",
				Namespace: PipelineRunNamespace,
			},
			Data: map[string]string{
				"GIT_URL":              "https://github.com/conforma/cli",
				"GIT_REVISION":         "main",
				"GIT_PATH":             "tasks/verify-enterprise-contract/0.1/verify-enterprise-contract.yaml",
				"IGNORE_REKOR":         "true",
				"TIMEOUT":              "60m",
				"WORKERS":              "1",
				"POLICY_CONFIGURATION": "github.com/enterprise-contract/config//slsa3",
				"PUBLIC_KEY":           "k8s://enterprise-contract/public-key",
			},
		}
		Expect(k8sClient.Create(context.Background(), configMap)).Should(Succeed())
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(context.Background(), configMap)).Should(Succeed())

		// Clean up any PipelineRuns that might have been created
		pipelineRunList := &pipelinev1.PipelineRunList{}
		Expect(k8sClient.List(
			context.Background(), pipelineRunList, client.InNamespace(PipelineRunNamespace),
		)).Should(Succeed())
		for i := range pipelineRunList.Items {
			pr := &pipelineRunList.Items[i]
			Expect(k8sClient.Delete(context.Background(), pr)).Should(Succeed())
		}
	})

	Context("When checking PipelineRun conditions", func() {
		It("Should detect a signed and succeeded (with a Conforma cli execution triggered) PipelineRun", func() {
			By("Creating a signed and succeeded PipelineRun")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName,
					Namespace: PipelineRunNamespace,
					Annotations: map[string]string{
						AnnotationSigned:              "true",
						AnnotationConformaTriggeredOn: time.Now().Format(time.RFC3339),
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status using the status subresource
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   apis.ConditionSucceeded,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Checking if the PipelineRun is detected as succeeded")
			Expect(isSucceededPipelineRun(pipelineRun)).Should(BeTrue())

			By("Checking if the PipelineRun is detected as signed")
			Expect(isSignedPipelineRun(pipelineRun)).Should(BeTrue())

			By("Checking if the PipelineRun is detected having a Conforma cli execution triggered")
			Expect(isConformaTriggered(pipelineRun)).Should(BeTrue())
		})

		It("Should detect a signed and succeeded (without Conforma cli execution triggered) PipelineRun", func() {
			By("Creating a signed and succeeded PipelineRun")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName,
					Namespace: PipelineRunNamespace,
					Annotations: map[string]string{
						AnnotationSigned: "true",
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status using the status subresource
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   apis.ConditionSucceeded,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Checking if the PipelineRun is detected as succeeded")
			Expect(isSucceededPipelineRun(pipelineRun)).Should(BeTrue())

			By("Checking if the PipelineRun is detected as signed")
			Expect(isSignedPipelineRun(pipelineRun)).Should(BeTrue())

			By("Checking if the PipelineRun is detected not having a Conforma cli execution triggered")
			Expect(isConformaTriggered(pipelineRun)).Should(BeFalse())
		})

		It("Should not detect an unsigned PipelineRun", func() {
			By("Creating an unsigned but succeeded PipelineRun")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName,
					Namespace: PipelineRunNamespace,
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status using the status subresource
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   apis.ConditionSucceeded,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Checking if the PipelineRun is detected as succeeded")
			Expect(isSucceededPipelineRun(pipelineRun)).Should(BeTrue())

			By("Checking if the PipelineRun is detected as not signed")
			Expect(isSignedPipelineRun(pipelineRun)).Should(BeFalse())

			By("Checking if the PipelineRun is detected not having a Conforma cli execution triggered")
			Expect(isConformaTriggered(pipelineRun)).Should(BeFalse())
		})

		It("Should not detect a failed PipelineRun", func() {
			By("Creating a signed but failed PipelineRun")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName,
					Namespace: PipelineRunNamespace,
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status using the status subresource
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   apis.ConditionSucceeded,
							Status: corev1.ConditionFalse,
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Checking if the PipelineRun is detected as not succeeded")
			Expect(isSucceededPipelineRun(pipelineRun)).Should(BeFalse())

			By("Checking if the PipelineRun is detected as not signed")
			Expect(isSignedPipelineRun(pipelineRun)).Should(BeFalse())

			By("Checking if the PipelineRun is detected not having a Conforma cli execution triggered")
			Expect(isConformaTriggered(pipelineRun)).Should(BeFalse())
		})
	})

	Context("When handling Conforma cli execution", func() {
		It("Should detect a PipelineRun with Conforma cli execution triggered", func() {
			By("Creating a PipelineRun with Conforma cli execution triggered annotation")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName + "-conforma-triggered",
					Namespace: PipelineRunNamespace,
					Annotations: map[string]string{
						AnnotationConformaTriggeredOn: time.Now().Format(time.RFC3339),
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			By("Checking if the PipelineRun is detected as Conforma cli execution triggered")
			Expect(isConformaTriggered(pipelineRun)).Should(BeTrue())
		})

		It("Should mark a PipelineRun as Conforma cli execution triggered", func() {
			By("Creating a PipelineRun without annotation for Conforma cli execution")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName + "-mark-conforma-triggered",
					Namespace: PipelineRunNamespace,
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			By("Marking the PipelineRun as Conforma cli execution triggered")
			reconciler := &PipelineRunReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			Expect(reconciler.markConformaTriggered(context.Background(), pipelineRun)).Should(Succeed())

			By("Verifying the PipelineRun has the Conforma cli execution triggered annotation")
			updatedPipelineRun := &pipelinev1.PipelineRun{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{
				Name:      PipelineRunName + "-mark-conforma-triggered",
				Namespace: PipelineRunNamespace,
			}, updatedPipelineRun)).Should(Succeed())
			Expect(updatedPipelineRun.Annotations[AnnotationConformaTriggeredOn]).Should(Equal(time.Now().Format(time.RFC3339)))
		})
	})

	Context("When reconciling PipelineRuns", func() {
		It("Should process a signed and succeeded PipelineRun", func() {
			By("Creating a signed and succeeded PipelineRun")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName + "-reconcile",
					Namespace: PipelineRunNamespace,
					Annotations: map[string]string{
						AnnotationSigned: "true",
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status using the status subresource
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   apis.ConditionSucceeded,
							Status: "True",
						},
					},
				},
				PipelineRunStatusFields: pipelinev1.PipelineRunStatusFields{
					Results: []pipelinev1.PipelineRunResult{
						{
							Name:  "IMAGE_URL",
							Value: *pipelinev1.NewStructuredValues("quay.io/test/image:latest"),
						},
						{
							Name:  "IMAGE_DIGEST",
							Value: *pipelinev1.NewStructuredValues("sha256:1234567890abcdef"),
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Reconciling the PipelineRun")
			reconciler := &PipelineRunReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      PipelineRunName + "-reconcile",
					Namespace: PipelineRunNamespace,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the PipelineRun has been processed")
			updatedPipelineRun := &pipelinev1.PipelineRun{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{
				Name:      PipelineRunName + "-reconcile",
				Namespace: PipelineRunNamespace,
			}, updatedPipelineRun)).Should(Succeed())
			Expect(updatedPipelineRun.Annotations[AnnotationConformaTriggeredOn]).Should(Equal(time.Now().Format(time.RFC3339)))
		})
	})

	Context("When triggering Conforma verification", func() {
		It("Should create a TaskRun with correct parameters", func() {
			By("Creating a PipelineRun with required results")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName + "-conforma",
					Namespace: PipelineRunNamespace,
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status with results
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				PipelineRunStatusFields: pipelinev1.PipelineRunStatusFields{
					Results: []pipelinev1.PipelineRunResult{
						{
							Name:  "IMAGE_URL",
							Value: *pipelinev1.NewStructuredValues("quay.io/test/image:latest"),
						},
						{
							Name:  "IMAGE_DIGEST",
							Value: *pipelinev1.NewStructuredValues("sha256:1234567890abcdef"),
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Reconciling the PipelineRun")
			reconciler := &PipelineRunReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      PipelineRunName + "-conforma",
					Namespace: PipelineRunNamespace,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the TaskRun was created with correct parameters")
			taskRunList := &pipelinev1.TaskRunList{}
			Expect(k8sClient.List(context.Background(), taskRunList, client.InNamespace(PipelineRunNamespace))).Should(Succeed())

			var foundTaskRun *pipelinev1.TaskRun
			for i := range taskRunList.Items {
				tr := &taskRunList.Items[i]
				if tr.Labels[AnnotationConformaPipelineRun] == pipelineRun.Name {
					foundTaskRun = tr
					break
				}
			}
			Expect(foundTaskRun).ToNot(BeNil(), "Expected to find a TaskRun for the PipelineRun")
			taskRun := *foundTaskRun
			Expect(taskRun.Labels["app.kubernetes.io/created-by"]).To(Equal("conforma-controller"))
			Expect(taskRun.Labels[AnnotationConformaPipelineRun]).To(Equal(pipelineRun.Name))
			Expect(string(taskRun.Spec.TaskRef.Resolver)).To(Equal("git"))
			Expect(taskRun.Spec.TaskRef.ResolverRef.Params).To(ContainElements(
				pipelinev1.Param{
					Name: "url",
					Value: pipelinev1.ParamValue{
						StringVal: "https://github.com/conforma/cli",
						Type:      pipelinev1.ParamTypeString,
					},
				},
				pipelinev1.Param{
					Name: "revision",
					Value: pipelinev1.ParamValue{
						StringVal: "main",
						Type:      pipelinev1.ParamTypeString,
					},
				},
				pipelinev1.Param{
					Name: "pathInRepo",
					Value: pipelinev1.ParamValue{
						StringVal: "tasks/verify-enterprise-contract/0.1/verify-enterprise-contract.yaml",
						Type:      pipelinev1.ParamTypeString,
					},
				},
			))
			Expect(taskRun.Spec.Params).To(ContainElements(
				pipelinev1.Param{
					Name: "IMAGES",
					Value: pipelinev1.ParamValue{
						StringVal: `{"components":[{"name": "quay.io/test/image:latest", "containerImage":"quay.io/test/image:latest@sha256:1234567890abcdef"}]}`, //nolint:lll
						Type:      pipelinev1.ParamTypeString,
					},
				},
				pipelinev1.Param{
					Name: "IGNORE_REKOR",
					Value: pipelinev1.ParamValue{
						StringVal: "true",
						Type:      pipelinev1.ParamTypeString,
					},
				},
				pipelinev1.Param{
					Name: "TIMEOUT",
					Value: pipelinev1.ParamValue{
						StringVal: "60m",
						Type:      pipelinev1.ParamTypeString,
					},
				},
				pipelinev1.Param{
					Name: "WORKERS",
					Value: pipelinev1.ParamValue{
						StringVal: "1",
						Type:      pipelinev1.ParamTypeString,
					},
				},
				pipelinev1.Param{
					Name: "POLICY_CONFIGURATION",
					Value: pipelinev1.ParamValue{
						StringVal: "github.com/enterprise-contract/config//slsa3",
						Type:      pipelinev1.ParamTypeString,
					},
				},
				pipelinev1.Param{
					Name: "PUBLIC_KEY",
					Value: pipelinev1.ParamValue{
						StringVal: "k8s://enterprise-contract/public-key",
						Type:      pipelinev1.ParamTypeString,
					},
				},
			))
			Expect(taskRun.Spec.Timeout.Duration).To(Equal(10 * time.Minute))
		})

		It("Should create TaskRun with correct parameters when PipelineRun has no annotations", func() {
			By("Creating a PipelineRun without annotations")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName + "-conforma-default",
					Namespace: PipelineRunNamespace,
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status with results
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				PipelineRunStatusFields: pipelinev1.PipelineRunStatusFields{
					Results: []pipelinev1.PipelineRunResult{
						{
							Name:  "IMAGE_URL",
							Value: *pipelinev1.NewStructuredValues("quay.io/test/image:latest"),
						},
						{
							Name:  "IMAGE_DIGEST",
							Value: *pipelinev1.NewStructuredValues("sha256:1234567890abcdef"),
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Reconciling the PipelineRun")
			reconciler := &PipelineRunReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      PipelineRunName + "-conforma-default",
					Namespace: PipelineRunNamespace,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the TaskRun was created with correct parameters")
			taskRunList := &pipelinev1.TaskRunList{}
			Expect(k8sClient.List(context.Background(), taskRunList, client.InNamespace(PipelineRunNamespace))).Should(Succeed())

			var foundTaskRun *pipelinev1.TaskRun
			for i := range taskRunList.Items {
				tr := &taskRunList.Items[i]
				if tr.Labels[AnnotationConformaPipelineRun] == pipelineRun.Name {
					foundTaskRun = tr
					break
				}
			}
			Expect(foundTaskRun).ToNot(BeNil(), "Expected to find a TaskRun for the PipelineRun")
			taskRun := *foundTaskRun
			Expect(taskRun.Spec.Params).To(ContainElement(
				pipelinev1.Param{
					Name: "IMAGES",
					Value: pipelinev1.ParamValue{
						StringVal: `{"components":[{"name": "quay.io/test/image:latest", "containerImage":"quay.io/test/image:latest@sha256:1234567890abcdef"}]}`, //nolint:lll
						Type:      pipelinev1.ParamTypeString,
					},
				},
			))
		})

		It("Should fail when IMAGE_URL result is missing", func() {
			By("Creating a PipelineRun without IMAGE_URL result")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName + "-conforma-no-url",
					Namespace: PipelineRunNamespace,
				},
				Status: pipelinev1.PipelineRunStatus{
					PipelineRunStatusFields: pipelinev1.PipelineRunStatusFields{
						Results: []pipelinev1.PipelineRunResult{
							{
								Name:  "IMAGE_DIGEST",
								Value: *pipelinev1.NewStructuredValues("sha256:1234567890abcdef"),
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			By("Reconciling the PipelineRun")
			reconciler := &PipelineRunReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      PipelineRunName + "-conforma-no-url",
					Namespace: PipelineRunNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("IMAGE_URL result not found"))
		})

		It("Should fail when IMAGE_DIGEST result is missing", func() {
			By("Creating a PipelineRun without IMAGE_DIGEST result")
			pipelineRun := &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      PipelineRunName + "-conforma-no-digest",
					Namespace: PipelineRunNamespace,
				},
				Status: pipelinev1.PipelineRunStatus{
					PipelineRunStatusFields: pipelinev1.PipelineRunStatusFields{
						Results: []pipelinev1.PipelineRunResult{
							{
								Name:  "IMAGE_URL",
								Value: *pipelinev1.NewStructuredValues("quay.io/test/image:latest"),
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), pipelineRun)).Should(Succeed())

			// Update the status with only IMAGE_URL result
			pipelineRun.Status = pipelinev1.PipelineRunStatus{
				PipelineRunStatusFields: pipelinev1.PipelineRunStatusFields{
					Results: []pipelinev1.PipelineRunResult{
						{
							Name:  "IMAGE_URL",
							Value: *pipelinev1.NewStructuredValues("quay.io/test/image:latest"),
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), pipelineRun)).Should(Succeed())

			By("Reconciling the PipelineRun")
			reconciler := &PipelineRunReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      PipelineRunName + "-conforma-no-digest",
					Namespace: PipelineRunNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(fmt.Sprintf("IMAGE_DIGEST result not found in PipelineRun %s", pipelineRun.Name)))
		})
	})
})
