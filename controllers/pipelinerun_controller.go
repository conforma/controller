package controllers

import (
	"context"
	"fmt"
	"os"
	"time"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	ConformaDomain = "conforma.dev"
	// added by Tekton Chains :contentReference[oaicite:1]{index=1}
	AnnotationSigned = "chains.tekton.dev/signed"
	// AnnotationConformaTriggeredOn indicates that a conforma cli TaskRun has been
	// created, along with the timestamp of the creation
	AnnotationConformaTriggeredOn = ConformaDomain + "/triggered-on"
	// AnnotationConformaPipelineRun indicates the name of the PipelineRun that has
	// triggered the Conforma cli execution
	AnnotationConformaPipelineRun = ConformaDomain + "/pipelinerun-name"
	// ConfigMapName is the name of the ConfigMap containing Conforma parameters
	ConfigMapName = "conforma-controller-conforma-params"
)

type PipelineRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// TaskRun template injected via main.go (so the image / args are configurable)
	TaskRunTemplate pipelinev1.TaskRunSpec
}

func (r *PipelineRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pipelinev1.PipelineRun{}).
		WithEventFilter(
			predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					pipelineRun, ok := e.Object.(*pipelinev1.PipelineRun)
					if !ok {
						return false
					}
					return filterPipelineRun(pipelineRun)
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return false
				},
				GenericFunc: func(e event.GenericEvent) bool {
					return false
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					pipelineRun, ok := e.ObjectNew.(*pipelinev1.PipelineRun)
					if !ok {
						return false
					}
					return filterPipelineRun(pipelineRun)
				},
			},
		).
		Owns(&pipelinev1.TaskRun{}). // lets us get events when the TaskRun updates
		Complete(r)
}

func filterPipelineRun(pipelineRun *pipelinev1.PipelineRun) bool {
	return isSucceededPipelineRun(pipelineRun) &&
		isSignedPipelineRun(pipelineRun) &&
		!isConformaTriggered(pipelineRun)
}

func (r *PipelineRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Get the PipelineRun
	pipelineRun := &pipelinev1.PipelineRun{}
	if err := r.Get(ctx, req.NamespacedName, pipelineRun); err != nil {
		log.Error(err, "unable to fetch PipelineRun")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Found signed PipelineRun", "name", pipelineRun.Name, "namespace", pipelineRun.Namespace)

	if err := r.triggerConforma(ctx, pipelineRun); err != nil {
		log.Error(err, "Failed to trigger Conforma")
		return ctrl.Result{}, err
	}

	// Mark Conforma cli execution as triggered
	if err := r.markConformaTriggered(ctx, pipelineRun); err != nil {
		log.Error(err, "Failed to mark Conforma cli execution as triggered")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PipelineRunReconciler) triggerConforma(ctx context.Context, pr *pipelinev1.PipelineRun) error {
	log := log.FromContext(ctx)

	// Get IMAGE_URL and IMAGE_DIGEST from PipelineRun results
	imageURL := ""
	imageDigest := ""
	for _, result := range pr.Status.Results {
		switch result.Name {
		case "IMAGE_URL":
			imageURL = result.Value.StringVal
		case "IMAGE_DIGEST":
			imageDigest = result.Value.StringVal
		}
	}

	if imageURL == "" {
		return fmt.Errorf("IMAGE_URL result not found in PipelineRun %s", pr.Name)
	}
	if imageDigest == "" {
		return fmt.Errorf("IMAGE_DIGEST result not found in PipelineRun %s", pr.Name)
	}

	// Create JSON string for SNAPSHOT_FILENAME
	snapshotJSON := fmt.Sprintf(
		`{"components":[{"name": "%s", "containerImage":"%s@%s"}]}`, imageURL, imageURL, imageDigest)

	// get param values from a configmap
	configMap := &v1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{Name: ConfigMapName, Namespace: getControllerNamespace()}, configMap)
	if err != nil {
		log.Error(err, "Failed to get Conforma params configmap")
		return err
	}

	taskRun := &pipelinev1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "conforma-verify-",
			Namespace:    getControllerNamespace(),
			Labels: map[string]string{
				"app.kubernetes.io/created-by": "conforma-controller",
				AnnotationConformaPipelineRun:  pr.Name,
			},
		},
		Spec: pipelinev1.TaskRunSpec{
			TaskRef: &pipelinev1.TaskRef{
				ResolverRef: pipelinev1.ResolverRef{
					Resolver: "git",
					Params: []pipelinev1.Param{
						{
							Name:  "url",
							Value: pipelinev1.ParamValue{StringVal: configMap.Data["GIT_URL"], Type: pipelinev1.ParamTypeString},
						},
						{
							Name:  "revision",
							Value: pipelinev1.ParamValue{StringVal: configMap.Data["GIT_REVISION"], Type: pipelinev1.ParamTypeString},
						},
						{
							Name:  "pathInRepo",
							Value: pipelinev1.ParamValue{StringVal: configMap.Data["GIT_PATH"], Type: pipelinev1.ParamTypeString},
						},
					},
				},
			},
			Params: []pipelinev1.Param{
				{Name: "IMAGES", Value: *pipelinev1.NewStructuredValues(snapshotJSON)},
				// apply these values from the configmap
				{Name: "IGNORE_REKOR", Value: *pipelinev1.NewStructuredValues(configMap.Data["IGNORE_REKOR"])},
				{Name: "TIMEOUT", Value: *pipelinev1.NewStructuredValues(configMap.Data["TIMEOUT"])},
				{Name: "WORKERS", Value: *pipelinev1.NewStructuredValues(configMap.Data["WORKERS"])},
				{Name: "POLICY_CONFIGURATION", Value: *pipelinev1.NewStructuredValues(configMap.Data["POLICY_CONFIGURATION"])},
				{Name: "PUBLIC_KEY", Value: *pipelinev1.NewStructuredValues(configMap.Data["PUBLIC_KEY"])},
			},
			Timeout: &metav1.Duration{Duration: 10 * time.Minute},
		},
	}

	if err := r.Client.Create(ctx, taskRun); err != nil {
		log.Error(err, "Failed to create Conforma TaskRun")
		return err
	}

	log.Info("Conforma TaskRun created", "name", taskRun.Name)
	return nil
}

// getControllerNamespace returns the namespace where the controller is running
func getControllerNamespace() string {
	// Read namespace from the downward API file
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		// Fallback to default if we can't read the file
		return "default"
	}
	return string(nsBytes)
}

// markConformaTriggered marks the PipelineRun as having triggered Conforma cli execution
func (r *PipelineRunReconciler) markConformaTriggered(ctx context.Context, pr *pipelinev1.PipelineRun) error {
	patch := client.MergeFrom(pr.DeepCopy())
	if pr.Annotations == nil {
		pr.Annotations = make(map[string]string)
	}
	pr.Annotations[AnnotationConformaTriggeredOn] = time.Now().Format(time.RFC3339)
	return r.Patch(ctx, pr, patch)
}

// isSucceededPipelineRun returns a boolean indicating whether the
// PipelineRun is succeeded
func isSucceededPipelineRun(pr *pipelinev1.PipelineRun) bool {
	condition := pr.Status.GetCondition(apis.ConditionSucceeded)
	if condition == nil {
		return false
	}

	return condition.Status == v1.ConditionTrue
}

// isSignedPipelineRun returns a boolean indicating whether the
// PipelineRun is signed
func isSignedPipelineRun(pr *pipelinev1.PipelineRun) bool {
	signed, ok := pr.Annotations[AnnotationSigned]
	if !ok {
		return false
	}

	return signed == "true"
}

// isConformaTriggered checks if conforma cli TaskRun has been triggered
func isConformaTriggered(pr *pipelinev1.PipelineRun) bool {
	_, ok := pr.Annotations[AnnotationConformaTriggeredOn]
	return ok
}
