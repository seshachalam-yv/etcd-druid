package etcdoperatortask

import (
	"context"
	"fmt"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/task"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// TODO Move this to helper since it'll be used by delete step function as well.
type StepFunction struct {
	StepName string
	StepFunc reconcileFn
}

// reconcileTask manages the lifecycle of an EtcdOperatorTask resource.
func (r *Reconciler) reconcileTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	// Use named step functions for better logging and extensibility
	steps := []StepFunction{
		{StepName: "ensureTaskFinalizer", StepFunc: r.ensureTaskFinalizer},
		{StepName: "transitionToPendingState", StepFunc: r.transitionToPendingState},
		{StepName: "admitTask", StepFunc: r.admitTask},
		{StepName: "transitionToInProgressState", StepFunc: r.transitionToInProgressState},
		{StepName: "runTask", StepFunc: r.runTask},
	}
	logger := taskHandler.Logger()
	for _, step := range steps {
		logger.Info("Executing step", "step", step.StepName)
		result := step.StepFunc(ctx, taskObjKey, taskHandler)
		if ctrlutils.ShortCircuitReconcileFlow(result) {
			logger.Info("Short-circuiting reconciliation", "step", step.StepName, "result", result)
			return result
		}
	}

	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	ttl := time.Duration(ptr.Deref(task.Spec.TTLSecondsAfterFinished, 600)) * time.Second
	logger.Info("Reconciliation complete, requeueing after TTL", "ttl", ttl)
	return ctrlutils.ReconcileAfter(ttl, "Task completed, waiting for TTL to expire")
}

// TODO: Evaluate if partialmeta is enough.
// ensureTaskFinalizer adds the finalizer if not present.
func (r *Reconciler) ensureTaskFinalizer(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	task := &v1alpha1.EtcdOperatorTask{}
	if err := r.client.Get(ctx, taskObjKey, task); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if controllerutil.ContainsFinalizer(task, FinalizerName) {
		return ctrlutils.ContinueReconcile()
	}
	controllerutil.AddFinalizer(task, FinalizerName)
	if err := r.client.Update(ctx, task); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
}

// 1) mark state as pending
// 2) set initiated at
// transitionToPendingState sets the state to Pending if not already set.
func (r *Reconciler) transitionToPendingState(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if task.Status.State != nil {
		return ctrlutils.ContinueReconcile()
	}
	if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStatePending); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
}

// TODO: Come up with implementation for this.
// 1) Set LastOperation {phase: "admit", state: "inProgress", lastTransitionTime: time.Now(), description: "admit process for task {taskName}"}
// 2) Call only if the LastOperation is not set.
// admitTask runs the admission logic for the task.
func (r *Reconciler) admitTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if task.Status.State != nil && *task.Status.State != v1alpha1.TaskStatePending {
		return ctrlutils.ContinueReconcile()
	}
	if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseAdmit, v1alpha1.OperationStateInProgress); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	result := taskHandler.Admit(ctx)
	if !result.Completed {
		if result.Error != nil {
			return ctrlutils.ReconcileWithError(result.Error)
		}
		requeue := result.RequeueAfter
		if requeue == 0 {
			requeue = r.config.RequeueInterval
		}
		return ctrlutils.ReconcileAfter(requeue, "Task admit in progress")
	}
	if result.Error != nil {
		// Admission failed, mark as rejected
		if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseAdmit, v1alpha1.OperationStateFailed); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
		if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateRejected); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
		ttl := time.Duration(ptr.Deref(task.Spec.TTLSecondsAfterFinished, 600)) * time.Second
		return ctrlutils.ReconcileAfter(ttl, "Task failed to admit")
	}
	return ctrlutils.ContinueReconcile()
}

// state -> inProgress
// transitionToInProgressState sets the state to InProgress if currently Pending.
func (r *Reconciler) transitionToInProgressState(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if task.Status.State != nil && *task.Status.State == v1alpha1.TaskStatePending {
		if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateInProgress); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

// TODO: Implement this.
// 1) Set LastOperation {phase: "run", state: "inProgress", lastTransitionTime: time.Now(), description: "running task {taskName}"} only if previous phase was admit. Skip if already set.
// 2) Call the task handler to run the task.
// runTask executes the main logic for the task.
func (r *Reconciler) runTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateInProgress); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	result := taskHandler.Run(ctx)
	if result.Completed {
		if result.Error != nil {
			// Task failed
			if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateFailed); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
			if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateFailed); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
		} else {
			// Task succeeded
			if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateCompleted); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
			if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateSucceeded); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
		}
		ttl := time.Duration(ptr.Deref(task.Spec.TTLSecondsAfterFinished, 600)) * time.Second
		return ctrlutils.ReconcileAfter(ttl, "Task completed, waiting for TTL to expire")
	}
	if result.Error != nil {
		return ctrlutils.ReconcileWithError(result.Error)
	}
	requeue := result.RequeueAfter
	if requeue == 0 {
		requeue = r.config.RequeueInterval
	}
	return ctrlutils.ReconcileAfter(requeue, "Task in progress")
}

// TODO: Move to helper
// Method to get the task object.
// getTask fetches the EtcdOperatorTask resource by key.
func (r *Reconciler) getTask(ctx context.Context, taskObjKey client.ObjectKey) (*v1alpha1.EtcdOperatorTask, error) {
	task := &v1alpha1.EtcdOperatorTask{}
	if err := r.client.Get(ctx, taskObjKey, task); err != nil {
		return nil, err
	}
	return task, nil
}

// function to update the last operation field in the status.
// updateLastOperation updates the last operation status in the task.
func (r *Reconciler) updateLastOperation(ctx context.Context, taskObjKey client.ObjectKey, phase v1alpha1.OperationPhase, state v1alpha1.OperationState) error {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return err
	}
	now := metav1.Now()
	if task.Status.LastOperation == nil {
		task.Status.LastOperation = &v1alpha1.EtcdOperatorLastOperation{
			Phase:              phase,
			State:              state,
			LastTransitionTime: &now,
			Description:        fmt.Sprintf("%s is in state %s for task %s", phase, state, taskObjKey.Name),
		}
	} else {
		changed := false
		if task.Status.LastOperation.Phase != phase {
			task.Status.LastOperation.Phase = phase
			changed = true
		}
		if task.Status.LastOperation.State != state {
			task.Status.LastOperation.State = state
			changed = true
		}
		if changed {
			task.Status.LastOperation.LastTransitionTime = &now
			task.Status.LastOperation.Description = fmt.Sprintf("%s is in state %s for task %s", phase, state, taskObjKey.Name)
		}
	}
	return r.client.Status().Update(ctx, task)
}

// updateState updates the state and sets InitiatedAt if entering InProgress.
func (r *Reconciler) updateState(ctx context.Context, taskObjKey client.ObjectKey, state v1alpha1.TaskState) error {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return err
	}
	var changed bool
	if task.Status.State == nil {
		changed = true
	} else if *task.Status.State != state {
		changed = true
	}
	if !changed {
		return nil
	}
	if state == v1alpha1.TaskStateInProgress && task.Status.InitiatedAt == nil {
		task.Status.InitiatedAt = &metav1.Time{Time: time.Now()}
	}

	task.Status.State = &state
	task.Status.LastTransitionTime = &metav1.Time{Time: time.Now()}
	return r.client.Status().Update(ctx, task)
}

func (r *Reconciler) updateLastError(ctx context.Context, taskObjKey client.ObjectKey, err error) error {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return err
	}
	now := metav1.Now()
	lastErrors := task.Status.LastErrors

	if lastErrors == nil {
		lastErrors = []v1alpha1.EtcdOperatorTaskLastError{}
	}
	if len(lastErrors) >= 10 {
		lastErrors = lastErrors[1:]
	}

	lastErrors = append(lastErrors, v1alpha1.EtcdOperatorTaskLastError{
		// Code:        v1alpha1.ErrorCode(err).String(),
		Description: err.Error(),
		ObservedAt:  now,
	})
	task.Status.LastErrors = lastErrors
	return r.client.Status().Update(ctx, task)
}

// func (r *Reconciler) getLastError(task *v1alpha1.EtcdOperatorTask) druidv1 {
// 	if len(task.Status.LastErrors) == 0 {
// 		return nil
// 	}
// 	return task.Status.LastErrors[0].Error
// }
