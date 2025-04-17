package etcdoperatortask

import (
	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"

	"github.com/go-logr/logr"
)

// updateTaskStatusWithError updates the status of the EtcdOperatorTask with an error reason/message.
func (r *Reconciler) updateTaskStatusWithError(log logr.Logger, task *v1alpha1.EtcdOperatorTask, reason, message string) ctrlutils.ReconcileStepResult {
	log.Info("Updating task status with error", "reason", reason, "message", message)
	// Update status logic here...
	return ctrlutils.ContinueReconcile()
}
