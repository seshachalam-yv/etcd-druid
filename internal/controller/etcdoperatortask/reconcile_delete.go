package etcdoperatortask

import (
	"context"
	"time"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/operatortask"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// triggerDeletionFlow handles deletion and finalizer cleanup for EtcdOperatorTask.
func (r *Reconciler) triggerDeletionFlow(ctx context.Context, operatorTask operatortask.OperatorTask, task *druidv1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult {

	if task.IsCompleted() && !task.IsMarkedForDeletion() {
		if !task.TTLHasExpired() {
			return ctrlutils.ReconcileAfter(time.Duration(*task.Spec.TTLSecondsAfterFinished)*time.Second, "Task completed, waiting for TTL to expire")
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
	for _, fn := range deletionStepFns {
		result := fn(ctx, taskObjKey, operatorTask)
		if ctrlutils.ShortCircuitReconcileFlow(result) {
			return r.recordTaskIncompleteDeletionOperation(ctx, logger, taskObjKey, result)
		}
	}
	return ctrlutils.DoNotRequeue()
}
