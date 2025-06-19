package etcdopstask

import (
	"context"
	"fmt"
	"testing"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	druiderr "github.com/gardener/etcd-druid/internal/errors"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

// TestGetTask tests the getTask function.
func TestGetTask(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name        string
		taskName    string
		taskNS      string
		expectError bool
	}{
		{
			name:        "Valid task name and namespace",
			taskName:    "test-task",
			taskNS:      "test-ns",
			expectError: false,
		},
		{
			name:        "Invalid task name",
			taskName:    "",
			taskNS:      "test-ns",
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := setupFakeClient(nil, false)
			task := newTestTask(nil)
			err := cl.Create(context.TODO(), task)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to create test task")
			r := newTestReconciler(t, cl)

			taskKey := client.ObjectKey{
				Name:      tt.taskName,
				Namespace: tt.taskNS,
			}
			taskObj, err := r.getTask(context.TODO(), taskKey)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), fmt.Sprintf("Expected error for task %s in namespace %s", tt.taskName, tt.taskNS))
			} else {
				g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Did not expect error for task %s in namespace %s", tt.taskName, tt.taskNS))
				g.Expect(taskObj.Name).To(Equal(tt.taskName), "Task name should match")
				g.Expect(taskObj.Namespace).To(Equal(tt.taskNS), "Task namespace should match")
			}

		})

	}
}

// TestRecordLastOperation tests the recordLastOperation function.
func TestRecordLastOperation(t *testing.T) {
	tests := []struct {
		name          string
		task          *v1alpha1.EtcdOpsTask
		initialLastOp *v1alpha1.EtcdOpsLastOperation
		phase         v1alpha1.OperationPhase
		state         v1alpha1.OperationState
	}{
		{
			name:          "Previous LastOperation is nil",
			task:          newTestTask(nil),
			initialLastOp: nil,
			phase:         v1alpha1.OperationPhaseAdmit,
			state:         v1alpha1.OperationStateInProgress,
		},
		{
			name: "Phase changed, state unchanged",
			task: newTestTask(nil),
			initialLastOp: &v1alpha1.EtcdOpsLastOperation{
				Phase: v1alpha1.OperationPhaseAdmit,
				State: v1alpha1.OperationStateInProgress,
			},
			phase: v1alpha1.OperationPhaseRunning,
			state: v1alpha1.OperationStateInProgress,
		},
		{
			name: "State changed, phase unchanged",
			task: newTestTask(nil),
			initialLastOp: &v1alpha1.EtcdOpsLastOperation{
				Phase: v1alpha1.OperationPhaseRunning,
				State: v1alpha1.OperationStateInProgress,
			},
			phase: v1alpha1.OperationPhaseRunning,
			state: v1alpha1.OperationStateCompleted,
		},
		{
			name: "Both phase and state changed",
			task: newTestTask(nil),
			initialLastOp: &v1alpha1.EtcdOpsLastOperation{
				Phase: v1alpha1.OperationPhaseRunning,
				State: v1alpha1.OperationStateCompleted,
			},
			phase: v1alpha1.OperationPhaseCleanup,
			state: v1alpha1.OperationStateFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			cl := setupFakeClient(tt.task, true)
			err := cl.Create(context.TODO(), tt.task)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to create test task")
			r := newTestReconciler(t, cl)
			taskKey := client.ObjectKey{
				Name:      tt.task.Name,
				Namespace: tt.task.Namespace,
			}
			if tt.initialLastOp != nil {
				tt.task.Status.LastOperation = tt.initialLastOp
				err = cl.Status().Update(context.TODO(), tt.task)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to update task status with initial LastOperation")
			}
			err = r.recordLastOperation(context.TODO(), taskKey, tt.phase, tt.state, "")
			g.Expect(err).NotTo(HaveOccurred(), "Failed to record last operation")
			updatedTask := &v1alpha1.EtcdOpsTask{}
			err = cl.Get(context.TODO(), taskKey, updatedTask)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to get updated task")
			g.Expect(updatedTask.Status.LastOperation).NotTo(BeNil(), "LastOperation should not be nil")
			g.Expect(updatedTask.Status.LastOperation.Phase).To(Equal(tt.phase), "LastOperation phase should match")
			g.Expect(updatedTask.Status.LastOperation.State).To(Equal(tt.state), "LastOperation state should match")

		})
	}
}

// TestRecordTaskState tests the recordTaskState function.
func TestRecordTaskState(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name         string
		task         *v1alpha1.EtcdOpsTask
		initialState *v1alpha1.TaskState
		state        v1alpha1.TaskState
	}{
		{
			name:         "Initial state is nil",
			task:         newTestTask(nil),
			initialState: nil,
			state:        v1alpha1.TaskStatePending,
		},
		{
			name:         "No overall state change",
			task:         newTestTask(nil),
			initialState: ptr.To(v1alpha1.TaskStateInProgress),
			state:        v1alpha1.TaskStateInProgress,
		},
		{
			name:         "State changed from InProgress to Completed",
			task:         newTestTask(nil),
			initialState: ptr.To(v1alpha1.TaskStateInProgress),
			state:        v1alpha1.TaskStateSucceeded,
		},
		{
			name:         "InitiatedAt is set when transitioning to InProgress",
			task:         newTestTask(nil),
			initialState: ptr.To(v1alpha1.TaskStatePending),
			state:        v1alpha1.TaskStateInProgress,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := setupFakeClient(tc.task, true)
			err := cl.Create(context.TODO(), tc.task)
			g.Expect(err).NotTo(HaveOccurred())
			r := newTestReconciler(t, cl)

			taskKey := client.ObjectKey{
				Name:      tc.task.Name,
				Namespace: tc.task.Namespace,
			}
			if tc.initialState != nil {
				tc.task.Status.State = tc.initialState
				err = cl.Status().Update(context.TODO(), tc.task)
				g.Expect(err).NotTo(HaveOccurred())
			}
			err = r.recordTaskState(context.TODO(), taskKey, tc.state)
			g.Expect(err).NotTo(HaveOccurred())
			updatedTask := &v1alpha1.EtcdOpsTask{}
			err = cl.Get(context.TODO(), taskKey, updatedTask)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(updatedTask.Status.State).NotTo(BeNil())
			g.Expect(*updatedTask.Status.State).To(Equal(tc.state))

			// when transitioning to InProgress, check if InitiatedAt is set
			if tc.state == v1alpha1.TaskStateInProgress && *tc.initialState == v1alpha1.TaskStatePending {
				g.Expect(updatedTask.Status.InitiatedAt).To(Not(BeNil()))
			}
		})
	}
}

// TestRecordLastError tests the recordLastError function.
func TestRecordLastError(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name             string
		task             *v1alpha1.EtcdOpsTask
		initialErrorSize int
		error            error
	}{
		{
			name:             "No initial last error",
			task:             newTestTask(nil),
			initialErrorSize: 0,
			error:            druiderr.WrapError(fmt.Errorf("test error"), "TestError", "TestOperation", "This is a test error"),
		},
		{
			name:             "Initial error exists, but total size is less than 9",
			task:             newTestTask(nil),
			initialErrorSize: 6,
			error:            druiderr.WrapError(fmt.Errorf("another test error"), "AnotherTestError", "TestOperation", "This is another test error"),
		},
		{
			name:             "Initial error exists, total size exceeds 9",
			task:             newTestTask(nil),
			initialErrorSize: 9,
			error:            druiderr.WrapError(fmt.Errorf("yet another test error"), "YetAnotherTestError", "TestOperation", "This is yet another test error"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cl := setupFakeClient(tc.task, true)
			err := cl.Create(context.TODO(), tc.task)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to create test task")
			r := newTestReconciler(t, cl)

			taskKey := client.ObjectKey{
				Name:      tc.task.Name,
				Namespace: tc.task.Namespace,
			}
			if tc.initialErrorSize > 0 {
				for i := range tc.initialErrorSize {
					tc.task.Status.LastErrors = append(tc.task.Status.LastErrors, v1alpha1.EtcdOpsTaskLastError{
						Code:        "InitialError",
						Description: fmt.Sprintf("initial error %d", i),
					})
				}
				err = cl.Status().Update(context.TODO(), tc.task)
				g.Expect(err).NotTo(HaveOccurred())
			}
			err = r.recordLastError(context.TODO(), taskKey, tc.error)
			g.Expect(err).NotTo(HaveOccurred())
			updatedTask := &v1alpha1.EtcdOpsTask{}
			err = cl.Get(context.TODO(), taskKey, updatedTask)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(updatedTask.Status.LastErrors).NotTo(BeEmpty())
			g.Expect(len(updatedTask.Status.LastErrors)).To(BeNumerically("<=", 10))
			// check error code:
			index := len(updatedTask.Status.LastErrors) - 1
			g.Expect(updatedTask.Status.LastErrors[index].Code).To(Equal(tc.error.(*druiderr.DruidError).Code))
			if tc.initialErrorSize >= 9 {
				size := len(updatedTask.Status.LastErrors)
				// size should be 10
				g.Expect(size).To(Equal(10))
			}
		})
	}
}

// TestMapToLastError tests the MapToLastError function.
func TestMapToLastError(t *testing.T) {
	g := NewGomegaWithT(t)
	tests := []struct {
		name        string
		inputErr    error
		wantNil     bool
		wantCode    string
		wantContain string
	}{
		{
			name:     "nil error",
			inputErr: nil,
			wantNil:  true,
		},
		{
			name:     "non-DruidError error",
			inputErr: fmt.Errorf("some generic error"),
			wantNil:  true,
		},
		{
			name:        "DruidError without cause",
			inputErr:    &druiderr.DruidError{Operation: "op", Code: "code", Message: "msg"},
			wantNil:     false,
			wantCode:    "code",
			wantContain: "Operation: op, Code: code message: msg",
		},
		{
			name:        "DruidError with cause",
			inputErr:    &druiderr.DruidError{Operation: "op2", Code: "code2", Message: "msg2", Cause: fmt.Errorf("root cause")},
			wantNil:     false,
			wantCode:    "code2",
			wantContain: "cause: root cause",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MapToLastError(tc.inputErr)
			if tc.wantNil {
				g.Expect(got).To(BeNil())
				return
			}
			g.Expect(got).ToNot(BeNil())
			g.Expect(string(got.Code)).To(Equal(tc.wantCode))
			g.Expect(got.Description).To(ContainSubstring(tc.wantContain))
		})
	}
}
