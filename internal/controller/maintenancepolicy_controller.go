package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/yourorg/maintenance-orchestrator/api/v1alpha1"
	"github.com/yourorg/maintenance-orchestrator/internal/config"
	"github.com/yourorg/maintenance-orchestrator/internal/window"
)

// MaintenancePolicyReconciler validates MaintenancePolicy objects and reports
// validity through the Validated status condition. It performs no cluster
// mutation beyond updating its own status.
type MaintenancePolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config *config.Config
}

// +kubebuilder:rbac:groups=maintenance.platform.dev,resources=maintenancepolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=maintenance.platform.dev,resources=maintenancepolicies/status,verbs=get;update;patch

// Reconcile validates the policy spec and writes the Validated condition.
func (r *MaintenancePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pol := &v1alpha1.MaintenancePolicy{}
	if err := r.Get(ctx, req.NamespacedName, pol); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	problems := validatePolicySpec(pol.Spec)
	cond := metav1.Condition{
		Type:               v1alpha1.CondValidated,
		ObservedGeneration: pol.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
	if len(problems) == 0 {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Valid"
		cond.Message = "policy is valid"
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "Invalid"
		cond.Message = strings.Join(problems, "; ")
		logger.Info("invalid MaintenancePolicy", "name", pol.Name, "problems", cond.Message)
	}

	changed := apimeta.SetStatusCondition(&pol.Status.Conditions, cond)
	if pol.Status.ObservedGeneration != pol.Generation {
		pol.Status.ObservedGeneration = pol.Generation
		changed = true
	}
	if changed {
		if err := r.Status().Update(ctx, pol); err != nil {
			return ctrl.Result{}, fmt.Errorf("update MaintenancePolicy status: %w", err)
		}
	}
	return ctrl.Result{}, nil
}

// validatePolicySpec returns a list of human-readable problems, empty when valid.
func validatePolicySpec(spec v1alpha1.MaintenancePolicySpec) []string {
	var problems []string
	if spec.MaxUnavailablePercent < 0 || spec.MaxUnavailablePercent > 100 {
		problems = append(problems, "maxUnavailablePercent must be between 0 and 100")
	}
	if spec.MinCapacityHeadroomPercent < 0 || spec.MinCapacityHeadroomPercent > 100 {
		problems = append(problems, "minCapacityHeadroomPercent must be between 0 and 100")
	}
	now := time.Now()
	for i := range spec.AllowedWindows {
		if _, err := window.IsOpen(spec.AllowedWindows[i], now); err != nil {
			problems = append(problems, fmt.Sprintf("allowedWindows[%d]: %v", i, err))
		}
	}
	return problems
}

// SetupWithManager registers the policy reconciler with the manager.
func (r *MaintenancePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MaintenancePolicy{}).
		Named("maintenancepolicy").
		Complete(r)
}

