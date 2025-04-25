package etcdoperatortask

import (
	"context"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/operatortask"
	"github.com/gardener/etcd-druid/internal/tasks"
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
		// r.recordReconcileStartOperation,
		r.ensureFinalizer,
		r.updateObservedGeneration,
		r.moveTaskToPending,
		r.Admit,
		r.moveTaskToInProgress,
		r.executeTask,
	}

	for _, step := range reconcileStepFns {
		logger.Info("Executing step", "step", step)
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

func (r *Reconciler) executeTask(
	ctx tasks.TaskContext,
	taskObjKey client.ObjectKey,
	executor tasks.TaskExecutor,
) ctrlutils.ReconcileStepResult {

	task := &v1alpha1.EtcdOperatorTask{}
	if result := r.getTask(ctx, taskObjKey, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result
	}
	// Call the executor
	ctx.Logger.Info("Executing task", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
	completed, opStatus, err := executor.Execute(ctx, task)
	if err != nil {
		ctx.Logger.Error(err, "Task execution failed")

		task.Status.State = v1alpha1.TaskStateFailed
		task.Status.LastErrors = append(task.Status.LastErrors, v1alpha1.EtcdOperatorTaskLastError{
			Code:        "",
			Description: err.Error(),
			ObservedAt:  metav1.Now(),
		})

		if opStatus != nil && opStatus.LastOperation != "" {
			task.Status.LastOperation = &v1alpha1.EtcdOperatorLastOperation{
				Name:               opStatus.LastOperation,
				State:              v1alpha1.OperationStateFailed,
				LastTransitionTime: metav1.Now(),
				Reason:             err.Error(),
			}
		}

		// Update the CR status
		if updateErr := r.client.Status().Update(ctx.Context, task); updateErr != nil {
			ctx.Logger.Error(updateErr, "Failed to update Task status after error")
			return ctrlutils.ReconcileWithError(updateErr)
		}

		// Return early
		return ctrlutils.DoNotRequeue()
	}

	// No error from sidecar call
	if opStatus != nil {
		if opStatus.LastOperation != "" {
			task.Status.LastOperation = &v1alpha1.EtcdOperatorLastOperation{
				Name:               opStatus.LastOperation,
				State:              v1alpha1.OperationStateCompleted,
				LastTransitionTime: metav1.Now(),
			}
		}

		if opStatus.LastError != "" {
			task.Status.LastErrors = append(task.Status.LastErrors, v1alpha1.EtcdOperatorTaskLastError{
				Code:        "",
				Description: opStatus.LastError,
				ObservedAt:  metav1.Now(),
			})
		}
	}

	if completed {
		ctx.Logger.Info("Task execution completed successfully")
		task.Status.State = v1alpha1.TaskStateSucceeded
	} else {
		ctx.Logger.Info("Task execution in progress; will requeue to check again")
		task.Status.State = v1alpha1.TaskStateInProgress
	}

	// Update the CR status with partial or final outcome
	if err := r.client.Status().Update(ctx.Context, task); err != nil {
		ctx.Logger.Error(err, "Failed to update Task status")
		return ctrlutils.ReconcileWithError(err)
	}

	// Decide if we need to requeue
	if completed {
		return ctrlutils.ContinueReconcile()
	} else {
		// Requeue in X seconds to check the snapshot status again
		return ctrlutils.ReconcileAfter(10*time.Second, "Task execution in progress")
	}
}
