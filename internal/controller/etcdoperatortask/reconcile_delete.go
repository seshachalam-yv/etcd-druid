package etcdoperatortask

import (
	"context"
	"time"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/operatortask"
	"github.com/go-logr/logr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// triggerDeletionFlow handles deletion and finalizer cleanup for EtcdOperatorTask.
func (r *Reconciler) triggerDeletionFlow(ctx context.Context, operatorTask operatortask.OperatorTask, task *druidv1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult {
	if task.IsCompleted() && !task.IsMarkedForDeletion() {
		if !task.TTLHasExpired() {
			ttl := int64(*task.Spec.TTLSecondsAfterFinished)
			return ctrlutils.ReconcileAfter(time.Duration(ttl)*time.Second, "Task completed, waiting for TTL to expire")
		}
	}

	if !controllerutil.ContainsFinalizer(task, FinalizerName) {
		return ctrlutils.DoNotRequeue()
	}

	deletionStepFns := []reconcileFn{
		r.recordTaskDeletionStartOperation,
		r.cleanupTaskResources,
		r.recordTaskDeletionSuccessOperation,
		r.removeTaskFinalizer,
		r.revmoveTaskIfRequired,
	}
	for _, step := range deletionStepFns {
		result := step(ctx, client.ObjectKeyFromObject(task), operatorTask)
		if ctrlutils.ShortCircuitReconcileFlow(result) {
			return result
		}
	}
	return ctrlutils.DoNotRequeue()
}

// triggerRejectionDeletionFlow handles deletion and finalizer cleanup for rejected EtcdOperatorTask.
func (r *Reconciler) triggerRejectionDeletionFlow(ctx context.Context, task *druidv1alpha1.EtcdOperatorTask, logger logr.Logger) ctrlutils.ReconcileStepResult {
	logger.Info("Triggering rejection deletion flow", "namespace", task.Namespace, "name", task.Name)
	// Only minimal steps needed for rejected tasks; operatorTask may be nil
	if !task.TTLHasExpired() && !task.IsMarkedForDeletion() {
		return ctrlutils.ReconcileAfter(time.Duration(*task.Spec.TTLSecondsAfterFinished)*time.Second, "Task rejected, waiting for TTL to expire")
	}

	// Only remove finalizer and delete the resource
	deletionStepFns := []reconcileFn{
		r.removeTaskFinalizer,
		r.revmoveTaskIfRequired,
	}
	for _, step := range deletionStepFns {
		result := step(ctx, client.ObjectKeyFromObject(task), nil)
		if ctrlutils.ShortCircuitReconcileFlow(result) {
			logger.Info("Short circuiting reconcile flow", "namespace", task.Namespace, "name", task.Name)
			return result
		}
	}
	return ctrlutils.DoNotRequeue()
}

// recordTaskDeletionStartOperation records the start of the deletion operation for the task.
func (r *Reconciler) recordTaskDeletionStartOperation(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &druidv1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	logger.Info("Recording start of task deletion operation", "namespace", task.Namespace, "name", task.Name)
	if r.recorder != nil {
		r.recorder.Eventf(task, "Normal", "DeletionStart", "Started deletion operation for task %s/%s", task.Namespace, task.Name)
	}
	return ctrlutils.ContinueReconcile()
}

// cleanupTaskResources cleans up resources associated with the task.
func (r *Reconciler) cleanupTaskResources(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	logger.Info("Cleaning up resources for task", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	// Implement resource cleanup logic here if needed.
	return ctrlutils.ContinueReconcile()
}

// recordTaskDeletionSuccessOperation records the successful completion of the deletion operation.
func (r *Reconciler) recordTaskDeletionSuccessOperation(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &druidv1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	logger.Info("Recording successful completion of task deletion operation", "namespace", task.Namespace, "name", task.Name)
	if r.recorder != nil {
		r.recorder.Eventf(task, "Normal", "DeletionSuccess", "Successfully completed deletion operation for task %s/%s", task.Namespace, task.Name)
	}
	return ctrlutils.ContinueReconcile()
}

// removeTaskFinalizer removes the finalizer from the task.
func (r *Reconciler) removeTaskFinalizer(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	var logger logr.Logger
	if operatorTask != nil {
		logger = operatorTask.Logger()
	} else {
		logger = r.logger.WithValues("namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	}

	task := &druidv1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	if controllerutil.ContainsFinalizer(task, FinalizerName) {
		logger.Info("Removing finalizer from task", "namespace", task.Namespace, "name", task.Name)
		removeFinalizer(task, FinalizerName)
		if err := r.client.Update(ctx, task); err != nil {
			logger.Error(err, "Failed to remove finalizer from task")
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

// revmoveTaskIfRequired deletes the task object if it is marked for deletion and finalizer has been removed.
func (r *Reconciler) revmoveTaskIfRequired(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	task := &druidv1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	r.logger.Info("Checking if task is marked for deletion and finalizer has been removed", "namespace", task.Namespace, "name", task.Name)
	r.logger.Info("Finalizer removed", "finalizer", controllerutil.ContainsFinalizer(task, FinalizerName))
	if !controllerutil.ContainsFinalizer(task, FinalizerName) {
		if err := r.client.Delete(ctx, task); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}
