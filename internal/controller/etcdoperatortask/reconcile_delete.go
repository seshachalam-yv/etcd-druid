package etcdoperatortask

import (
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/tasks"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// reconcileEtcdOperatorTaskDeletion handles deletion and finalizer cleanup for EtcdOperatorTask.
func (r *Reconciler) reconcileEtcdOperatorTaskDeletion(ctx tasks.TaskContext, taskExecutor tasks.TaskExecutor, task *druidv1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult {

	if task.DeletionTimestamp == nil {
		return ctrlutils.ContinueReconcile()
	}

	if !controllerutil.ContainsFinalizer(task, FinalizerName) {
		return ctrlutils.DoNotRequeue()
	}

	ctx.Logger.Info("Task marked for deletion, performing cleanup")
	if err := taskExecutor.Cleanup(ctx, task); err != nil {
		ctx.Logger.Error(err, "Cleanup failed")
		return ctrlutils.ReconcileWithError(err)
	}

	controllerutil.RemoveFinalizer(task, FinalizerName)
	if err := r.client.Update(ctx.Context, task); err != nil {
		ctx.Logger.Error(err, "Failed to remove finalizer")
		return ctrlutils.ReconcileWithError(err)
	}
	ctx.Logger.Info("Finalizer removed")
	return ctrlutils.ContinueReconcile()
}
