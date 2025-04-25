# EtcdOperatorTask Controller Design

## Custom Resource Golang API

`EtcdOperatorTask` is the new custom resource that will be introduced. This API will be in `v1alpha1` version and will be subject to change. We will be respecting [Kubernetes Deprecation Policy](https://kubernetes.io/docs/reference/using-api/deprecation-policy/).

```go
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=etcdoperatortasks,shortName=eot;eots,scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",JSONPath=".spec.type",type=string
// +kubebuilder:printcolumn:name="State",JSONPath=".status.state",type=string
// +kubebuilder:printcolumn:name="Age",JSONPath=".metadata.creationTimestamp",type="date"
type EtcdOperatorTask struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   EtcdOperatorTaskSpec   `json:"spec"`
    
    Status EtcdOperatorTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XPreserveUnknownFields
// +kubebuilder:validation:Immutable
type EtcdOperatorTaskSpec struct {
    // +kubebuilder:validation:Enum=OnDemandSnapshot;Compaction;Defragmentation;QuorumRecovery
    // +kubebuilder:validation:Required
    // +kubebuilder:default=OnDemandSnapshot
    // +kubebuilder:validation:Immutable
    Type v1alpha1.EtcdOperatorTaskType `json:"type"`

    // Config is task-specific key/value parameters.
    // +optional
    Config runtime.RawExtension `json:"config,omitempty"` 

    // TTLSecondsAfterFinished controls how long the status+pod stays around.
    // +optional
    // +kubebuilder:validation:Minimum=0
    TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

    // EtcdReference points at the Etcd CR for which this task runs.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Immutable
    EtcdReference types.NamespacedName `json:"etcdReference"`


}
```

#### Status

The authors propose the following fields for the Status (current state) of the `EtcdOperatorTask` custom resource to monitor the progress of the task.

```go
// EtcdOperatorTaskStatus is the status for a EtcdOperatorTask resource.
type EtcdOperatorTaskStatus struct {
  // ObservedGeneration is the most recent generation observed for the resource.
  ObservedGeneration *int64 `json:"observedGeneration,omitempty"`
  // State is the last known state of the task.
  State TaskState `json:"state"`
  // Time at which the task has moved from "pending" state to any other state.
  InitiatedAt metav1.Time `json:"initiatedAt"`
  // LastError represents the errors when processing the task.
  // +optional
  LastErrors []LastError `json:"lastErrors,omitempty"`
  // Captures the last operation status if task involves many stages.
  // +optional
  LastOperation *LastOperation `json:"lastOperation,omitempty"`
}

type LastOperation struct {
  // Status of the last operation, one of pending, progress, completed, failed.
  State OperationState `json:"state"`
  // LastTransitionTime is the time at which the operation state last transitioned from one state to another.
  LastTransitionTime metav1.Time `json:"lastTransitionTime"`
  // A human readable message indicating details about the last operation.
  Description string `json:"description"`
}

// LastError stores details of the most recent error encountered for the task.
type LastError struct {
  // Code is an error code that uniquely identifies an error.
  Code ErrorCode `json:"code"`
  // Description is a human-readable message indicating details of the error.
  Description string `json:"description"`
  // ObservedAt is the time at which the error was observed.
  ObservedAt metav1.Time `json:"observedAt"`
}

// TaskState represents the state of the task.
type TaskState string

const (
  TaskStateFailed TaskState = "Failed"
  TaskStatePending TaskState = "Pending"
  TaskStateRejected TaskState = "Rejected"
  TaskStateSucceeded TaskState = "Succeeded"
  TaskStateInProgress TaskState = "InProgress"
)

// OperationState represents the state of last operation.
type OperationState string

const (
  OperationStateInProgress OperationState = "InProgress"
  OperationStateCompleted OperationState = "Completed"
  OperationStateFailed OperationState = "Failed"
)
```

### Custom Resource YAML API

```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: EtcdOperatorTask
metadata:
    name: <name of operator task resource>
    namespace: <cluster namespace>
    generation: <specific generation of the desired state>
spec:
    type: <type/category of supported out-of-band task>
    ttlSecondsAfterFinished: <time-to-live to garbage collect the custom resource after it has been completed>
    config: <task specific configuration>
    ownerEtcdRefrence: <refer to corresponding etcd owner name and namespace for which task has been invoked>
status:
    observedGeneration: <specific observedGeneration of the resource>
    state: <last known current state of the out-of-band task>
    initiatedAt: <time at which task move to any other state from "pending" state>
    lastErrors:
    - code: <error-code>
      description: <description of the error>
      observedAt: <time the error was observed>
    lastOperation:
      name: <operation-name>
      state: <task state as seen at the completion of last operation>
      lastTransitionTime: <time of transition to this state>
      reason: <reason/message if any>
```

## OperatorTask Interface

Defines the contract for all task executors:

```go
type TaskResult struct {
    Description string
    Error error
    RequeueAfter  time.Duration // Duration to requeue the task
    Completed bool
}

// OperatorTask defines the interface for task execution.
type OperatorTask interface {
    EtcdReference types.NamespacedName
    Name string
    config runtime.RawExtension
    // Checks if the task is permitted to run. This is a one-time gate; once passed, it is not checked again for the same task execution.
    Admit(ctx context.Context) *TaskResult  
    Run(ctx context.Context) *TaskResult
    Cleanup(ctx context.Context) *TaskResult
}
```

---

## Task Executor Registration Pattern

To improve extensibility and maintainability, the EtcdOperatorTask reconciler uses an executor registration pattern. Instead of hardcoding executor creation logic in a switch statement, the reconciler maintains an internal registry mapping task types to executor factory functions.

```go
// TaskExecutorFactory builds an OperatorTask given client, logger, and the Task.
type TaskExecutorFactory func(client.Client, logr.Logger) OperatorTask

func (r *EtcdOperatorTaskReconciler) RegisterOperatorTask(
    t v1alpha1.EtcdOperatorTaskType,
    factory TaskExecutorFactory,
) {
    r.executorRegistry[t] = factory
}

func (r *EtcdOperatorTaskReconciler) getExecutor(
    task *v1alpha1.EtcdOperatorTask,
) (OperatorTask, error) {
    factory, ok := r.executorRegistry[task.Spec.Type]
    if !ok {
        return nil, fmt.Errorf("unsupported task type %q", task.Spec.Type)
    }
    return factory(r.Client, r.Log.WithValues("type", task.Spec.Type)), nil
}
```

```go
// NewReconciler constructs the controller and registers all built-in executors.
func NewReconciler(mgr ctrl.Manager) *EtcdOperatorTaskReconciler {
    r := &EtcdOperatorTaskReconciler{
        Client:           mgr.GetClient(),
        Log:              ctrl.Log.WithName("etcdoperatortask"),
        executorRegistry: make(map[v1alpha1.EtcdOperatorTaskType]TaskExecutorFactory),
        metrics:          metrics.NewTaskMetrics(),
    }
    // register executors
    r.RegisterOperatorTask(v1alpha1.TypeOnDemandSnapshot, NewOnDemandSnapshotExecutor)
    r.RegisterOperatorTask(v1alpha1.TypeCompactionJob,   NewCompactionJobExecutor)
    r.RegisterOperatorTask(v1alpha1.TypeDefragmentation, NewDefragExecutor)
    r.RegisterOperatorTask(v1alpha1.TypeQuorumRecovery,  NewQuorumRecoveryExecutor)
    return r
}
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
        return r.UpdateStatusRejected(task, err)
    }

    if task.IsCompleted() || task.IsMarkedForDeletion() {
        return r.triggerTaskDeletionFlow(ctx, logger, taskObjKey, operatorTask)
    }

    return r.reconcileTask(task, operatorTask)
}
```
> [!NOTE] Invalid Config: Set State: Rejected

```go
// reconcileTask manages preconditions, execution, and status updates.
func (r *Reconciler) reconcileTask(ctx tasks.TaskContext, taskObjKey client.ObjectKey, executor tasks.TaskExecutor) ctrlutils.ReconcileStepResult {
    ctx.Logger.Info("Reconciling task", "namespace", taskObjKey.Namespace, "name", taskObjKey.Name)
    reconcileStepFns := []reconcileFn{
        r.recordTaskReconciliationStartOperation,
        r.ensureFinalizer,
        r.moveTaskToPending,
        r.updateObservedGeneration,
        r.checkPreconditions,
        r.moveTaskToInProgress,
        r.executeTask,
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
    if task.IsCompleted() && !task.IsMarkedForDeletion() {
        if !task.TTLHasExpired() {
            return ctrlutils.ReconcileAfter(task.Spec.TTLSecondsAfterFinished, "Task completed, waiting for TTL to expire")
        }
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
```

## Metrics
```go
// MetricsExecutor wraps an OperatorTask with metrics collection
type MetricsExecutor struct {
    wrapped  OperatorTask
    taskType string
}

// WithMetrics creates a new MetricsExecutor
func WithMetrics(wrapped OperatorTask, taskType string) OperatorTask {
    return &MetricsExecutor{
        wrapped:  wrapped,
        taskType: taskType,
    }
}

// PermitExecution proxies the call and collects metrics
func (m *MetricsExecutor) PermitExecution(ctx context.Context, task *v1alpha1.EtcdOperatorTask) *TaskExecutionResult {
    startTime := time.Now()
    result := m.wrapped.PermitExecution(ctx, task)
    duration := time.Since(startTime).Seconds()

    // Record duration
    taskDuration.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace, string(*result.State)).Observe(duration)

    // Record errors if any
    if result.LastError != nil {
        errorTypes.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace, result.LastError.Code).Inc()
    }

    return result
}

// Run proxies the call and collects metrics
func (m *MetricsExecutor) Run(ctx context.Context, task *v1alpha1.EtcdOperatorTask) *TaskExecutionResult {
    startTime := time.Now()
    tasksInProgress.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace).Inc()
    defer tasksInProgress.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace).Dec()

    result := m.wrapped.Run(ctx, task)
    duration := time.Since(startTime).Seconds()

    // Record duration
    taskDuration.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace, string(*result.State)).Observe(duration)

    // Update success/failure/rejection counters
    switch *result.State {
    case TaskStateSucceeded:
        taskSuccessTotal.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace).Inc()
    case TaskStateFailed:
        taskFailureTotal.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace).Inc()
    case TaskStateRejected:
        taskRejectionTotal.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace).Inc()
    }

    // Record errors if any
    if result.LastError != nil {
        errorTypes.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace, result.LastError.Code).Inc()
    }

    return result
}

// Cleanup proxies the call and collects metrics
func (m *MetricsExecutor) Cleanup(ctx context.Context, task *v1alpha1.EtcdOperatorTask) *TaskExecutionResult {
    startTime := time.Now()
    result := m.wrapped.Cleanup(ctx, task)
    duration := time.Since(startTime).Seconds()

    // Record duration
    taskDuration.WithLabelValues(m.taskType, task.Spec.OwnerEtcdRefrence.Name, task.Namespace, string(*result.State)).Observe(duration)

    return result
}
```

```go
var (
    taskDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "etcd_operator_task_duration_seconds",
            Help:    "Duration of EtcdOperatorTask execution",
            Buckets: prometheus.DefBuckets,
        },
        []string{"task_type", "etcd_name", "namespace", "state"},
    )
    taskSuccessTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "etcd_operator_task_success_total",
            Help: "Total number of successful EtcdOperatorTasks",
        },
        []string{"task_type", "etcd_name", "namespace"},
    )
    taskFailureTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "etcd_operator_task_failure_total",
            Help: "Total number of failed EtcdOperatorTasks",
        },
        []string{"task_type", "etcd_name", "namespace"},
    )
    taskRejectionTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "etcd_operator_task_rejection_total",
            Help: "Total number of rejected EtcdOperatorTasks",
        },
        []string{"task_type", "etcd_name", "namespace"},
    )
    tasksInProgress = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "etcd_operator_tasks_in_progress",
            Help: "Number of EtcdOperatorTasks currently in progress",
        },
        []string{"task_type", "etcd_name", "namespace"},
    )
)
```

```go
func (r *EtcdOperatorTaskReconciler) createOperatorTask(task *v1alpha1.EtcdOperatorTask) (OperatorTask, error) {
    factory, ok := r.executorRegistry[task.Spec.Type]
    if !ok {
        return nil, fmt.Errorf("unsupported task type: %s", task.Spec.Type)
    }
    // Wrap the executor with metrics
    return WithMetrics(factory(r.Client, r.Log, task), task.Spec.Type), nil
}
```

## Example Task Executor
```go
type snapshotExecutor struct {
	k8sClient  client.Client
	httpClient *http.Client
	log        logr.Logger
}

func NewSnapshotExecutor(k8sClient client.Client, log logr.Logger, task) OperatorTask {
	return &SnapshotExecutor{
		k8sClient:  k8sClient,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		log:        log.WithName("snapshot-exec"),

	}
}

func (e *SnapshotExecutor) Admit(ctx context.Context, task *v1alpha1.EtcdOperatorTask) *TaskResult {
    // 1. Fetch Etcd CR
    var etcd druidv1alpha1.Etcd
    if err := e.k8sClient.Get(ctx, types.NamespacedName{
        Name:      task.Spec.OwnerEtcdRefrence.Name,
        Namespace: task.Namespace,
    }, &etcd); err != nil {
        e.log.Info("Failed to fetch Etcd CR", "err", err)
        return &TaskExecutionResult{
            State:       ptr(TaskStatePending),
            LastError:   &v1alpha1.EtcdOperatorTaskLastError{Description: err.Error()},
            RequeueAfter: 10 * time.Second,
        }
    }

    // 2. Check Etcd CR readiness (using .Ready or .Conditions)
    ready := false
    for _, cond := range etcd.Status.Conditions {
        if cond.Type == "Ready" && cond.Status == corev1.ConditionTrue {
            ready = true
            break
        }
    }
    if !ready {
        e.log.Info("Etcd CR not ready, will retry")
        return &TaskExecutionResult{
            State:       ptr(TaskStatePending),
            LastError:   &v1alpha1.EtcdOperatorTaskLastError{Description: "Etcd CR not ready"},
            RequeueAfter: 15 * time.Second,
        }
    }

    return &TaskExecutionResult{
        State: ptr(TaskStateInProgress),
    }
}

func (e *SnapshotExecutor) Run(ctx context.Context, task *v1alpha1.EtcdOperatorTask) *TaskExecutionResult {
	podIP, err := e.firstReadyEtcdPodIP(ctx, task.Spec.OwnerEtcdRefrence.Name, task.Namespace)
	if err != nil {
		return &TaskExecutionResult{
			State:       ptr(TaskStatePending),
			LastError:   &v1alpha1.EtcdOperatorTaskLastError{Description: err.Error()},
			RequeueAfter: 20 * time.Second,
		}
	}

	snapType := "full"
	timeout := 30 * time.Second
	if t, ok := task.Spec.Config["snapshotType"].(string); ok && t == "Delta" {
		snapType = "delta"
	}
	if v, ok := task.Spec.Config["timeoutSeconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}

	url := fmt.Sprintf("http://%s/snapshot/%s", podIP, snapType)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	e.log.Info("Triggering snapshot", "url", url)

	e.httpClient.Timeout = timeout
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return &TaskExecutionResult{
			State:       ptr(TaskStateInProgress),
			LastError:   &v1alpha1.EtcdOperatorTaskLastError{Description: fmt.Sprintf("HTTP call failed: %v", err)},
			RequeueAfter: 15 * time.Second,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return &TaskExecutionResult{
			State:       ptr(TaskStateFailed),
			LastError:   &v1alpha1.EtcdOperatorTaskLastError{Description: fmt.Sprintf("sidecar returned %d", resp.StatusCode)},
		}
	}

	op := &v1alpha1.EtcdOperatorTaskLastOperation{
		Description: fmt.Sprintf("%s snapshot triggered", snapType),
		LastUpdateTime: metav1.Now(),
	}

	return &TaskExecutionResult{
		State:        ptr(TaskStateSucceeded),
		LastOperation: op,
	}
}

func (e *SnapshotExecutor) Cleanup(ctx context.Context, task *v1alpha1.EtcdOperatorTask) *TaskExecutionResult {
	// No-op for snapshot tasks
	return &TaskExecutionResult{
		State: ptr(TaskStateSucceeded),
	}
}

// ptr is a helper to take a pointer to a value (for State)
func ptr[T any](v T) *T { return &v }
```


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