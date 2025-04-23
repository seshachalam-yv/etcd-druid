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

func (r *Reconciler) updateTaskStatusFromStep(ctx tasks.TaskContext, taskObjKey client.ObjectKey, stepName string, result ctrlutils.ReconcileStepResult) error {
    task := &v1alpha1.EtcdOperatorTask{}
    if err := r.client.Get(ctx.Context, taskObjKey, task); err != nil {
        return err
    }

    // Create a copy to modify
    updated := task.DeepCopy()
    now := metav1.Now()
    needsUpdate := false

    // Handle errors
    if result.HasErrors() {
        if updated.Status.LastErrors == nil {
            updated.Status.LastErrors = []v1alpha1.LastError{}
        }
        
        // Keep only the most recent errors (e.g., last 5)
        if len(updated.Status.LastErrors) >= 5 {
            updated.Status.LastErrors = updated.Status.LastErrors[1:]
        }
        
        for _, err := range result.GetErrors() {
            updated.Status.LastErrors = append(updated.Status.LastErrors, v1alpha1.LastError{
                Code:        v1alpha1.ErrorCode(stepName), // Or a more specific error code
                Description: err.Error(),
                ObservedAt:  now,
            })
        }
        needsUpdate = true
    }

    // Handle operation status
	// TODO: Handle name conflicts with the ETCD LastOperation.
    if result.GetDescription() != "" {
        operationState := v1alpha1.OperationStateCompleted
        if result.HasErrors() {
            operationState = v1alpha1.OperationStateFailed
        } else if stepName == "executeTask" {
            operationState = v1alpha1.OperationStateInProgress
        }

        updated.Status.LastOperation = &v1alpha1.LastOperation{
            Name: v1alpha1.OpsName(stepName),
            State:  operationState,
            LastTransitionTime: now,
            Reason:            result.GetDescription(),
        }
        needsUpdate = true
    }

    // Only update if there are changes. Needs cahnges to make it more readable.
    if needsUpdate && !equality.Semantic.DeepEqual(task.Status, updated.Status) {
        return r.client.Status().Update(ctx.Context, updated)
    }
    
    return nil
}