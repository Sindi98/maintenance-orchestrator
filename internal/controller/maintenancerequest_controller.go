package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/audit"
	"github.com/Sindi98/maintenance-orchestrator/internal/config"
	"github.com/Sindi98/maintenance-orchestrator/internal/executor"
	"github.com/Sindi98/maintenance-orchestrator/internal/kube"
	"github.com/Sindi98/maintenance-orchestrator/internal/metrics"
	"github.com/Sindi98/maintenance-orchestrator/internal/planner"
	"github.com/Sindi98/maintenance-orchestrator/internal/policy"
	"github.com/Sindi98/maintenance-orchestrator/internal/preflight"
	"github.com/Sindi98/maintenance-orchestrator/internal/statemachine"
)

// MaintenanceRequestReconciler drives a MaintenanceRequest through its lifecycle
// using a poll-and-requeue model: every Reconcile computes the next step from the
// observed state, writes .status, and returns a RequeueAfter.
type MaintenanceRequestReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Config   *config.Config
	Recorder record.EventRecorder

	// Built in SetupWithManager.
	kube      *kube.Client
	preflight *preflight.Engine
	planner   *planner.Planner
	executor  *executor.Executor
	audit     *audit.Logger
}

// +kubebuilder:rbac:groups=maintenance.platform.dev,resources=maintenancerequests,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=maintenance.platform.dev,resources=maintenancerequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=maintenance.platform.dev,resources=maintenancepolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups=core,resources=pods,verbs=delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments;replicasets;statefulsets;daemonsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile is the entry point invoked by controller-runtime.
func (r *MaintenanceRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	mr := &v1alpha1.MaintenanceRequest{}
	if err := r.Get(ctx, req.NamespacedName, mr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !mr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// First observation: enter Pending and record creation.
	if mr.Status.Phase == "" {
		mr.Status.Phase = statemachine.InitialPhase()
		mr.Status.Message = "request accepted"
		setCondition(mr, v1alpha1.CondValidated, metav1.ConditionFalse, "Pending", "request accepted")
		if err := r.updateStatus(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionCreated, "maintenance request created", auditFields(mr))
		metrics.RequestsTotal.WithLabelValues(string(mr.Spec.Mode), string(mr.Spec.Target.Type)).Inc()
		r.refreshActiveGauge(ctx)
		return ctrl.Result{Requeue: true}, nil
	}

	if statemachine.IsTerminal(mr.Status.Phase) {
		return ctrl.Result{}, nil
	}

	// Cancel has top priority from any non-terminal phase.
	if mr.Spec.Cancel {
		return r.cancel(ctx, mr, "cancelled by user")
	}

	// Pause from an interruptible phase.
	if mr.Spec.Pause && canPause(mr.Status.Phase) {
		return r.pause(ctx, mr)
	}

	pol, err := policy.Resolve(ctx, r.Client, mr, r.Config.DefaultPolicyName)
	if err != nil {
		return r.fail(ctx, mr, "PolicyResolution", err.Error())
	}

	switch mr.Status.Phase {
	case v1alpha1.PhasePending, v1alpha1.PhaseValidating:
		return r.reconcileValidating(ctx, mr, pol)
	case v1alpha1.PhaseAwaitingApproval:
		return r.reconcileAwaitingApproval(ctx, mr, pol)
	case v1alpha1.PhasePlanned:
		return r.reconcilePlanned(ctx, mr, pol)
	case v1alpha1.PhaseExecuting:
		return r.reconcileExecuting(ctx, mr, pol)
	case v1alpha1.PhasePaused:
		return r.reconcilePaused(ctx, mr)
	case v1alpha1.PhaseBlocked:
		return r.reconcileBlocked(ctx, mr, pol)
	default:
		logger.Info("unknown phase, ignoring", "phase", mr.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// SetupWithManager wires the reconciler, registers the spec.nodeName pod index,
// builds the domain helpers and arranges audit-file cleanup on shutdown.
func (r *MaintenanceRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, kube.IndexPodNodeName,
		func(o client.Object) []string {
			pod, ok := o.(*corev1.Pod)
			if !ok || pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		}); err != nil {
		return fmt.Errorf("register pod node-name index: %w", err)
	}

	r.kube = kube.New(mgr.GetClient())
	r.preflight = preflight.NewEngine(r.kube)
	r.planner = planner.NewPlanner(r.kube, r.Config.DefaultPoolKeys)
	r.executor = executor.New(r.kube)

	aud, err := audit.New(mgr.GetLogger(), r.Recorder, r.Config.EnableK8sEvents, r.Config.AuditExportPath)
	if err != nil {
		return fmt.Errorf("init audit logger: %w", err)
	}
	r.audit = aud
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return r.audit.Close()
	})); err != nil {
		return fmt.Errorf("register audit cleanup: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MaintenanceRequest{}).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: r.Config.ReconcileConcurrency}).
		Named("maintenancerequest").
		Complete(r)
}

// --- status & lifecycle helpers ---

func (r *MaintenanceRequestReconciler) updateStatus(ctx context.Context, mr *v1alpha1.MaintenanceRequest) error {
	return r.Status().Update(ctx, mr)
}

// setCondition upserts a status condition with the object's current generation.
func setCondition(mr *v1alpha1.MaintenanceRequest, condType string, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&mr.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: mr.Generation,
		LastTransitionTime: metav1.Now(),
	})
}

func auditFields(mr *v1alpha1.MaintenanceRequest) map[string]string {
	return map[string]string{
		"mode":        string(mr.Spec.Mode),
		"targetType":  string(mr.Spec.Target.Type),
		"strategy":    string(mr.Spec.Strategy),
		"requestedBy": mr.Spec.RequestedBy,
		"reason":      mr.Spec.Reason,
	}
}

// refreshActiveGauge recomputes the active_maintenances gauge from cluster state.
func (r *MaintenanceRequestReconciler) refreshActiveGauge(ctx context.Context) {
	list := &v1alpha1.MaintenanceRequestList{}
	if err := r.List(ctx, list); err != nil {
		return
	}
	var active float64
	for i := range list.Items {
		if statemachine.IsActive(list.Items[i].Status.Phase) {
			active++
		}
	}
	metrics.ActiveMaintenances.Set(active)
}

func (r *MaintenanceRequestReconciler) complete(ctx context.Context, mr *v1alpha1.MaintenanceRequest, msg string) (ctrl.Result, error) {
	now := metav1.Now()
	mr.Status.Phase = v1alpha1.PhaseCompleted
	mr.Status.Message = msg
	if mr.Status.CompletionTime == nil {
		mr.Status.CompletionTime = &now
	}
	setCondition(mr, v1alpha1.CondExecuting, metav1.ConditionFalse, "Completed", "execution finished")
	setCondition(mr, v1alpha1.CondCompleted, metav1.ConditionTrue, "Completed", msg)
	metrics.SuccessTotal.Inc()
	r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionCompleted, msg, nil)
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	r.refreshActiveGauge(ctx)
	return ctrl.Result{}, nil
}

func (r *MaintenanceRequestReconciler) fail(ctx context.Context, mr *v1alpha1.MaintenanceRequest, reason, msg string) (ctrl.Result, error) {
	now := metav1.Now()
	mr.Status.Phase = v1alpha1.PhaseFailed
	mr.Status.Message = msg
	mr.Status.LastError = msg
	if mr.Status.CompletionTime == nil {
		mr.Status.CompletionTime = &now
	}
	setCondition(mr, v1alpha1.CondFailed, metav1.ConditionTrue, reason, msg)
	metrics.FailureTotal.WithLabelValues(reason).Inc()
	r.audit.Record(mr, corev1.EventTypeWarning, audit.ActionFailed, msg, map[string]string{"reason": reason})
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	r.refreshActiveGauge(ctx)
	return ctrl.Result{}, nil
}

func (r *MaintenanceRequestReconciler) cancel(ctx context.Context, mr *v1alpha1.MaintenanceRequest, msg string) (ctrl.Result, error) {
	now := metav1.Now()
	mr.Status.Phase = v1alpha1.PhaseCancelled
	mr.Status.Message = msg
	if mr.Status.CompletionTime == nil {
		mr.Status.CompletionTime = &now
	}
	setCondition(mr, v1alpha1.CondExecuting, metav1.ConditionFalse, "Cancelled", msg)
	r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionCancelled, msg, nil)
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	r.refreshActiveGauge(ctx)
	return ctrl.Result{}, nil
}

func (r *MaintenanceRequestReconciler) pause(ctx context.Context, mr *v1alpha1.MaintenanceRequest) (ctrl.Result, error) {
	if mr.Status.Phase != v1alpha1.PhasePaused {
		r.audit.Record(mr, corev1.EventTypeNormal, audit.ActionPaused, "request paused", nil)
	}
	mr.Status.Phase = v1alpha1.PhasePaused
	mr.Status.Message = "paused"
	if err := r.updateStatus(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}
	r.refreshActiveGauge(ctx)
	return r.requeue(r.Config.GlobalRequeueInterval.Duration), nil
}

func (r *MaintenanceRequestReconciler) requeue(d time.Duration) ctrl.Result {
	if d <= 0 {
		d = r.Config.GlobalRequeueInterval.Duration
	}
	return ctrl.Result{RequeueAfter: d}
}

// --- timeout helpers ---

func (r *MaintenanceRequestReconciler) effectiveGlobalTimeout(mr *v1alpha1.MaintenanceRequest) time.Duration {
	if mr.Spec.GlobalTimeout.Duration > 0 {
		return mr.Spec.GlobalTimeout.Duration
	}
	return r.Config.DefaultGlobalTimeout.Duration
}

func (r *MaintenanceRequestReconciler) effectiveDrainTimeout(mr *v1alpha1.MaintenanceRequest) time.Duration {
	if mr.Spec.DrainTimeout.Duration > 0 {
		return mr.Spec.DrainTimeout.Duration
	}
	return r.Config.DefaultDrainTimeout.Duration
}

func (r *MaintenanceRequestReconciler) globalTimedOut(mr *v1alpha1.MaintenanceRequest) bool {
	if mr.Status.StartTime == nil {
		return false
	}
	return time.Since(mr.Status.StartTime.Time) > r.effectiveGlobalTimeout(mr)
}

func (r *MaintenanceRequestReconciler) drainTimedOut(mr *v1alpha1.MaintenanceRequest, ns *v1alpha1.NodeExecutionStatus) bool {
	if ns.StartTime == nil {
		return false
	}
	return time.Since(ns.StartTime.Time) > r.effectiveDrainTimeout(mr)
}

// canPause reports whether a phase can transition to Paused on spec.pause.
func canPause(p v1alpha1.Phase) bool {
	switch p {
	case v1alpha1.PhasePlanned, v1alpha1.PhaseExecuting, v1alpha1.PhaseBlocked, v1alpha1.PhaseAwaitingApproval:
		return true
	default:
		return false
	}
}

