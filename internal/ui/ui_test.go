package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

func newTestServer(t *testing.T, objs ...client.Object) (*Server, http.Handler) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	s := NewServer(cl, ":0", logr.Discard())
	mux := http.NewServeMux()
	s.routes(mux)
	return s, mux
}

func mkRequest(name string, phase v1alpha1.Phase) *v1alpha1.MaintenanceRequest {
	return &v1alpha1.MaintenanceRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.Now()},
		Spec: v1alpha1.MaintenanceSpec{
			Mode:          v1alpha1.ModeExecute,
			Reason:        "test",
			RequestedBy:   "tester",
			Target:        v1alpha1.TargetRef{Type: v1alpha1.TargetNode, NodeNames: []string{"w1"}},
			Strategy:      v1alpha1.StrategySerial,
			MaxConcurrent: 1,
			Approval:      v1alpha1.ApprovalSpec{Policy: v1alpha1.ApprovalManualBeforeDrain},
		},
		Status: v1alpha1.MaintenanceStatus{
			Phase:        phase,
			ApprovalGate: v1alpha1.GateDrain,
			Message:      "awaiting",
			Summary:      v1alpha1.ProgressSummary{Total: 2, Completed: 1},
			Nodes: []v1alpha1.NodeExecutionStatus{
				{Node: "w1", Phase: v1alpha1.NodeDraining, Batch: 0, TotalPods: 3, EvictedPods: 1},
			},
			Preflight: []v1alpha1.PreflightCheckResult{
				{Code: v1alpha1.CodeEmptyDir, Node: "w1", Status: v1alpha1.CheckWarn, Message: "emptyDir", Time: metav1.Now()},
			},
		},
	}
}

func do(t *testing.T, h http.Handler, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	req := (*http.Request)(nil)
	if form != nil {
		body = strings.NewReader(form.Encode())
		req = httptest.NewRequest(method, target, body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPagesRender(t *testing.T) {
	mr := mkRequest("patch-1", v1alpha1.PhaseAwaitingApproval)
	pol := &v1alpha1.MaintenancePolicy{ObjectMeta: metav1.ObjectMeta{Name: "cluster-default"}}
	_, h := newTestServer(t, mr, pol)

	for _, tc := range []struct{ path, want string }{
		{"/", "patch-1"},
		{"/new", "New maintenance request"},
		{"/requests/patch-1", "Spec"},
		{"/requests/patch-1/status", "Approve"},
		{"/fragment/requests", "patch-1"},
		{"/policies", "cluster-default"},
		{"/healthz", "ok"},
	} {
		rec := do(t, h, http.MethodGet, tc.path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: code %d", tc.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("GET %s: body missing %q", tc.path, tc.want)
		}
	}
}

func TestUnknownPath404(t *testing.T) {
	_, h := newTestServer(t)
	if rec := do(t, h, http.MethodGet, "/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope: code %d, want 404", rec.Code)
	}
}

func TestCreate(t *testing.T) {
	s, h := newTestServer(t)
	form := url.Values{
		"name": {"new-req"}, "mode": {"DryRun"}, "reason": {"r"}, "requestedBy": {"me"},
		"targetType": {"Node"}, "nodeNames": {"w1 w2"}, "strategy": {"Serial"},
		"maxConcurrent": {"1"}, "approval": {"AutoApprove"}, "uncordonAfter": {"on"},
		"upgrade": {"on"}, "machineAPI": {"Auto"}, "targetKubeletVersion": {"v1.30.2"},
	}
	rec := do(t, h, http.MethodPost, "/requests", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: code %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	got := &v1alpha1.MaintenanceRequest{}
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: "new-req"}, got); err != nil {
		t.Fatalf("created object not found: %v", err)
	}
	if len(got.Spec.Target.NodeNames) != 2 || !got.Spec.ReplaceNodes() || got.Spec.Upgrade.TargetKubeletVersion != "v1.30.2" {
		t.Errorf("parsed spec wrong: %+v", got.Spec)
	}
}

func TestCreateValidationError(t *testing.T) {
	_, h := newTestServer(t)
	// Missing reason/requestedBy and no node names.
	rec := do(t, h, http.MethodPost, "/requests", url.Values{"name": {"x"}, "targetType": {"Node"}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid create: code %d, want 400", rec.Code)
	}
}

func TestActions(t *testing.T) {
	mr := mkRequest("act", v1alpha1.PhaseAwaitingApproval)
	s, h := newTestServer(t, mr)

	if rec := do(t, h, http.MethodPost, "/requests/act/approve", url.Values{"gate": {"Drain"}, "by": {"alice"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("approve: code %d", rec.Code)
	}
	got := &v1alpha1.MaintenanceRequest{}
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: "act"}, got); err != nil {
		t.Fatal(err)
	}
	if len(got.Spec.Approval.Gates) != 1 || got.Spec.Approval.Gates[0].Decision != v1alpha1.DecisionApproved || got.Spec.Approval.Gates[0].ApprovedBy != "alice" {
		t.Errorf("approve gate not recorded: %+v", got.Spec.Approval.Gates)
	}

	if rec := do(t, h, http.MethodPost, "/requests/act/cancel", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("cancel: code %d", rec.Code)
	}
	_ = s.client.Get(context.Background(), types.NamespacedName{Name: "act"}, got)
	if !got.Spec.Cancel {
		t.Error("cancel did not set spec.cancel")
	}
}

func TestCreateErrorPreservesForm(t *testing.T) {
	_, h := newTestServer(t)
	// reason is omitted -> validation error. uncordonAfter is omitted (a browser
	// omits unchecked boxes); allowControlPlane is checked; upgrade fields set.
	form := url.Values{
		"name": {"x"}, "mode": {"Execute"}, "requestedBy": {"me"},
		"targetType": {"Node"}, "nodeNames": {"w1"},
		"allowControlPlane":    {"on"},
		"upgrade":              {"on"},
		"machineAPI":           {"OpenShift"},
		"targetKubeletVersion": {"v1.31.0"},
	}
	rec := do(t, h, http.MethodPost, "/requests", form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create error: code %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `name="uncordonAfter" checked`) {
		t.Error("uncordonAfter must render unchecked when the operator omitted it")
	}
	if !strings.Contains(body, `name="allowControlPlane" checked`) {
		t.Error("allowControlPlane must stay checked on error re-render")
	}
	if !strings.Contains(body, `value="v1.31.0"`) {
		t.Error("targetKubeletVersion must be preserved on error re-render")
	}
	if !strings.Contains(body, `<option value="OpenShift" selected>`) {
		t.Error("machineAPI selection must be preserved on error re-render")
	}
}
