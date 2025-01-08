/*
Copyright 2022.
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

//lint:file-ignore U1000 Linter claims functions unused, but are required for generic

package bpfmanoperator

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"

	bpfmaniov1alpha1 "github.com/bpfman/bpfman-operator/apis/v1alpha1"
	"github.com/bpfman/bpfman-operator/internal"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

//+kubebuilder:rbac:groups=bpfman.io,resources=tracepointprograms,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=bpfman.io,resources=tracepointprograms/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=bpfman.io,resources=tracepointprograms/finalizers,verbs=update

type TracepointProgramReconciler struct {
	ClusterProgramReconciler
}

func (r *TracepointProgramReconciler) getRecCommon() *ReconcilerCommon[bpfmaniov1alpha1.BpfProgram, bpfmaniov1alpha1.BpfProgramList] {
	return &r.ClusterProgramReconciler.ReconcilerCommon
}

func (r *TracepointProgramReconciler) getFinalizer() string {
	return internal.TracepointProgramControllerFinalizer
}

// SetupWithManager sets up the controller with the Manager.
func (r *TracepointProgramReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bpfmaniov1alpha1.TracepointProgram{}).
		// Watch bpfPrograms which are owned by TracepointPrograms
		Watches(
			&bpfmaniov1alpha1.BpfProgram{},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(predicate.And(statusChangedPredicateCluster(), internal.BpfProgramTypePredicate(internal.Tracepoint.String()))),
		).
		Complete(r)
}

func (r *TracepointProgramReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Logger = ctrl.Log.WithName("tracepoint")
	r.Logger.Info("bpfman-operator enter: tracepoint", "Name", req.NamespacedName.Name)

	tracepointProgram := &bpfmaniov1alpha1.TracepointProgram{}
	if err := r.Get(ctx, req.NamespacedName, tracepointProgram); err != nil {
		// list all TracepointProgram objects with
		if errors.IsNotFound(err) {
			bpfProgram := &bpfmaniov1alpha1.BpfProgram{}
			if err := r.Get(ctx, req.NamespacedName, bpfProgram); err != nil {
				if errors.IsNotFound(err) {
					r.Logger.V(1).Info("bpfProgram not found stale reconcile, exiting", "Name", req.NamespacedName)
				} else {
					r.Logger.Error(err, "failed getting bpfProgram Object", "Name", req.NamespacedName)
				}
				return ctrl.Result{}, nil
			}

			// Get owning TracepointProgram object from ownerRef
			ownerRef := metav1.GetControllerOf(bpfProgram)
			if ownerRef == nil {
				return ctrl.Result{Requeue: false}, fmt.Errorf("failed getting bpfProgram Object owner")
			}

			if err := r.Get(ctx, types.NamespacedName{Namespace: corev1.NamespaceAll, Name: ownerRef.Name}, tracepointProgram); err != nil {
				if errors.IsNotFound(err) {
					r.Logger.Info("Tracepoint Program from ownerRef not found stale reconcile exiting", "Name", req.NamespacedName)
				} else {
					r.Logger.Error(err, "failed getting TracepointProgram Object from ownerRef", "Name", req.NamespacedName)
				}
				return ctrl.Result{}, nil
			}

		} else {
			r.Logger.Error(err, "failed getting TracepointProgram Object", "Name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
	}

	return reconcileBpfProgram(ctx, r, tracepointProgram)
}

func (r *TracepointProgramReconciler) updateStatus(ctx context.Context, _namespace string, name string, cond bpfmaniov1alpha1.ProgramConditionType, message string) (ctrl.Result, error) {
	// Sometimes we end up with a stale TracepointProgram due to races, do this
	// get to ensure we're up to date before attempting a finalizer removal.
	prog := &bpfmaniov1alpha1.TracepointProgram{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: corev1.NamespaceAll, Name: name}, prog); err != nil {
		r.Logger.V(1).Info("failed to get fresh Tracepoint  object...requeuing")
		return ctrl.Result{Requeue: true, RequeueAfter: retryDurationOperator}, nil
	}

	return r.updateCondition(ctx, prog, &prog.Status.Conditions, cond, message)
}
