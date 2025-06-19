package etcdopstask

import (
	"context"
	"fmt"
	"testing"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/client/kubernetes"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	druiderr "github.com/gardener/etcd-druid/internal/errors"
	"github.com/gardener/etcd-druid/internal/task"
	"github.com/gardener/etcd-druid/test/utils"

	"github.com/go-logr/logr/testr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

func newTestReconciler(t *testing.T, cl client.Client) *Reconciler {
	return &Reconciler{
		logger: testr.New(t),
		client: cl,
	}
}

func newTestTask(state *v1alpha1.TaskState) *v1alpha1.EtcdOpsTask {
	ts := &v1alpha1.EtcdOpsTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.EtcdOpsTaskSpec{
			Config: v1alpha1.EtcdOpsTaskConfig{},
		},
		Status: v1alpha1.EtcdOpsTaskStatus{},
	}
	if state != nil {
		ts.Status.State = state
	}
	return ts
}

func setupFakeClient(task *v1alpha1.EtcdOpsTask, status bool) client.Client {
	if status {
		return utils.NewTestClientBuilder().
			WithScheme(kubernetes.Scheme).
			WithStatusSubresource(task).
			Build()
	}
	return utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).Build()
}

// TestCleanupTaskResources tests the function testCleanupTaskResources
func TestCleanupTaskResources(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name                  string
		task                  *v1alpha1.EtcdOpsTask
		expectedResult        ctrlutils.ReconcileStepResult
		cleanupFailed         bool
		expectedLastOperation *v1alpha1.EtcdOpsLastOperation
		expectedLastErrors    *v1alpha1.EtcdOpsTaskLastError
	}{
		{
			name:          "Task not found",
			task:          nil,
			cleanupFailed: false,
		},
		{
			name:           "Task is in rejected state, last operation is updated",
			task:           newTestTask(ptr.To(v1alpha1.TaskStateRejected)),
			expectedResult: ctrlutils.ContinueReconcile(),
			expectedLastOperation: &v1alpha1.EtcdOpsLastOperation{
				Phase: v1alpha1.OperationPhaseCleanup,
				State: v1alpha1.OperationStateCompleted,
			},
			cleanupFailed: false,
		},
		{
			name:           "result.Completed is true, no error, last operation is updated",
			task:           newTestTask(nil),
			expectedResult: ctrlutils.ContinueReconcile(),
			expectedLastOperation: &v1alpha1.EtcdOpsLastOperation{
				Phase: v1alpha1.OperationPhaseCleanup,
				State: v1alpha1.OperationStateCompleted,
			},
			cleanupFailed: false,
		}, // TODO: Fix the case below by changing fakeHandler
		// {
		// 	name:           "result.Completed is true, error, last operation and last Error is updated",
		// 	task:           newTestTask(nil),
		// 	expectedResult: ctrlutils.ReconcileWithError(), // TODO: Add the right error/ or rather use error substring and then compare
		// 	cleanupFailed:  true,
		// 	expectedLastOperation: &v1alpha1.EtcdOpsLastOperation{
		// 		Phase: v1alpha1.OperationPhaseCleanup,
		// 		State: v1alpha1.OperationStateFailed,
		// 	},
		// 	expectedLastErrors: &v1alpha1.EtcdOpsTaskLastError{
		// 		Code:        v1alpha1.ErrorCode("TestError"),
		// 		Description: "This is a test error",
		// 	},
		// },
		{
			name:           "result.Completed is false, no error, last operation is not updated",
			task:           newTestTask(nil),
			expectedResult: ctrlutils.ContinueReconcile(),
			cleanupFailed:  false,
		},
		{
			name:           "result.Completed is false, error, last Error is updated",
			task:           newTestTask(nil),
			expectedResult: ctrlutils.ContinueReconcile(),
			cleanupFailed:  true,
			expectedLastErrors: &v1alpha1.EtcdOpsTaskLastError{
				Code:        v1alpha1.ErrorCode("TestError"),
				Description: "This is a test error",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cl client.Client
			if tc.task != nil {
				cl = setupFakeClient(tc.task, true)
				err := cl.Create(context.TODO(), tc.task)
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				cl = setupFakeClient(nil, false)
			}
			reconciler := newTestReconciler(t, cl)
			fakeHandler := utils.NewFakeHandler("test-task", types.NamespacedName{Name: "test-task", Namespace: "test-ns"}, reconciler.logger)
			if tc.cleanupFailed {
				fakeHandler.WithCleanup(&task.Result{
					Completed: false,
					Error:     druiderr.WrapError(fmt.Errorf("test error"), "TestError", "TestOperation", "This is a test error"),
				})
			} else {
				fakeHandler.WithCleanup(&task.Result{
					Completed: true,
				})
			}

			result := reconciler.cleanupTaskResources(context.TODO(), types.NamespacedName{Name: "test-task", Namespace: "test-ns"}, fakeHandler)
			if tc.task == nil {
				g.Expect(result.HasErrors()).To(BeTrue())
				g.Expect(result.GetCombinedError()).To(HaveOccurred())
				g.Expect(result.GetCombinedError().Error()).To(ContainSubstring("not found"))
				return
			}
			g.Expect(result).To(Equal(tc.expectedResult))
			task := &v1alpha1.EtcdOpsTask{}
			err := cl.Get(context.TODO(), types.NamespacedName{Name: "test-task", Namespace: "test-ns"}, task)
			g.Expect(err).ToNot(HaveOccurred())
			if tc.expectedLastOperation != nil {
				g.Expect(task.Status.LastOperation).ToNot(BeNil())
				g.Expect(task.Status.LastOperation.Phase).To(Equal(tc.expectedLastOperation.Phase))
				g.Expect(task.Status.LastOperation.State).To(Equal(tc.expectedLastOperation.State))
			}
			index := len(task.Status.LastErrors) - 1
			if tc.expectedLastErrors != nil {
				g.Expect(task.Status.LastErrors).ToNot(BeNil())
				g.Expect(task.Status.LastErrors[index].Code).To(Equal(v1alpha1.ErrorCode("TestError")))
			} else {
				g.Expect(task.Status.LastErrors).To(BeNil())
			}
		})

	}
}

// TestRemoveTaskFinalizer tests the function removeTaskFinalizer
func TestRemoveTaskFinalizer(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name           string
		task           *v1alpha1.EtcdOpsTask
		expectedResult ctrlutils.ReconcileStepResult
	}{
		{
			name: "Task not found",
			task: nil,
		},
		{
			name: "Task found with finalizer - successfully removed",
			task: &v1alpha1.EtcdOpsTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-task",
					Namespace:  "test-ns",
					Finalizers: []string{FinalizerName, "other-finalizer"},
				},
			},
			expectedResult: ctrlutils.ContinueReconcile(),
		},
		{
			name: "Task found without finalizer",
			task: &v1alpha1.EtcdOpsTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-task",
					Namespace: "test-ns",
				},
			},
			expectedResult: ctrlutils.ContinueReconcile(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cl := setupFakeClient(nil, false)
			if tc.task != nil {
				err := cl.Create(context.TODO(), tc.task)
				g.Expect(err).ToNot(HaveOccurred())
			}

			reconciler := newTestReconciler(t, cl)
			fakeHandler := utils.NewFakeHandler("test-task", types.NamespacedName{Name: "test-task", Namespace: "test-ns"}, reconciler.logger)

			result := reconciler.removeTaskFinalizer(context.TODO(), types.NamespacedName{Name: "test-task", Namespace: "test-ns"}, fakeHandler)
			if tc.task == nil {
				g.Expect(result.HasErrors()).To(BeTrue())
				g.Expect(result.GetCombinedError()).To(HaveOccurred())
				g.Expect(result.GetCombinedError().Error()).To(ContainSubstring("not found"))
				return
			}
			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

// TestRemoveTask tests the function removeTask
func TestRemoveTask(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name           string
		task           *v1alpha1.EtcdOpsTask
		expectedResult ctrlutils.ReconcileStepResult
	}{
		{
			name: "Error getting the task",
			task: nil,
		},
		{
			name: "Successful deletion",
			task: &v1alpha1.EtcdOpsTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-task",
					Namespace: "test-ns",
				},
			},
			expectedResult: ctrlutils.DoNotRequeue(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cl := setupFakeClient(nil, false)
			if tc.task != nil {
				err := cl.Create(context.TODO(), tc.task)
				g.Expect(err).ToNot(HaveOccurred(), "Failed to create test task")
			}

			reconciler := newTestReconciler(t, cl)
			fakeHandler := utils.NewFakeHandler("test-task", types.NamespacedName{Name: "test-task", Namespace: "test-ns"}, reconciler.logger)
			fakeHandler.WithCleanup(&task.Result{Completed: true})

			result := reconciler.removeTask(context.TODO(), types.NamespacedName{Name: "test-task", Namespace: "test-ns"}, fakeHandler)
			if tc.task == nil {
				g.Expect(result.HasErrors()).To(BeTrue())
				g.Expect(result.GetCombinedError()).To(HaveOccurred())
				g.Expect(result.GetCombinedError().Error()).To(ContainSubstring("not found"))
				return
			}
			g.Expect(result).To(Equal(tc.expectedResult), "Expected result does not match")
		})
	}
}
