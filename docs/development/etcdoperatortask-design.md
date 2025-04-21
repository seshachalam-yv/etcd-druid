# EtcdOperatorTask Controller Design

## 1. Task Categories

EtcdOperator tasks are categorized into four main types based on their interaction with etcd and execution requirements:

| **Category**                            | **Description**                                                                                | **Examples**                                                                         | **Execution Context**             |
| --------------------------------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ | --------------------------------- |
| **1. etcd-backup-restore Subcommands**  | Tasks that leverage `etcd-backup-restore` CLI/subcommands, sometimes running an embedded etcd. | Copying/compacting snapshots                                                         | Standalone Kubernetes Jobs.       |
| **2. etcd-backup-restore Sidecar HTTP** | Tasks that require the sidecar container to handle snapshot creation via HTTP endpoints.       | On-demand full/delta snapshots, Etcd Maintenance ops(Compaction and Defragmentation) | HTTP call to sidecar in etcd Pod. |
| **3. etcd Client Operations**           | Maintenance tasks issued via `etcdctl` or compatible client libraries.                         | Leadership change, removing member etc                                               | Etcd Client call.                 |
| **4. Kubernetes API Tasks**             | Tasks that manipulate Kubernetes resources for cluster-wide or disruptive actions.             | Quorum loss recovery, migration, PVC management                                      | Operator using K8s API.           |

---

## 2. Controller Placement: Alternatives

We propose creating a new controller (and reconciler) for handling out-of-band etcd task operations. The controller follows a multi-step reconciliation flow similar to the existing etcd controller:
There are two potential options for the **operator task controller**:
- Running the operator task controller inside the leading etcd-backup-restore container. It would watch for events related to specific (or all) task types.
- Running the operator task controller inside etcd-druid, watching for events related to specific (or all) task types.

| **Task Type**                                      | **etcd-backup-restore (Leader) Operator Controller**                                                                                           | **etcd-druid Operator Controller**                                             |                                             |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ | ------------------------------------------- |
| **Type 1: Compaction**                             | N <br> (Not ideal because compaction starts an embedded etcd)                                                                                  | Y – Triggered as a Kubernetes Job using `etcdbrctl compact`                    |                                             |
| **Type 1: Copy**                                   | Y – Invokes internal APIs to perform copy                                                                                                      | Y – Executes as a Kubernetes Job using `etcdbrctl copy`                        |                                             |
| **Type 2: Full/Delta Snapshots , Maintenance Ops** | Y – Invokes internal APIs                                                                                                                      | Y – Uses an HTTPS client to communicate with the etcd-backup-restore container |                                             |
| **Type 3/4: Etcd Migrations**                      | N – Not applicable because etcd-backup-restore itself is not running and we prefer not to create k8s client from backup-restore for migrations | Y – Managed via etcd and Kubernetes client operations                          |                                             |
| **Type 4: Quorum Recovery**                        | N – No leader exists for quorum recovery                                                                                                       | Y – Handled through direct interactions with the Kubernetes API                |                                             |

---

## 3. Recommended Architecture

We recommend implementing the **EtcdOperatorTask** controller as part of `etcd-druid`:
- All tasks are feasible from etcd-druid, avoiding the need for multiple controllers.
- If a task requires the `etcd-backup-restore` container, etcd-druid can coordinate via HTTP endpoints, ensuring no conflicts with scheduled operations.

---

## 4. Controller Design


### TaskExecutor Interface

Defines the contract for all task executors:

```go
type TaskExecutionResult struct {
    LastOperation *v1alpha1.EtcdOperatorLastOperation
    LastError     *v1alpha1.EtcdOperatorLastError
    RequeueAfter  time.Duration // 0 if no requeue needed
    Completed     bool
}

// TaskExecutor defines the interface for task execution.
type TaskExecutor interface {
    CheckPreconditions(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) (*TaskExecutionResult, error)
    Execute(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) (*TaskExecutionResult, error)
    Cleanup(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) (*TaskExecutionResult, error)
}
```

---

## 6. Reconciliation Flow

The core reconciliation logic:

```go
func (r *TaskReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    task := getTask(req)
    if task == nil {
        return doNotRequeue()
    }

    executor, err := r.createTaskExecutor(task)
    if err != nil {
        updateStatusWithError(task, "UnknownTaskType")
        return r.collectGarbage(task)
    }

    if isDeletionRequested(task) {
        return r.triggerTaskDeletionFlow(ctx, logger, taskObjKey, executor)
    }
    if isTaskCompleted(task) {
        return r.collectGarbage(task)
    }

    return r.reconcileTask(task, executor)
}
```

### Task Executor Instantiation

```go
// createTaskExecutor creates a TaskExecutor instance for the given task,
// parsing task.Spec.Config or other settings at construction.
// Returns an error if the task type is unsupported.
func createTaskExecutor(
    task *v1alpha1.EtcdOperatorTask,
    k8sClient client.Client,
    logger logr.Logger,
) (TaskExecutor, error) {
    switch task.Spec.Type {
    case v1alpha1.EtcdOperatorTaskTypeOnDemandSnapshot:
        return NewOnDemandSnapshot(k8sClient, logger, task), nil
    // Add more cases for new task types
    default:
        return nil, fmt.Errorf("unsupported task type: %s", task.Spec.Type)
    }
}
```

```go
// reconcileTask manages preconditions, execution, and status updates.
func (r *Reconciler) reconcileTask(ctx tasks.TaskContext, taskObjKey client.ObjectKey, executor tasks.TaskExecutor) ctrlutils.ReconcileStepResult {
    ctx.Logger.Info("Reconciling task", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
    reconcileStepFns := []reconcileFn{
        r.recordTaskReconciliationStartOperation,
        r.ensureFinalizer,
        r.moveTaskToPending,
        r.checkAnySameTypeTaskInProgress,
        r.checkPreconditions,
        r.moveTaskToInProgress,
        r.executeTask,
        r.recordTaskReconciliationSuccessOperation,
        r.updateObservedGeneration,
        r.removeTaskReconciliationOperationAnnotation,
    }

    for _, step := range reconcileStepFns {
        ctx.Logger.Info("Executing step", "step", step)
        result := step(ctx, taskObjKey, executor)
        if ctrlutils.ShortCircuitReconcileFlow(result) {
            return result
        }
    }

    ctx.Logger.Info("Task execution completed", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
    return ctrlutils.ReconcileAfter(task.Spec.TTLSecondsAfterFinished, "Task completed, waiting for TTL to expire")
}
```

### Deletion Flow

```go
func (r *Reconciler) triggerTaskDeletionFlow(
    ctx tasks.TaskContext,
    logger logr.Logger,
    taskObjKey client.ObjectKey,
    executor tasks.TaskExecutor,
) ctrlutils.ReconcileStepResult {
    deletionStepFns := []reconcileFn{
        r.recordTaskDeletionStartOperation,
        r.cleanupTaskResources,
        r.recordTaskDeletionSuccessOperation,
        r.removeTaskFinalizer,
    }
    for _, fn := range deletionStepFns {
        result := fn(ctx, taskObjKey, executor)
        if ctrlutils.ShortCircuitReconcileFlow(result) {
            return r.recordTaskIncompleteDeletionOperation(ctx, logger, taskObjKey, result)
        }
    }
    return ctrlutils.DoNotRequeue()
}
```

---
## 5. Extending with New Task Types

To add a new task type:
1. Implement the `TaskExecutor` interface for the new task.
2. Instantiate the executor in the reconciler with any required dependencies during task processing.

This design ensures that each task execution is isolated, customizable, and testable, and avoids the pitfalls of global registries or shared state.

---
## Open Questions

1. **When should the task status be updated to 'rejected'?**
2. **Do we allow spec updates to the Operator CR?**
    - Allowing spec updates after tasks are scheduled but before execution introduces complexity in task handling. (Needs better phrasing and decision.)
    ```go
    type maintenanceOps struct {
      // +optional
      EtcdCompaction bool `json:"etcdCompaction,omitempty"`
      // +optional
      EtcdDefragmentation bool `json:"etcdDefragmentation,omitempty"`
    }
    ```
3. **For on-demand delta snapshots, should we check that a full snapshot exists first?**
4. **If an on-demand operation fails due to an internal issue, should we automatically re-trigger it?**
5. **Should we refactor the current snapshot endpoint to be asynchronous?**
6. **How should we coordinate with scheduled operations?**
    - Currently handled via endpoints in backup-restore.

---