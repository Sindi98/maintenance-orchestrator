package main

import (
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	maintenancev1alpha1 "github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/config"
	"github.com/Sindi98/maintenance-orchestrator/internal/controller"
	"github.com/Sindi98/maintenance-orchestrator/internal/logging"
	"github.com/Sindi98/maintenance-orchestrator/internal/metrics"
	"github.com/Sindi98/maintenance-orchestrator/internal/ui"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(maintenancev1alpha1.AddToScheme(scheme))
}

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", "",
		"path to an optional YAML config file (sets the CONFIG_FILE used by the loader)")
	flag.Parse()

	if configFile != "" {
		_ = os.Setenv("CONFIG_FILE", configFile)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	ctrl.SetLogger(logging.Setup(cfg.LogLevel, cfg.LogFormat))
	setupLog := ctrl.Log.WithName("setup")

	metrics.Register()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: cfg.MetricsAddr,
		},
		HealthProbeBindAddress: cfg.ProbeAddr,
		LeaderElection:         cfg.LeaderElection,
		LeaderElectionID:       cfg.LeaderElectionID,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.MaintenanceRequestReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Config:   cfg,
		Recorder: mgr.GetEventRecorderFor("maintenance-orchestrator"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "MaintenanceRequest")
		os.Exit(1)
	}

	if err := (&controller.MaintenancePolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: cfg,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "MaintenancePolicy")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if cfg.UIEnabled {
		if err := mgr.Add(ui.NewServer(mgr.GetClient(), cfg.UIAddr, ctrl.Log.WithName("ui"))); err != nil {
			setupLog.Error(err, "unable to add web dashboard")
			os.Exit(1)
		}
	}

	setupLog.Info("starting maintenance-orchestrator",
		"metricsAddr", cfg.MetricsAddr,
		"probeAddr", cfg.ProbeAddr,
		"uiEnabled", cfg.UIEnabled,
		"leaderElection", cfg.LeaderElection,
		"reconcileConcurrency", cfg.ReconcileConcurrency,
	)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
