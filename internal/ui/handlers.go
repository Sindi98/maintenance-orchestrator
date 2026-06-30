package ui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

type action string

const (
	actionApprove action = "approve"
	actionReject  action = "reject"
	actionPause   action = "pause"
	actionResume  action = "resume"
	actionCancel  action = "cancel"
)

type listData struct {
	Requests []v1alpha1.MaintenanceRequest
}

type detailData struct {
	Request v1alpha1.MaintenanceRequest
}

type policiesData struct {
	Policies []v1alpha1.MaintenancePolicy
}

type newData struct {
	Error  string
	Form   map[string]string
	Modes  []string
	Strats []string
}

// handleList renders the dashboard home (request list). Unknown paths 404.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	reqs, err := s.listRequests(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderPage(w, "list.html", listData{Requests: reqs})
}

// handleListFragment renders just the request rows, for live refresh.
func (s *Server) handleListFragment(w http.ResponseWriter, r *http.Request) {
	reqs, err := s.listRequests(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderFragment(w, "requests_rows", listData{Requests: reqs})
}

// handleDetail renders a single request.
func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	mr, err := s.getRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderPage(w, "detail.html", detailData{Request: *mr})
}

// handleStatusFragment renders only the live status block of a request.
func (s *Server) handleStatusFragment(w http.ResponseWriter, r *http.Request) {
	mr, err := s.getRequest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderFragment(w, "request_status", mr)
}

// handlePolicies renders the (read-only) policy list.
func (s *Server) handlePolicies(w http.ResponseWriter, r *http.Request) {
	list := &v1alpha1.MaintenancePolicyList{}
	if err := s.client.List(r.Context(), list); err != nil {
		s.fail(w, err)
		return
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	s.renderPage(w, "policies.html", policiesData{Policies: list.Items})
}

// handleNewForm renders the create form.
func (s *Server) handleNewForm(w http.ResponseWriter, _ *http.Request) {
	s.renderPage(w, "new.html", defaultNewData(""))
}

// handleCreate parses the create form and creates the MaintenanceRequest.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	mr, err := parseRequestForm(r)
	if err != nil {
		d := defaultNewData(err.Error())
		for k := range r.PostForm {
			d.Form[k] = r.PostForm.Get(k)
		}
		w.WriteHeader(http.StatusBadRequest)
		s.renderPage(w, "new.html", d)
		return
	}
	if err := s.client.Create(r.Context(), mr); err != nil {
		d := defaultNewData("create failed: " + err.Error())
		for k := range r.PostForm {
			d.Form[k] = r.PostForm.Get(k)
		}
		w.WriteHeader(http.StatusConflict)
		s.renderPage(w, "new.html", d)
		return
	}
	http.Redirect(w, r, "/requests/"+mr.Name, http.StatusSeeOther)
}

// handleAction returns a handler that applies the given runtime action.
func (s *Server) handleAction(a action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		_ = r.ParseForm()
		gate := v1alpha1.Gate(strings.TrimSpace(r.FormValue("gate")))
		if gate == "" {
			gate = v1alpha1.GateDrain
		}
		by := strings.TrimSpace(r.FormValue("by"))
		if by == "" {
			by = "dashboard"
		}
		err := s.mutate(r.Context(), name, func(mr *v1alpha1.MaintenanceRequest) {
			applyAction(mr, a, gate, by)
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		http.Redirect(w, r, "/requests/"+name, http.StatusSeeOther)
	}
}

// --- data helpers ---

func (s *Server) listRequests(ctx context.Context) ([]v1alpha1.MaintenanceRequest, error) {
	list := &v1alpha1.MaintenanceRequestList{}
	if err := s.client.List(ctx, list); err != nil {
		return nil, err
	}
	// Newest first.
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].CreationTimestamp.After(list.Items[j].CreationTimestamp.Time)
	})
	return list.Items, nil
}

func (s *Server) getRequest(ctx context.Context, name string) (*v1alpha1.MaintenanceRequest, error) {
	mr := &v1alpha1.MaintenanceRequest{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name}, mr); err != nil {
		return nil, err
	}
	return mr, nil
}

// mutate applies fn to the named request's spec via a merge patch. A merge patch
// carries no resourceVersion precondition, so the action succeeds even while the
// controller is rapidly updating .status — a full Update would otherwise lose the
// optimistic-lock race and fail with conflicts.
func (s *Server) mutate(ctx context.Context, name string, fn func(*v1alpha1.MaintenanceRequest)) error {
	mr := &v1alpha1.MaintenanceRequest{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name}, mr); err != nil {
		return err
	}
	patch := client.MergeFrom(mr.DeepCopy())
	fn(mr)
	return s.client.Patch(ctx, mr, patch)
}

func applyAction(mr *v1alpha1.MaintenanceRequest, a action, gate v1alpha1.Gate, by string) {
	switch a {
	case actionApprove:
		setGate(mr, gate, v1alpha1.DecisionApproved, by)
	case actionReject:
		setGate(mr, gate, v1alpha1.DecisionRejected, by)
	case actionPause:
		mr.Spec.Pause = true
	case actionResume:
		mr.Spec.Pause = false
	case actionCancel:
		mr.Spec.Cancel = true
	}
}

// setGate upserts a gate decision in spec.approval.gates.
func setGate(mr *v1alpha1.MaintenanceRequest, gate v1alpha1.Gate, decision v1alpha1.Decision, by string) {
	now := metav1.Now()
	gd := v1alpha1.GateDecision{Gate: gate, Decision: decision, ApprovedBy: by, Time: &now}
	for i := range mr.Spec.Approval.Gates {
		if mr.Spec.Approval.Gates[i].Gate == gate {
			mr.Spec.Approval.Gates[i] = gd
			return
		}
	}
	mr.Spec.Approval.Gates = append(mr.Spec.Approval.Gates, gd)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if apierrors.IsNotFound(err) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Log the full error server-side but return a generic message: the raw error
	// can carry Kubernetes API/client internals that should not reach the browser.
	s.log.Error(err, "dashboard request failed")
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func defaultNewData(errMsg string) newData {
	return newData{
		Error: errMsg,
		Form: map[string]string{
			"mode":          string(v1alpha1.ModeDryRun),
			"strategy":      string(v1alpha1.StrategySerial),
			"maxConcurrent": "1",
			"targetType":    string(v1alpha1.TargetNode),
			"approval":      string(v1alpha1.ApprovalAuto),
			"uncordonAfter": "on",
		},
		Modes:  []string{string(v1alpha1.ModeDryRun), string(v1alpha1.ModeAdvisory), string(v1alpha1.ModeExecute)},
		Strats: []string{string(v1alpha1.StrategySerial), string(v1alpha1.StrategyBatched), string(v1alpha1.StrategyByZone), string(v1alpha1.StrategyByPool)},
	}
}

// parseRequestForm builds a MaintenanceRequest from the create form.
func parseRequestForm(r *http.Request) (*v1alpha1.MaintenanceRequest, error) {
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	reason := strings.TrimSpace(r.PostForm.Get("reason"))
	requestedBy := strings.TrimSpace(r.PostForm.Get("requestedBy"))
	if reason == "" || requestedBy == "" {
		return nil, fmt.Errorf("reason and requestedBy are required")
	}

	target, err := parseTarget(r)
	if err != nil {
		return nil, err
	}

	maxc := int32(formInt(r, "maxConcurrent", 1))
	if maxc < 1 {
		maxc = 1
	}
	spec := v1alpha1.MaintenanceSpec{
		Mode:          v1alpha1.Mode(formStr(r, "mode", string(v1alpha1.ModeDryRun))),
		Reason:        reason,
		RequestedBy:   requestedBy,
		Target:        target,
		Strategy:      v1alpha1.Strategy(formStr(r, "strategy", string(v1alpha1.StrategySerial))),
		MaxConcurrent: maxc,
		UncordonAfter: formBool(r, "uncordonAfter"),
		Approval:      v1alpha1.ApprovalSpec{Policy: v1alpha1.ApprovalPolicy(formStr(r, "approval", string(v1alpha1.ApprovalAuto)))},
	}
	if b := int32(formInt(r, "batchSize", 0)); b > 0 {
		spec.BatchSize = b
	}
	if v := strings.TrimSpace(r.PostForm.Get("policyRef")); v != "" {
		spec.PolicyRef = &v1alpha1.PolicyRef{Name: v}
	}
	spec.AllowControlPlane = formBool(r, "allowControlPlane")
	spec.Force = formBool(r, "force")

	if formBool(r, "upgrade") {
		spec.Upgrade = &v1alpha1.UpgradeSpec{
			Strategy:             v1alpha1.UpgradeReplaceNode,
			MachineAPI:           v1alpha1.MachineAPI(formStr(r, "machineAPI", string(v1alpha1.MachineAPIAuto))),
			TargetKubeletVersion: strings.TrimSpace(r.PostForm.Get("targetKubeletVersion")),
		}
	}

	return &v1alpha1.MaintenanceRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}, nil
}

func parseTarget(r *http.Request) (v1alpha1.TargetRef, error) {
	t := v1alpha1.TargetRef{Type: v1alpha1.TargetType(formStr(r, "targetType", string(v1alpha1.TargetNode)))}
	switch t.Type {
	case v1alpha1.TargetNode:
		names := splitList(r.PostForm.Get("nodeNames"))
		if len(names) == 0 {
			return t, fmt.Errorf("at least one node name is required for a Node target")
		}
		t.NodeNames = names
	case v1alpha1.TargetNodeSelector:
		labels, err := parseLabels(r.PostForm.Get("selector"))
		if err != nil {
			return t, err
		}
		if len(labels) == 0 {
			return t, fmt.Errorf("a NodeSelector target needs at least one label (key=value)")
		}
		t.Selector = &metav1.LabelSelector{MatchLabels: labels}
	case v1alpha1.TargetPool:
		t.PoolKey = strings.TrimSpace(r.PostForm.Get("poolKey"))
		t.PoolValue = strings.TrimSpace(r.PostForm.Get("poolValue"))
		if t.PoolKey == "" || t.PoolValue == "" {
			return t, fmt.Errorf("a Pool target needs poolKey and poolValue")
		}
	default:
		return t, fmt.Errorf("invalid target type %q", t.Type)
	}
	return t, nil
}

func parseLabels(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range splitList(s) {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			return nil, fmt.Errorf("invalid label %q (want key=value)", pair)
		}
		out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return out, nil
}

func splitList(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	out := make([]string, 0, len(f))
	for _, v := range f {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func formStr(r *http.Request, key, def string) string {
	if v := strings.TrimSpace(r.PostForm.Get(key)); v != "" {
		return v
	}
	return def
}

func formInt(r *http.Request, key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get(key))); err == nil {
		return n
	}
	return def
}

func formBool(r *http.Request, key string) bool {
	v := strings.ToLower(strings.TrimSpace(r.PostForm.Get(key)))
	return v == "on" || v == "true" || v == "1" || v == "yes"
}
