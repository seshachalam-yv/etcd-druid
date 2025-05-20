package etcdoperatortask

import (
	"context"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/task"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// TODO Move this to helper since it'll be used by delete step function as well.
type StepFunction struct {
	StepName string
	StepFunc reconcileFn
}

func (r *Reconciler) reconcileTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	// TODO: change this to use the struct since loggign for steps will be easier.
    reconcileStepFns := []reconcileFn{ // map[string]reconcileFn
        r.ensureTaskFinalizer,
        r.transitionToPendingState, // updates the status field. Skipped if already done.
        r.admitTask, // calls the implemented interface method. Run only at the first time.
        r.transitionToInProgressState, // updates the status. Skipped if already marked.
        r.runTask, // calls the implemented interface method.
    }
	logger := taskHandler.Logger()
    for _, step := range reconcileStepFns {
		logger.V(1).Info("Executing step", "step", step)
        result := step(ctx, taskObjKey, taskHandler)
        if ctrlutils.ShortCircuitReconcileFlow(result) {
            return result
        }
    }

	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}

    return ctrlutils.ReconcileAfter(time.Duration(*task.Spec.TTLSecondsAfterFinished), "Task completed, waiting for TTL to expire")
}

// TODO: Evaluate if partialmeta is enough.
func (r *Reconciler) ensureTaskFinalizer(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task := &v1alpha1.EtcdOperatorTask{}
	if err := r.client.Get(ctx, taskObjKey, task); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}

	if controllerutil.ContainsFinalizer(task, FinalizerName) {
		return ctrlutils.ContinueReconcile()
	}

	controllerutil.AddFinalizer(task, FinalizerName)
	if err := r.client.Update(ctx, task); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}

	return ctrlutils.ContinueReconcile()
}

// 1) mark state as pending
// 2) set initiated at
func (r *Reconciler) transitionToPendingState(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if task.Status.State != nil {
		return ctrlutils.ContinueReconcile()
	}
	if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStatePending); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
	
}

// TODO: Come up with implementation for this.
// 1) Set LastOperation {phase: "admit", state: "inProgress", lastTransitionTime: time.Now(), description: "admit process for task {taskName}"}
// 2) Call only if the LastOperation is not set.
func (r *Reconciler) admitTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if task.Status.State != nil && *task.Status.State != v1alpha1.TaskStatePending {
		return ctrlutils.ContinueReconcile()	
	} 

	if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseAdmit, v1alpha1.OperationStateInProgress); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
    result := taskHandler.Admit(ctx)
	// TODO: Check error?
	if !result.Completed {
		if result.Error != nil {
			return ctrlutils.ReconcileWithError(result.Error)
		}
		if result.RequeueAfter == 0 {
			return ctrlutils.ReconcileAfter(r.config.RequeueInterval, "Task admit in progress")
		}
		return ctrlutils.ReconcileAfter(result.RequeueAfter, "Task admit in progress")
	}
	if result.Error != nil  && result.Completed {
		if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseAdmit, v1alpha1.OperationStateFailed); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
		if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateRejected); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
		// TODO: Pass TTL
		return ctrlutils.ReconcileAfter(time.Duration(*task.Spec.TTLSecondsAfterFinished), "Task failed to admit")
	} 
	return ctrlutils.ContinueReconcile()
}

// state -> inProgress
func (r *Reconciler) transitionToInProgressState(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	// Do this only if the current state is pending.
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	// TODO: is the check for the state needed? reverse conditions for fail first
	if task.Status.State != nil && *task.Status.State == v1alpha1.TaskStatePending {
		task.Status.State = ptr.To(v1alpha1.TaskStateInProgress)
		task.Status.InitiatedAt = &metav1.Time{Time: time.Now()}
		if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateInProgress); err != nil {
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

// TODO: Implement this.
// 1) Set LastOperation {phase: "run", state: "inProgress", lastTransitionTime: time.Now(), description: "running task {taskName}"} only if previous phase was admit. Skip if already set.
// 2) Call the task handler to run the task.
func (r *Reconciler) runTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}

	if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateInProgress); err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	result := taskHandler.Run(ctx)
	if result.Completed {
		// mark state as succeeded or failed
		if result.Error != nil {
			if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateFailed); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
			if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateFailed); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
		} else {
			if err := r.updateLastOperation(ctx, taskObjKey, v1alpha1.OperationPhaseRunning, v1alpha1.OperationStateCompleted); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
			if err := r.updateState(ctx, taskObjKey, v1alpha1.TaskStateSucceeded); err != nil {
				return ctrlutils.ReconcileWithError(err)
			}
		}
		return ctrlutils.ReconcileAfter(time.Duration(*task.Spec.TTLSecondsAfterFinished), "Task completed, waiting for TTL to expire")
	} else if result.Error != nil {
		return ctrlutils.ReconcileWithError(result.Error)
	}
	// TODO: In handler, pass TTL
	return ctrlutils.ReconcileAfter(result.RequeueAfter, "Task in progress") 


}


// TODO: Move to helper
// Method to get the task object.
func (r *Reconciler) getTask(ctx context.Context, taskObjKey client.ObjectKey) (*v1alpha1.EtcdOperatorTask, error) {
	task := &v1alpha1.EtcdOperatorTask{}
	if err := r.client.Get(ctx, taskObjKey, task); err != nil {
		return nil, err
	}
	return task, nil
}

// function to update the last operation field in the status.
func (r *Reconciler) updateLastOperation(ctx context.Context, taskObjKey client.ObjectKey, phase v1alpha1.OperationPhase, state v1alpha1.OperationState) error {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return err
	}
	var isUpdateRequired bool
	var lastOperation *v1alpha1.EtcdOperatorLastOperation

	if task.Status.LastOperation != nil {
		if task.Status.LastOperation.Phase != phase || task.Status.LastOperation.State != state {
			isUpdateRequired = true

		} 
		if !isUpdateRequired {
			return nil
		}
		lastOperation = task.Status.LastOperation
		lastOperation.Phase = phase
		lastOperation.State = state
		if lastOperation.Phase != phase {
			lastOperation.LastTransitionTime = &metav1.Time{Time: time.Now()}
		}

	} else {
		isUpdateRequired = true
		lastOperation = &v1alpha1.EtcdOperatorLastOperation{
			Phase:              phase,
			State:              state,
			LastTransitionTime: &metav1.Time{Time: time.Now()},
			Description:        string(task.Status.LastOperation.Phase) + "is in state " + string(task.Status.LastOperation.State) + "for task " + taskObjKey.Name,
		}
	}

	return r.client.Status().Update(ctx, task)
}


func (r *Reconciler) updateState(ctx context.Context, taskObjKey client.ObjectKey, state v1alpha1.TaskState) error {
	task, err := r.getTask(ctx, taskObjKey)
	if err != nil {
		return err
	}
	if *task.Status.State == v1alpha1.TaskStateInProgress {
		if task.Status.InitiatedAt == nil {
			task.Status.InitiatedAt = &metav1.Time{Time: time.Now()}
		}
	}
	task.Status.State = &state
	return r.client.Status().Update(ctx, task)
}
// func (r *Reconciler) getLastError(task *v1alpha1.EtcdOperatorTask) druidv1 {
// 	if len(task.Status.LastErrors) == 0 {
// 		return nil
// 	}
// 	return task.Status.LastErrors[0].Error
// }