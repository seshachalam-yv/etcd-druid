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


### OperatorTask Interface

Defines the contract for all task executors:

```go
type TaskExecutionResult struct {
    LastOperation *v1alpha1.EtcdOperatorLastOperation
    LastError     *v1alpha1.EtcdOperatorLastError
    RequeueAfter  time.Duration // 0 if no requeue needed
    Completed     bool
}

// OperatorTask defines the interface for task execution.
type OperatorTask interface {
    CheckPreconditions(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) *TaskExecutionResult
    Execute(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) (complete bool, *TaskExecutionResult, error)
    Cleanup(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) *TaskExecutionResult
}
```

---

## Task Executor Registration Pattern

To improve extensibility and maintainability, the EtcdOperatorTask reconciler uses an executor registration pattern. Instead of hardcoding executor creation logic in a switch statement, the reconciler maintains an internal registry mapping task types to executor factory functions.

### How it Works
- The reconciler struct contains a field:

  ```go
  executorRegistry map[v1alpha1.EtcdOperatorTaskType]TaskExecutorFactory
  ```

```go 
func (r *EtcdOperatorTaskReconciler) RegisterOperatorTask(
    taskType v1alpha1.EtcdOperatorTaskType,
    factory TaskExecutorFactory,
) {
    if r.executorRegistry == nil {
        r.executorRegistry = make(map[v1alpha1.EtcdOperatorTaskType]TaskExecutorFactory)
    }
    r.executorRegistry[taskType] = factory
}

func (r *EtcdOperatorTaskReconciler) createOperatorTask(
    task *v1alpha1.EtcdOperatorTask,
) (OperatorTask, error) {
    factory, ok := r.executorRegistry[task.Spec.Type]
    if !ok {
        return nil, fmt.Errorf("unsupported task type: %s", task.Spec.Type)
    }
    return factory(r.Client, r.Log, task), nil
}
```
- Executors are registered with the reconciler during setup:

  ```go
  reconciler.RegisterOperatorTask(v1alpha1.EtcdOperatorTaskTypeOnDemandSnapshot, NewOnDemandSnapshot)
  ```

- When a task needs to be executed, the reconciler looks up the appropriate factory and instantiates the executor:

  ```go
  executor, err := r.createOperatorTask(task)
  ```

## 6. Reconciliation Flow

The core reconciliation logic:

```go
func (r *TaskReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    task := getTask(req)
    if task == nil {
        return doNotRequeue()
    }

    operatorTask, err := r.createOperatorTask(task)
    if err != nil {
        updateStatusWithError(task, "UnknownTaskType")
        return r.collectGarbage(task)
    }

    if isDeletionRequested(task) {
        return r.triggerTaskDeletionFlow(ctx, logger, taskObjKey, operatorTask)
    }
    if isTaskCompleted(task) {
        return r.collectGarbage(task)
    }

    return r.reconcileTask(task, operatorTask)
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
    operatorTask tasks.OperatorTask,
) ctrlutils.ReconcileStepResult {
    deletionStepFns := []reconcileFn{
        r.recordTaskDeletionStartOperation,
        r.cleanupTaskResources,
        r.recordTaskDeletionSuccessOperation,
        r.removeTaskFinalizer,
    }
    for _, fn := range deletionStepFns {
        result := fn(ctx, taskObjKey, operatorTask)
        if ctrlutils.ShortCircuitReconcileFlow(result) {
            return r.recordTaskIncompleteDeletionOperation(ctx, logger, taskObjKey, result)
        }
    }
    return ctrlutils.DoNotRequeue()
}
```

```go
type SnapshotExecutor struct {
	k8sClient   client.Client
	httpClient  *http.Client
	log         logr.Logger
}

func NewSnapshotExecutor(k8sClient client.Client, log logr.Logger) TaskExecutor {
	return &SnapshotExecutor{
		k8sClient:  k8sClient,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		log:        log.WithName("snapshot-exec"),
	}
}

func (e *SnapshotExecutor) CheckPreconditions(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) (*tasks.TaskExecutionResult, error) {
	// 1. Make sure etcd StatefulSet has a Ready pod
	podIP, err := e.firstReadyEtcdPodIP(ctx, task.Spec.EtcdRef.Name, task.Namespace)
	if err != nil {
		e.log.Info("no ready pod yet → requeue", "err", err)
		return &tasks.TaskExecutionResult{Phase: tasks.TaskPhasePending, RequeueAfter: 20 * time.Second}, nil
	}
	// 2. Quick TCP probe to sidecar port
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(podIP, "8080"), 2*time.Second)
	if err != nil {
		return &tasks.TaskExecutionResult{Phase: tasks.TaskPhasePending, RequeueAfter: 15 * time.Second}, nil
	}
	_ = conn.Close()
	return &tasks.TaskExecutionResult{Phase: tasks.TaskPhaseRunning}, nil
}

func (e *SnapshotExecutor) Execute(ctx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) (*tasks.TaskExecutionResult, error) {
	podIP, _ := e.firstReadyEtcdPodIP(ctx, task.Spec.EtcdRef.Name, task.Namespace)

	var (
		snapType      string = "full"
		timeout       = 30 * time.Second
	)
	if t, ok := task.Spec.Config["snapshotType"].(string); ok && t == "Delta" {
		snapType = "delta"
	}
	if v, ok := task.Spec.Config["timeoutSeconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}

	url := fmt.Sprintf("http://%s/snapshot/%s", podIP, snapType)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	e.log.Info("trigger snapshot", "url", url)

	e.httpClient.Timeout = timeout
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return &tasks.TaskExecutionResult{Phase: tasks.TaskPhaseFailed}, errors.Wrap(err, "HTTP call failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return &tasks.TaskExecutionResult{Phase: tasks.TaskPhaseFailed},
			fmt.Errorf("sidecar returned %d", resp.StatusCode)
	}

	op := &druidv1a1.EtcdOperatorLastOperation{
		Description: fmt.Sprintf("%s snapshot triggered", snapType),
		LastUpdateTime: metav1.Now(),
	}

	return &tasks.TaskExecutionResult{Phase: tasks.TaskPhaseSucceeded, LastOp: op}, nil
}

func (e *SnapshotExecutor) Cleanup(ctx  tasks.TaskContext, _ *v1alpha1.EtcdOperatorTask) (*tasks.TaskExecutionResult, error) {
    // no-op
	return &tasks.TaskExecutionResult{Phase: tasks.TaskPhaseSucceeded}, nil
}

// ----------------- helpers -----------------

func (e *SnapshotExecutor) firstReadyEtcdPodIP(ctx  tasks.TaskContext, etcdName, ns string) (string, error) {
	var podList corev1.PodList
	if err := e.k8sClient.List(ctx, &podList,
		client.InNamespace(ns),
		client.MatchingLabels{"app": "etcd", "instance": etcdName}); err != nil {
		return "", err
	}
	for _, p := range podList.Items {
		if cond := getPodReadyCondition(p.Status); cond != nil && cond.Status == corev1.ConditionTrue {
			return p.Status.PodIP, nil
		}
	}
	return "", fmt.Errorf("no Ready etcd pods found")
}


### Registering a New Task Executor
1. Implement the executor and its factory function (constructor).
2. During reconciler initialization, register the new executor:
   ```go
   reconciler.RegisterTaskExecutor(v1alpha1.<YourTaskType>, New<YourExecutor>)
   ```

### Benefits
- **Extensible:** New executors can be added without modifying the core creation logic.
- **Decoupled:** Executor implementations are independent from the reconciler logic.
- **Testable:** The registry can be injected or mocked for testing.

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