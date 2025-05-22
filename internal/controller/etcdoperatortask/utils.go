package etcdoperatortask

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
)

// getTask fetches the EtcdOperatorTask resource for the given object key.
//
// Returns (nil, nil) if the resource is not found (i.e., has been deleted),
// or the task and error otherwise. This is useful for distinguishing between
// not-found and other error conditions in reconciliation logic.
func (r *Reconciler) getTask(ctx context.Context, taskObjKey client.ObjectKey) (*v1alpha1.EtcdOperatorTask, error) {
	task := &v1alpha1.EtcdOperatorTask{}
	err := r.client.Get(ctx, taskObjKey, task)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Return error if it's not a NotFound error
			return nil, err
		}
		// Not found: return (nil, nil)
		return nil, nil
	}
	return task, nil
}

// recordLastOperation sets or updates the LastOperation field in the task status.
//
// If LastOperation is nil, it initializes it with the given phase and state.
// If the phase or state changes, it updates them and sets LastTransitionTime to now.
// The Description is always updated to reflect the current operation.
//
// Returns an error if the status update fails.
func (r *Reconciler) recordLastOperation(ctx context.Context, taskObjKey client.ObjectKey, phase v1alpha1.OperationPhase, state v1alpha1.OperationState) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		task, err := r.getTask(ctx, taskObjKey)
		if err != nil {
			return err
		}
		now := &metav1.Time{Time: time.Now().UTC()}
		desc := fmt.Sprintf("%s is in state %s for task %s", phase, state, taskObjKey.Name)

		if task.Status.LastOperation == nil {
			// Initialize LastOperation if not present
			task.Status.LastOperation = &v1alpha1.EtcdOperatorLastOperation{
				Phase:              phase,
				State:              state,
				LastTransitionTime: now,
				Description:        desc,
			}
			return r.client.Status().Update(ctx, task)
		}

		phaseChanged := task.Status.LastOperation.Phase != phase
		stateChanged := task.Status.LastOperation.State != state
		if phaseChanged {
			task.Status.LastOperation.Phase = phase
			task.Status.LastOperation.LastTransitionTime = now
		}
		if stateChanged {
			task.Status.LastOperation.State = state
		}
		if phaseChanged || stateChanged {
			// Only update status if there was a transition
			task.Status.LastOperation.Description = desc
			return r.client.Status().Update(ctx, task)
		}
		return nil
	})
}

// recordTaskState sets the task's status.State to the given state and updates LastTransitionTime if the state changes.
//
// If transitioning to InProgress, sets InitiatedAt if not already set.
// Returns an error if the status update fails, or nil if no change is needed.
func (r *Reconciler) recordTaskState(ctx context.Context, taskObjKey client.ObjectKey, state v1alpha1.TaskState) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		task, err := r.getTask(ctx, taskObjKey)
		if err != nil {
			return err
		}

		stateChanged := task.Status.State == nil || *task.Status.State != state
		if !stateChanged {
			// No update needed if state is unchanged
			return nil
		}

		now := &metav1.Time{Time: time.Now().UTC()}
		if state == v1alpha1.TaskStateInProgress && task.Status.InitiatedAt == nil {
			// Set InitiatedAt only on first transition to InProgress
			task.Status.InitiatedAt = now
		}
		task.Status.State = &state
		task.Status.LastTransitionTime = now
		return r.client.Status().Update(ctx, task)
	})
}

// recordLastError appends an error to the LastErrors field in the task status.
//
// Maintains a maximum of 10 most recent errors (FIFO order: oldest errors are dropped).
// Returns an error if the status update fails.
func (r *Reconciler) recordLastError(ctx context.Context, taskObjKey client.ObjectKey, err error) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		task, getErr := r.getTask(ctx, taskObjKey)
		if getErr != nil {
			return getErr
		}
		now := &metav1.Time{Time: time.Now().UTC()}
		lastErrors := task.Status.LastErrors
		if lastErrors == nil {
			lastErrors = make([]v1alpha1.EtcdOperatorTaskLastError, 0, 10)
		}
		if len(lastErrors) >= 10 {
			// Remove oldest error to maintain a max of 10
			lastErrors = lastErrors[1:]
		}
		lastErrors = append(lastErrors, v1alpha1.EtcdOperatorTaskLastError{
			Description: err.Error(),
			ObservedAt:  *now,
		})
		task.Status.LastErrors = lastErrors
		return r.client.Status().Update(ctx, task)
	})
}
