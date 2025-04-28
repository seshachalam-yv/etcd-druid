package etcdoperatortask

import (
	"context"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/operatortask"
	"github.com/gardener/etcd-druid/internal/utils/kubernetes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// reconcileTask manages preconditions, execution, and status updates.
func (r *Reconciler) reconcileTask(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	logger.Info("Reconciling task", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	reconcileStepFns := []reconcileFn{
		r.recordReconcileStartOperation,
		r.ensureFinalizer,
		r.moveTaskToPending,
		r.Admit,
		r.moveTaskToInProgress,
		r.updateObservedGeneration,
		r.executeTask,
	}

	for _, step := range reconcileStepFns {
		result := step(ctx, taskObjKey, operatorTask)
		if ctrlutils.ShortCircuitReconcileFlow(result) {
			return result
		}
	}

	logger.Info("Task execution completed", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	return ctrlutils.ReconcileAfter(r.config.RequeueInterval, "Task execution in progress")
}

func (r *Reconciler) ensureFinalizer(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	logger.Info("Ensuring finalizer", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	taskPartialObjMeta := ctrlutils.EmptyEtcdOperatorTaskPartialObjectMetadata()
	if result := ctrlutils.GetLatestEtcdOperatorTaskPartialObjectMeta(ctx, r.client, taskObjKey, taskPartialObjMeta); ctrlutils.ShortCircuitReconcileFlow(result) {
		logger.Info("While ensuring finalizer, task not found", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name, "error", result.GetCombinedError())
		return result
	}
	if !controllerutil.ContainsFinalizer(taskPartialObjMeta, FinalizerName) {
		logger.Info("Adding finalizer", "finalizerName", FinalizerName)
		if err := kubernetes.AddFinalizers(ctx, r.client, taskPartialObjMeta, FinalizerName); err != nil {
			logger.Error(err, "failed to add finalizer")
			return ctrlutils.ReconcileWithError(err)
		}
	}
	logger.Info("Finalizer added", "finalizerName", FinalizerName)
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) getTask(ctx context.Context, taskObjKey client.ObjectKey, task *v1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult {

	if err := r.client.Get(ctx, taskObjKey, task); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrlutils.DoNotRequeue()
		}
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) admit(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	logger.Info("Checking preconditions for task", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	if task.Status.State == v1alpha1.TaskStateInProgress {
		return ctrlutils.ContinueReconcile()
	}

	result := operatorTask.Admit(ctx)
	if result.Error != nil {
		logger.Error(result.Error, "Preconditions not met")
		return ctrlutils.ReconcileWithError(result.Error)
	}

	logger.Info("Preconditions met, starting task execution")

	task.Status.State = v1alpha1.TaskStateInProgress
	task.Status.InitiatedAt = metav1.Now()
	task.Status.ObservedGeneration = &task.Generation

	if err := r.client.Status().Update(ctx, task); err != nil {

		logger.Error(err, "Failed to update status to InProgress")
		return ctrlutils.ReconcileWithError(err)
	}

	logger.Info("Preconditions met, starting task execution")
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) recordReconcileStartOperation(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	logger.Info("Recording start of reconcile operation", "namespace", task.Namespace, "name", task.Name)
	if r.recorder != nil {
		r.recorder.Eventf(task, "Normal", "ReconcileStart", "Started reconcile operation for task %s/%s", task.Namespace, task.Name)
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) updateObservedGeneration(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	if task.Status.ObservedGeneration == nil || *task.Status.ObservedGeneration != task.Generation {
		gen := task.Generation
		task.Status.ObservedGeneration = &gen
		if err := r.client.Status().Update(ctx, task); err != nil {
			logger.Error(err, "Failed to update ObservedGeneration")
			return ctrlutils.ReconcileWithError(err)
		}
		logger.Info("Updated ObservedGeneration", "generation", gen)
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) moveTaskToPending(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	if task.Status.State != v1alpha1.TaskStatePending {
		logger.Info("Moving task to Pending state", "namespace", task.Namespace, "name", task.Name)
		task.Status.State = v1alpha1.TaskStatePending
		// Ensure InitiatedAt is set to a non-zero value to satisfy CRD validation
		if task.Status.InitiatedAt.IsZero() {
			task.Status.InitiatedAt = metav1.Now()
		}
		if err := r.client.Status().Update(ctx, task); err != nil {
			logger.Error(err, "Failed to move task to Pending state")
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) Admit(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	result := operatorTask.Admit(ctx)
	if result.Error != nil {
		logger.Error(result.Error, "Preconditions not met")
		return ctrlutils.ReconcileWithError(result.Error)
	}
	logger.Info("Preconditions met, starting task execution")
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) moveTaskToInProgress(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	if task.Status.State != v1alpha1.TaskStateInProgress {
		logger.Info("Moving task to InProgress state", "namespace", task.Namespace, "name", task.Name)
		task.Status.State = v1alpha1.TaskStateInProgress
		task.Status.InitiatedAt = metav1.Now()
		if err := r.client.Status().Update(ctx, task); err != nil {
			logger.Error(err, "Failed to move task to InProgress state")
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) executeTask(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult {
	logger := operatorTask.Logger()
	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	result := operatorTask.Run(ctx)
	if result.Description != "" {
		task.Status.LastOperation = &v1alpha1.EtcdOperatorLastOperation{
			State:              v1alpha1.OperationStateInProgress,
			Description:        result.Description,
			LastTransitionTime: metav1.Now(),
		}
		if result.Completed {
			task.Status.LastOperation.State = v1alpha1.OperationStateCompleted
		}
	}
	if result.Error != nil {
		logger.Error(result.Error, "Task execution failed")
		task.Status.LastErrors = append(task.Status.LastErrors, v1alpha1.EtcdOperatorTaskLastError{
			Code:        "",
			Description: result.Error.Error(),
			ObservedAt:  metav1.Now(),
		})
		if err := r.client.Status().Update(ctx, task); err != nil {
			logger.Error(err, "Failed to update Task status after error")
			return ctrlutils.ReconcileWithError(err)
		}
		return ctrlutils.DoNotRequeue()
	}

	if result.Completed {
		logger.Info("Task execution completed successfully")
		if result.Error != nil {
			task.Status.State = v1alpha1.TaskStateFailed
		} else {
			task.Status.State = v1alpha1.TaskStateSucceeded
		}
	} else {
		logger.Info("Task execution in progress; will requeue to check again")
		task.Status.State = v1alpha1.TaskStateInProgress
	}
	if err := r.client.Status().Update(ctx, task); err != nil {
		logger.Error(err, "Failed to update Task status")
		return ctrlutils.ReconcileWithError(err)
	}
	if result.Completed {
		ttl := int64(*task.Spec.TTLSecondsAfterFinished)
		return ctrlutils.ReconcileAfter(time.Duration(ttl)*time.Second, "Task execution completed successfully")
	} else {
		return ctrlutils.ReconcileAfter(10*time.Second, "Task execution in progress")
	}
}
