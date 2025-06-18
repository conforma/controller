package controllers

import (
	"context"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	SignedAnnotation    = "chains.tekton.dev/signed" // added by Tekton Chains :contentReference[oaicite:1]{index=1}
	ValidatedAnnotation = "conforma.dev/validation"  // “success” | “failure”
)

type PipelineRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// TaskRun template injected via main.go (so the image / args are configurable)
	TaskRunTemplate pipelinev1.TaskRunSpec
}

func (r *PipelineRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Get the PipelineRun
	pipelineRun := &pipelinev1.PipelineRun{}
	if err := r.Get(ctx, req.NamespacedName, pipelineRun); err != nil {
		log.Error(err, "unable to fetch PipelineRun")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check for the chains.tekton.dev/signed annotation
	if value, exists := pipelineRun.Annotations[SignedAnnotation]; exists && value == "true" {
		log.Info("Found signed PipelineRun", "name", pipelineRun.Name, "namespace", pipelineRun.Namespace)
		// TODO: Add TaskRun creation logic here
	}

	return ctrl.Result{}, nil
}

func (r *PipelineRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pipelinev1.PipelineRun{}).
		Owns(&pipelinev1.TaskRun{}). // lets us get events when the TaskRun updates
		Complete(r)
}
