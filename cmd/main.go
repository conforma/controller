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

package main

import (
	"crypto/tls"
	"flag"
	"os"

	// Tekton APIs
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

	"github.com/conforma/controller/controllers"

	// K8s client-go helpers
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(pipelinev1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------
func getEnvOrDie(k string) string {
	v := os.Getenv(k)
	if v == "" {
		setupLog.Error(nil, "required env var missing", "key", k)
		os.Exit(1)
	}
	return v
}

func disableHTTP2(c *tls.Config) {
	setupLog.Info("disabling http/2")
	c.NextProtos = []string{"http/1.1"}
}

// -----------------------------------------------------------------------------
// main
// -----------------------------------------------------------------------------
func main() {
	var (
		metricsAddr                                      string
		metricsSecure                                    bool
		metricsCertPath, metricsCertName, metricsCertKey string
		enableLeaderElection                             bool
		probeAddr                                        string
		enableHTTP2                                      bool
		tlsOpts                                          []func(*tls.Config)
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443",
		"Address the metrics endpoint binds to (\":8443\" for HTTPS).")
	flag.BoolVar(&metricsSecure, "metrics-secure", true,
		"Serve the metrics endpoint via HTTPS.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"Directory containing tls.crt and tls.key for the metrics server. Leave empty for self-signed.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "Metrics TLS cert file name.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "Metrics TLS key file name.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"Address the health/ready probes bind to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for the controller manager.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"Enable HTTP/2 on metrics server (disabled by default for CVE hardening).")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// --- HTTP/2 hardening ------------------------------------------------------
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// --- Metrics server configuration -----------------------------------------
	metricsOpts := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: metricsSecure,
		TLSOpts:       tlsOpts,
	}
	if metricsSecure {
		metricsOpts.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// Optional: use a custom certificate for metrics
	var metricsCertWatcher *certwatcher.CertWatcher
	if metricsSecure && metricsCertPath != "" {
		var err error
		metricsCertWatcher, err = certwatcher.New(
			metricsCertPath+"/"+metricsCertName,
			metricsCertPath+"/"+metricsCertKey,
		)
		if err != nil {
			setupLog.Error(err, "initialising metrics certificate watcher")
			os.Exit(1)
		}
		metricsOpts.TLSOpts = append(metricsOpts.TLSOpts,
			func(c *tls.Config) { c.GetCertificate = metricsCertWatcher.GetCertificate })
	}

	// --- Manager --------------------------------------------------------------
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsOpts,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "82c555ec.conforma.dev",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// --- TaskRun template injected into reconciler ----------------------------
	validateSpec := pipelinev1.TaskRunSpec{
		TaskSpec: &pipelinev1.TaskSpec{
			Steps: []pipelinev1.Step{{
				Name:  "validate",
				Image: getEnvOrDie("VALIDATOR_IMAGE"),
				Args:  []string{"--pipelinerun=$(context.pipelineRun.name)"},
			}},
		},
		ServiceAccountName: os.Getenv("VALIDATOR_SA"),
	}

	if err = (&controllers.PipelineRunReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		TaskRunTemplate: validateSpec,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PipelineRun")
		os.Exit(1)
	}

	// --- Certificate watcher (metrics) ----------------------------------------
	if metricsCertWatcher != nil {
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics cert watcher")
			os.Exit(1)
		}
	}

	// --- Health / readiness probes -------------------------------------------
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
