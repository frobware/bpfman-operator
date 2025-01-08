/*
Copyright 2024.

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

package bpfmanoperator

import (
	"context"
	"fmt"
	"testing"

	bpfmaniov1alpha1 "github.com/bpfman/bpfman-operator/apis/v1alpha1"
	internal "github.com/bpfman/bpfman-operator/internal"
	testutils "github.com/bpfman/bpfman-operator/internal/test-utils"

	"github.com/stretchr/testify/require"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Runs the FentryProgramReconcile test.  If multiCondition == true, it runs it
// with an error case in which the program object has multiple conditions.
func fentryProgramReconcile(t *testing.T, multiCondition bool) {
	var (
		name            = "fakeFentryProgram"
		bytecodePath    = "/tmp/hello.o"
		bpfFunctionName = "fentry_test"
		fakeNode        = testutils.NewNode("fake-control-plane")
		functionName    = "do_unlinkat"
		ctx             = context.TODO()
		bpfProgName     = fmt.Sprintf("%s-%s", name, fakeNode.Name)
	)
	// A FentryProgram object with metadata and spec.
	Fentry := &bpfmaniov1alpha1.FentryProgram{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: bpfmaniov1alpha1.FentryProgramSpec{
			BpfAppCommon: bpfmaniov1alpha1.BpfAppCommon{
				NodeSelector: metav1.LabelSelector{},
				ByteCode: bpfmaniov1alpha1.BytecodeSelector{
					Path: &bytecodePath,
				},
			},
			FentryProgramInfo: bpfmaniov1alpha1.FentryProgramInfo{

				BpfProgramCommon: bpfmaniov1alpha1.BpfProgramCommon{
					BpfFunctionName: bpfFunctionName,
				},
				FunctionName: functionName,
			},
		},
	}

	// The expected accompanying BpfProgram object
	expectedBpfProg := &bpfmaniov1alpha1.BpfProgram{
		ObjectMeta: metav1.ObjectMeta{
			Name: bpfProgName,
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       Fentry.Name,
					Controller: &[]bool{true}[0],
				},
			},
			Labels:     map[string]string{internal.BpfProgramOwner: Fentry.Name, internal.K8sHostLabel: fakeNode.Name},
			Finalizers: []string{internal.FentryProgramControllerFinalizer},
		},
		Spec: bpfmaniov1alpha1.BpfProgramSpec{
			Type: "fentry",
		},
		Status: bpfmaniov1alpha1.BpfProgramStatus{
			Conditions: []metav1.Condition{bpfmaniov1alpha1.BpfProgCondLoaded.Condition()},
		},
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{fakeNode, Fentry, expectedBpfProg}

	// Register operator types with the runtime scheme.
	s := scheme.Scheme
	s.AddKnownTypes(bpfmaniov1alpha1.SchemeGroupVersion, Fentry)
	s.AddKnownTypes(bpfmaniov1alpha1.SchemeGroupVersion, &bpfmaniov1alpha1.BpfProgram{})
	s.AddKnownTypes(bpfmaniov1alpha1.SchemeGroupVersion, &bpfmaniov1alpha1.BpfProgramList{})

	// Create a fake client to mock API calls.
	cl := fake.NewClientBuilder().WithStatusSubresource(Fentry).WithRuntimeObjects(objs...).Build()

	rc := ReconcilerCommon[bpfmaniov1alpha1.BpfProgram, bpfmaniov1alpha1.BpfProgramList]{
		Client: cl,
		Scheme: s,
	}
	cpr := ClusterProgramReconciler{
		ReconcilerCommon: rc,
	}

	// Set development Logger so we can see all logs in tests.
	logf.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true})))

	// Create a FentryProgram object with the scheme and fake client.
	r := &FentryProgramReconciler{ClusterProgramReconciler: cpr}

	// Mock request to simulate Reconcile() being called on an event for a
	// watched resource .
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
		},
	}

	// First reconcile should add the finalzier to the fentryProgram object
	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	// Require no requeue
	require.False(t, res.Requeue)

	// Check the BpfProgram Object was created successfully
	err = cl.Get(ctx, types.NamespacedName{Name: Fentry.Name, Namespace: metav1.NamespaceAll}, Fentry)
	require.NoError(t, err)

	// Check the bpfman-operator finalizer was successfully added
	require.Contains(t, Fentry.GetFinalizers(), internal.BpfmanOperatorFinalizer)

	// NOTE: THIS IS A TEST FOR AN ERROR PATH. THERE SHOULD NEVER BE MORE THAN
	// ONE CONDITION.
	if multiCondition {
		// Add some random conditions and verify that the condition still gets
		// updated correctly.
		meta.SetStatusCondition(&Fentry.Status.Conditions, bpfmaniov1alpha1.ProgramDeleteError.Condition("bogus condition #1"))
		if err := r.Status().Update(ctx, Fentry); err != nil {
			r.Logger.V(1).Info("failed to set FentryProgram object status")
		}
		meta.SetStatusCondition(&Fentry.Status.Conditions, bpfmaniov1alpha1.ProgramReconcileError.Condition("bogus condition #2"))
		if err := r.Status().Update(ctx, Fentry); err != nil {
			r.Logger.V(1).Info("failed to set FentryProgram object status")
		}
		// Make sure we have 2 conditions
		require.Equal(t, 2, len(Fentry.Status.Conditions))
	}

	// Second reconcile should check bpfProgram Status and write Success condition to tcProgram Status
	res, err = r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	// Require no requeue
	require.False(t, res.Requeue)

	// Check the BpfProgram Object was created successfully
	err = cl.Get(ctx, types.NamespacedName{Name: Fentry.Name, Namespace: metav1.NamespaceAll}, Fentry)
	require.NoError(t, err)

	// Make sure we only have 1 condition now
	require.Equal(t, 1, len(Fentry.Status.Conditions))
	// Make sure it's the right one.
	require.Equal(t, Fentry.Status.Conditions[0].Type, string(bpfmaniov1alpha1.ProgramReconcileSuccess))
}

func TestFentryProgramReconcile(t *testing.T) {
	fentryProgramReconcile(t, false)
}

func TestFentryUpdateStatus(t *testing.T) {
	fentryProgramReconcile(t, true)
}
