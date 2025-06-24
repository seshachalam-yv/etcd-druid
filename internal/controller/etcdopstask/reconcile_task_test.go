package etcdopstask

import (
	"context"
	"fmt"
	"testing"
	// "time"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"

	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/task"
	testutils "github.com/gardener/etcd-druid/test/utils"

	. "github.com/onsi/gomega"
	// v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)


// TestEnsureFinalizer tests the ensureTaskFinalizer step function
func TestEnsureFinalizer(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name              string
		task              *druidv1alpha1.EtcdOpsTask
		expectedResult    ctrlutils.ReconcileStepResult
		ContainsFinalizer bool
	}{
		{
			name:              "Finalizer already exists",
			task:              newTestTask(nil),
			expectedResult:    ctrlutils.ContinueReconcile(),
			ContainsFinalizer: true,
		},
		{
			name:              "Finalizer does not exist, add finalizer",
			task:              newTestTask(nil),
			expectedResult:    ctrlutils.ContinueReconcile(),
			ContainsFinalizer: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := setupFakeClient(tc.task, false)
			r := newTestReconciler(t, cl)

			err := cl.Create(context.TODO(), tc.task)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to create task")
			if tc.ContainsFinalizer {
				controllerutil.AddFinalizer(tc.task, FinalizerName)
				err = cl.Update(context.TODO(), tc.task)
				if err != nil {
					fmt.Println("HEre :(")
				}
				g.Expect(err).ToNot(HaveOccurred(), "Failed to update task with finalizer")
			}

			taskObjKey := client.ObjectKeyFromObject(tc.task)
			result := r.ensureTaskFinalizer(context.TODO(), taskObjKey, nil)
			fmt.Println(result)

			g.Expect(result).To(Equal(tc.expectedResult))

			// Verify finalizer state after the operation
			updatedTask := &druidv1alpha1.EtcdOpsTask{}
			err = cl.Get(context.TODO(), taskObjKey, updatedTask)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(controllerutil.ContainsFinalizer(updatedTask, FinalizerName)).To(BeTrue())
		})
	}
}

// TestTransitionToPendingState tests the transitionToPendingState step function.
func TestTransitionToPendingState(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name           string
		task           *druidv1alpha1.EtcdOpsTask
		expectedResult ctrlutils.ReconcileStepResult
	}{
		{
			name:           "Task state is not nil, i.e either Pending or InProgress",
			task:           newTestTask(ptrState(druidv1alpha1.TaskStatePending)),
			expectedResult: ctrlutils.ContinueReconcile(),
		},
		{
			name:           "Task state is nil, should transition to Pending",
			task:           newTestTask(nil),
			expectedResult: ctrlutils.ContinueReconcile(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cl := setupFakeClient(tc.task, true)
			r := newTestReconciler(t, cl)
			err := cl.Create(context.TODO(), tc.task)
			g.Expect(err).To(BeNil())

			result := r.transitionToPendingState(context.TODO(), client.ObjectKeyFromObject(tc.task), nil)
			updatedTask := &druidv1alpha1.EtcdOpsTask{}
			err = cl.Get(context.TODO(), client.ObjectKeyFromObject(tc.task), updatedTask)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(result).To(Equal(tc.expectedResult))

			if tc.task.Status.State == nil {
				g.Expect(updatedTask.Status.State).ToNot(BeNil())
				g.Expect(*updatedTask.Status.State).To(Equal(druidv1alpha1.TaskStatePending))
			} else {
				g.Expect(updatedTask.Status.State).ToNot(BeNil())
				g.Expect(*updatedTask.Status.State).To(Equal(*tc.task.Status.State))
			}
		})
	}
}
// Cases :
// 1) If the 
// func TestAdmitTask(t *testing.T) {
// 	g := NewGomegaWithT(t)
// 	scheme := runtime.NewScheme()
// 	_ = druidv1alpha1.AddToScheme(scheme)

// 	tests := []struct{
// 		name string
// 		task *druidv1alpha1.EtcdOpsTask
// 		expectedResult ctrlutils.ReconcileStepResult
// 		expectedLastOperation *druidv1alpha1.EtcdOpsLastOperation
// 		expectedState *druidv1alpha1.OperationState
// 	}{

// 	}
	
// }

func TestTransitionToInProgressState(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name           string
		task           *druidv1alpha1.EtcdOpsTask
		expectedResult ctrlutils.ReconcileStepResult
	}{
		{
			name:           "Task state is not 'Pending', skipping state updation",
			task:           newTestTask(ptrState(druidv1alpha1.TaskStateInProgress)),
			expectedResult: ctrlutils.ContinueReconcile(),
		},
		{
			name:           "Task state is nil, skipping state updation",
			task:           newTestTask(nil),
			expectedResult: ctrlutils.ContinueReconcile(),
		},
		{
			name:           "Task state is 'Pending'. Update to 'InProgress'",
			task:           newTestTask(ptrState(druidv1alpha1.TaskStatePending)),
			expectedResult: ctrlutils.ContinueReconcile(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cl := setupFakeClient(tc.task, true)
			r := newTestReconciler(t, cl)
			err := cl.Create(context.TODO(), tc.task)
			g.Expect(err).To(BeNil())

			handler := testutils.NewFakeHandler("test-handler", client.ObjectKeyFromObject(tc.task), r.logger)
			result := r.transitionToInProgressState(context.TODO(), client.ObjectKeyFromObject(tc.task), handler)
			updatedTask := &druidv1alpha1.EtcdOpsTask{}
			err = cl.Get(context.TODO(), client.ObjectKeyFromObject(tc.task), updatedTask)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(result).To(Equal(tc.expectedResult))

			if tc.task.Status.State == nil {
				g.Expect(updatedTask.Status.State).To(BeNil())
			} else if *tc.task.Status.State != druidv1alpha1.TaskStatePending {
				g.Expect(*updatedTask.Status.State).To(Equal(*tc.task.Status.State))
			} else {
				g.Expect(updatedTask.Status.State).ToNot(BeNil())
				g.Expect(*updatedTask.Status.State).To(Equal(druidv1alpha1.TaskStateInProgress))
			}
		})
	}
}

// expectedStatusFields returns the expected State, LastOperation, and LastError for a given test scenario.
func expectedStatusFields(state *druidv1alpha1.TaskState, mockResult *task.Result) (*druidv1alpha1.TaskState, *druidv1alpha1.EtcdOpsLastOperation, *druidv1alpha1.EtcdOpsTaskLastError) {
	phase := druidv1alpha1.OperationPhaseAdmit
	desc := "Admit"
	if state != nil && *state == druidv1alpha1.TaskStatePending {
		if mockResult == nil {
			// nil result is error: rejected
			return ptrState(druidv1alpha1.TaskStateRejected), &druidv1alpha1.EtcdOpsLastOperation{
				Phase:       phase,
				State:       druidv1alpha1.OperationStateFailed,
				Description: desc,
			}, &druidv1alpha1.EtcdOpsTaskLastError{Description: "admit returned nil TaskResult"}
		} else if mockResult.Completed {
			// completed without error: in progress
			return ptrState(druidv1alpha1.TaskStateInProgress), &druidv1alpha1.EtcdOpsLastOperation{
				Phase:       phase,
				State:       druidv1alpha1.OperationStateInProgress,
				Description: desc,
			}, nil
		} else if mockResult.Error != nil {
			// error and not completed: rejected
			return ptrState(druidv1alpha1.TaskStateRejected), &druidv1alpha1.EtcdOpsLastOperation{
				Phase:       phase,
				State:       druidv1alpha1.OperationStateFailed,
				Description: desc,
			}, &druidv1alpha1.EtcdOpsTaskLastError{Description: mockResult.Error.Error()}
		} else {
			// in progress, not completed, no error: stays pending
			return ptrState(druidv1alpha1.TaskStatePending), &druidv1alpha1.EtcdOpsLastOperation{
				Phase:       phase,
				State:       druidv1alpha1.OperationStateInProgress,
				Description: desc,
			}, nil
		}
	}
	// Not pending: should not change state, and LastOperation should remain nil
	return ptrState(druidv1alpha1.TaskStateInProgress), nil, nil
}

// contains returns true if substr is in s.
func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && (contains(s[1:], substr) || contains(s[:len(s)-1], substr))))) || (len(s) > 0 && contains(s[1:], substr))
}

// ptrState returns a pointer to the given TaskState.
func ptrState(s druidv1alpha1.TaskState) *druidv1alpha1.TaskState {
	return &s
}
