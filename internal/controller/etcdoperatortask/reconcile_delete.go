package etcdoperatortask

import (
	"context"
	"time"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/task"
	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// triggerDeletionFlow handles the deletion flow for EtcdOperatorTask resources.
func (r *Reconciler) triggerDeletionFlow(ctx context.Context, taskHandler task.Handler, task *v1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult {
	logger := r.logger.WithValues("namespace", task.Namespace, "name", task.Name)
	logger.Info("Starting deletion flow for EtcdOperatorTask")

	// If task is completed but not marked for deletion, wait for TTL expiry
	if task.IsCompleted() {
		if !task.HasTTLExpired() {
			logger.Info("Task completed but TTL not expired yet, will requeue after TTL", "ttlSeconds", ptr.Deref(task.Spec.TTLSecondsAfterFinished, 600))

			return ctrlutils.ReconcileAfter(time.Duration(ptr.Deref(task.Spec.TTLSecondsAfterFinished, 600))*time.Second, "Task completed, waiting for TTL to expire")
		}
		logger.Info("Task TTL expired, proceeding with deletion")
	}

	deletionStepFns := []StepFunction{
		{
			StepName: "CleanupTaskResources",
			StepFunc:   r.cleanupTaskResources,
		},
		{
			StepName: "RemoveTaskFinalizer",
			StepFunc:   r.removeTaskFinalizer,
		},
		{
			StepName: "RemoveTask",
			StepFunc:   r.removeTask,
		},
	}
	for i, fn := range deletionStepFns {
		logger.Info("Executing deletion step", "stepIndex", i)
		result := fn.StepFunc(ctx, client.ObjectKeyFromObject(task), taskHandler)
		if ctrlutils.ShortCircuitReconcileFlow(result) {
			logger.Info("Short-circuiting deletion flow", "stepIndex", i, "result", result)
			return result
		}
	}
	logger.Info("Deletion flow completed for EtcdOperatorTask")
	return ctrlutils.DoNotRequeue()
}

// cleanupTaskResources calls the Cleanup method of the task handler.
func (r *Reconciler) cleanupTaskResources(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	logger := r.logger.WithValues("namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	logger.Info("Cleaning up task resources")
	r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseCleanup, v1alpha1.OperationStateInProgress)
	result := taskHandler.Cleanup(ctx)
	if result != nil && result.Error != nil {
		return ctrlutils.ReconcileWithError(result.Error)
	}
	if result != nil && result.Completed {
		if result.Error != nil {
			r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseCleanup, v1alpha1.OperationStateFailed)
			return ctrlutils.ReconcileWithError(result.Error)
		}
		r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseCleanup, v1alpha1.OperationStateCompleted)
	} else {
	}
	return ctrlutils.ContinueReconcile()
}

// removeTaskFinalizer removes the finalizer from the EtcdOperatorTask resource.
func (r *Reconciler) removeTaskFinalizer(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if controllerutil.ContainsFinalizer(task, FinalizerName) {
		controllerutil.RemoveFinalizer(task, FinalizerName)
		if err := r.client.Update(ctx, task); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

// removeTask deletes the EtcdOperatorTask resource from the cluster.
func (r *Reconciler) removeTask(ctx context.Context, taskObjKey client.ObjectKey, _ task.Handler) ctrlutils.ReconcileStepResult {
	task := &v1alpha1.EtcdOperatorTask{}
	if err := r.client.Get(ctx, taskObjKey, task); err != nil {
		return ctrlutils.ReconcileWithError(client.IgnoreNotFound(err))
	}
	if err := r.client.Delete(ctx, task); err != nil {
		return ctrlutils.ReconcileWithError(client.IgnoreNotFound(err))
	}
	return ctrlutils.DoNotRequeue()
}