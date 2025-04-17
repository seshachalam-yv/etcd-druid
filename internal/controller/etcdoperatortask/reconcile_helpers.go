package etcdoperatortask

import (
	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
)

// hasFinalizer checks if the given finalizer is present on the task.
func hasFinalizer(task *v1alpha1.EtcdOperatorTask, finalizer string) bool {
	for _, f := range task.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

// removeFinalizer removes the given finalizer from the task.
func removeFinalizer(task *v1alpha1.EtcdOperatorTask, finalizer string) {
	var finalizers []string
	for _, f := range task.Finalizers {
		if f != finalizer {
			finalizers = append(finalizers, f)
		}
	}
	task.Finalizers = finalizers
}

// Example helper using ctrlutils
func continueReconcile() ctrlutils.ReconcileStepResult {
	return ctrlutils.ContinueReconcile()
}
