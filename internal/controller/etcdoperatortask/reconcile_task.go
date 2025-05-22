package etcdoperatortask

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/task"
)

// reconcileTask manages the lifecycle of an EtcdOperatorTask resource.
// It executes a series of step functions to ensure the task is processed correctly.
func (r *Reconciler) reconcileTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	logger := taskHandler.Logger().WithValues("op", "reconcileTask")
	logger.Info("Triggering task execution flow")
	steps := []StepFunction{
		{StepName: "ensureTaskFinalizer", StepFunc: r.ensureTaskFinalizer},
		{StepName: "transitionToPendingState", StepFunc: r.transitionToPendingState},
		{StepName: "admitTask", StepFunc: r.admitTask},
		{StepName: "transitionToInProgressState", StepFunc: r.transitionToInProgressState},
		{StepName: "runTask", StepFunc: r.runTask},
	}
	for _, step := range steps {
		logger.Info("Executing step", "step", step.StepName)
		result := step.StepFunc(ctx, taskObjKey, taskHandler)
		if ctrlutils.ShortCircuitReconcileFlow(result) {
			logger.Info("Short-circuiting reconciliation", "step", step.StepName, "result", result)
			return result
		}
	}
	logger.Info("Task execution flow completed")
	return ctrlutils.ContinueReconcile()
}

// ensureTaskFinalizer checks if the EtcdOperatorTask has the finalizer.
// If not, it adds the finalizer and updates the task status.
func (r *Reconciler) ensureTaskFinalizer(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	meta := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{
			Kind:       "EtcdOperatorTask",
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
	}
	meta.SetNamespace(taskObjKey.Namespace)
	meta.SetName(taskObjKey.Name)
	if err := r.client.Get(ctx, taskObjKey, meta); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if controllerutil.ContainsFinalizer(meta, FinalizerName) {
		return ctrlutils.ContinueReconcile()
	}
	controllerutil.AddFinalizer(meta, FinalizerName)
	if err := r.client.Update(ctx, meta); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
}

// transitionToPendingState sets the task.status.state to Pending if not already set.
func (r *Reconciler) transitionToPendingState(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}

	if task.Status.State != nil {
		return ctrlutils.ContinueReconcile()
	}
	// Set the task state to Pending
	// and update the LastTransitionTime.
	if err := r.recordTaskState(ctx, taskObjKey, v1alpha1.TaskStatePending); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
}

// admitTask checks if the task is in a pending state.
// If so, it updates the LastOperation to admit and invokes the task handler's Admit method.
// If the admit operation is in progress, it requeues the task.
// If the admit operation fails, it updates the task state to Rejected and sets the LastOperation to failed.
// If the admit operation succeeds, it updates the task.status.state to InProgress.
// If the task is already in a completed state, it skips the admit operation.
func (r *Reconciler) admitTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if task.Status.State != nil && *task.Status.State != v1alpha1.TaskStatePending {
		return ctrlutils.ContinueReconcile()
	}
	if err := r.recordLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseAdmit, v1alpha1.OperationStateInProgress); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	result := taskHandler.Admit(ctx)
	if !result.Completed {
		if result.Error != nil {
			err = r.recordLastError(ctx, taskObjKey, result.Error)
			if err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
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
		err = r.recordLastError(ctx, taskObjKey, result.Error)
		if err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
		if err := r.recordLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseAdmit, v1alpha1.OperationStateFailed); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
		if err := r.recordTaskState(ctx, taskObjKey, v1alpha1.TaskStateRejected); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
		return ctrlutils.ReconcileAfter(task.GetTimeToExpiry(), "Task failed to admit")
	}
	return ctrlutils.ContinueReconcile()
}

// transitionToInProgressState sets the task.status.state to InProgress if not already set.
func (r *Reconciler) transitionToInProgressState(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if task.Status.State != nil && *task.Status.State == v1alpha1.TaskStatePending {
		if err := r.recordTaskState(ctx, taskObjKey, v1alpha1.TaskStateInProgress); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

// runTask executes the task handler's Run method.
// It updates the task status based on the result of the run operation.
// If the task is completed, it updates the LastOperation to completed or failed.
// If the task is still in progress, it requeues the task for further processing.
// If the task fails, it updates the task status to failed and sets the LastOperation to failed.
// If the task succeeds, it updates the task status to succeeded and sets the LastOperation to completed.
// If the task is already in a completed state, it skips the run operation.
func (r *Reconciler) runTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if err := r.recordLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateInProgress); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	result := taskHandler.Run(ctx)

	if result.Completed {
		if result.Error != nil {
			// Task failed
			err = r.recordLastError(ctx, taskObjKey, result.Error)
			if err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
			if err := r.recordLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateFailed); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
			if err := r.recordTaskState(ctx, taskObjKey, v1alpha1.TaskStateFailed); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
		} else {
			// Task succeeded
			if err := r.recordLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateCompleted); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
			if err := r.recordTaskState(ctx, taskObjKey, v1alpha1.TaskStateSucceeded); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
		}

		return ctrlutils.ReconcileAfter(task.GetTimeToExpiry(), "Task completed, waiting for TTL to expire")
	}

	if result.Error != nil {
		err = r.recordLastError(ctx, taskObjKey, result.Error)
		if err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
	}

	requeue := result.RequeueAfter
	if requeue == 0 {
		requeue = r.config.RequeueInterval
	}
	return ctrlutils.ReconcileAfter(requeue, "Task in progress")
}
