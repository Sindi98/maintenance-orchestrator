package controller_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/config"
	"github.com/Sindi98/maintenance-orchestrator/internal/controller"
)

var (
	testEnv     *envtest.Environment
	k8sClient   client.Client
	mgrCancel   context.CancelFunc
	skipEnvtest bool
)

// TestMain boots an envtest apiserver, installs the CRDs and starts the
// MaintenanceRequest reconciler for the whole package. When KUBEBUILDER_ASSETS is
// unset (no envtest binaries provisioned) the integration tests skip instead of
// failing, so `go test ./...` stays green in minimal environments.
func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		skipEnvtest = true
		os.Exit(m.Run())
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "deploy", "crd")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	if err != nil {
		panic(err)
	}
	if err := (&controller.MaintenanceRequestReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Config:   config.Defaults(),
		Recorder: mgr.GetEventRecorderFor("mreq-it"),
	}).SetupWithManager(mgr); err != nil {
		panic(err)
	}

	var mgrCtx context.Context
	mgrCtx, mgrCancel = context.WithCancel(context.Background())
	go func() { _ = mgr.Start(mgrCtx) }()

	code := m.Run()

	mgrCancel()
	_ = testEnv.Stop()
	os.Exit(code)
}

func TestDryRunReconcileCompletes(t *testing.T) {
	if skipEnvtest {
		t.Skip("set KUBEBUILDER_ASSETS (e.g. via `make test-integration`) to run envtest tests")
	}
	ctx := context.Background()

	pol := &v1alpha1.MaintenancePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-default"},
		Spec: v1alpha1.MaintenancePolicySpec{
			ProtectControlPlane: true,
			MaxConcurrentDrains: 1,
			FailureThreshold:    1,
		},
	}
	if err := k8sClient.Create(ctx, pol); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	defer func() { _ = k8sClient.Delete(ctx, pol) }()

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-it-1"}}
	if err := k8sClient.Create(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	defer func() { _ = k8sClient.Delete(ctx, node) }()

	mr := &v1alpha1.MaintenanceRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "it-dryrun"},
		Spec: v1alpha1.MaintenanceSpec{
			Mode:        v1alpha1.ModeDryRun,
			Reason:      "integration test",
			RequestedBy: "test",
			Target:      v1alpha1.TargetRef{Type: v1alpha1.TargetNode, NodeNames: []string{"worker-it-1"}},
			Approval:    v1alpha1.ApprovalSpec{Policy: v1alpha1.ApprovalAuto},
		},
	}
	if err := k8sClient.Create(ctx, mr); err != nil {
		t.Fatalf("create request: %v", err)
	}
	defer func() { _ = k8sClient.Delete(ctx, mr) }()

	deadline := time.Now().Add(45 * time.Second)
	var got v1alpha1.MaintenanceRequest
	for time.Now().Before(deadline) {
		if err := k8sClient.Get(ctx, client.ObjectKey{Name: "it-dryrun"}, &got); err != nil {
			t.Fatalf("get request: %v", err)
		}
		switch got.Status.Phase {
		case v1alpha1.PhaseCompleted:
			if got.Status.Plan == nil {
				t.Fatal("completed DryRun has no execution plan")
			}
			if got.Status.Plan.TotalNodes != 1 {
				t.Errorf("plan.totalNodes = %d, want 1", got.Status.Plan.TotalNodes)
			}
			return
		case v1alpha1.PhaseFailed, v1alpha1.PhaseBlocked:
			t.Fatalf("unexpected terminal phase %s: %s", got.Status.Phase, got.Status.Message)
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for Completed; last phase=%q", got.Status.Phase)
}
