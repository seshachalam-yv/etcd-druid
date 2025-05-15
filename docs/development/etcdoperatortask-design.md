# EtcdOperatorTask Controller Implementation Design

This document builds upon the initial proposal outlined in [05-etcd-operator-tasks](https://github.com/gardener/etcd-druid/blob/main/docs/proposals/05-etcd-operator-tasks.md) and describes the design and implementation of the **EtcdOperatorTask** controller. The controller enables out-of-band operational tasks for etcd clusters managed by etcd-druid. The design emphasizes extensibility, clear separation of concerns, and robust status tracking.

## Overview

The `EtcdOperatorTask` custom resource (CR) provides a generic mechanism to trigger and manage operational tasks (such as on-demand snapshots, maintenance, etc.) for etcd clusters. Each task is represented as a CR instance, with its lifecycle managed by a single controller. This approach decouples operational logic from the core reconciliation loop and ensures seamless extensibility for introducing new task types.

## Custom Resource API Design

The `EtcdOperatorTask` CRD is defined under the `v1alpha1` API version. It is subject to change and will follow the [Kubernetes Deprecation Policy](https://kubernetes.io/docs/reference/using-api/deprecation-policy/).

**Key design decisions:**

The authors slightly modified CRD for implementation purposes introduced to handle out-of-band tasks as initially proposed by [05-etcd-operator-tasks](https://github.com/gardener/etcd-druid/blob/main/docs/proposals/05-etcd-operator-tasks.md)

- The `spec` is immutable (`kubebuilder:validation:Immutable`) to ensure task intent cannot be changed after creation.
- The `config` field uses `runtime.RawExtension` to flexibly support task-specific parameters, validated via admission webhook.
- `ownerEtcdReference` under the spec field is renamed to `etcdReference` for clarity.
- The `LastOperation` struct now uses a `Description` field instead of `Name` and `Reason` for better expressiveness.

### Go API Definition

```go
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=etcdoperatortasks,shortName=eot;eots,scope=Namespaced
// +kubebuilder:subresource:status
type EtcdOperatorTask struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   EtcdOperatorTaskSpec   `json:"spec"`
    Status EtcdOperatorTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XPreserveUnknownFields
// +kubebuilder:validation:Immutable
type EtcdOperatorTaskSpec struct {
    // +required
    Type v1alpha1.EtcdOperatorTaskType `json:"type"`

    // Config is task-specific key/value parameters.
    // +required
    Config runtime.RawExtension `json:"config,omitempty"` 

    // TTLSecondsAfterFinished controls how long the status+pod stays around.
    // +optional
    // +kubebuilder:validation:XValidation:message="TTLSecondsAfterFinished must be greater than 0",rule="!has(self) || self > 0"
    TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

    // +optional
    EtcdReference types.NamespacedName `json:"etcdReference,omitempty"`

}
```

> [!Note]
> Use `runtime.RawExtension` for the `config` field to enable each task type to define its own schema dynamically. Ensure this field is validated through an admission webhook to enforce type safety and provide immediate feedback to users.

### Status Subresource

The `status` field tracks the progress and outcome of each task, including state transitions, errors, and operation history.



```go
// EtcdOperatorTaskStatus is the status for a EtcdOperatorTask resource.
type EtcdOperatorTaskStatus struct {
  // State is the last known state of the task.
  State TaskState `json:"state"`
  // Time at which the task has moved from "pending" state to any other state.
  // +optional
  InitiatedAt *metav1.Time `json:"initiatedAt"`
  // LastError represents the errors when processing the task. Will have a limit of 10 entries at a time.
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
  LastTransitionTime *metav1.Time `json:"lastTransitionTime"`
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
  ObservedAt *metav1.Time `json:"observedAt"`
}

// TaskState represents the state of the task ie the status of the CR.
type TaskState string

const (
  TaskStateFailed TaskState = "Failed"
  TaskStatePending TaskState = "Pending"
  TaskStateRejected TaskState = "Rejected"
  TaskStateSucceeded TaskState = "Succeeded"
  TaskStateInProgress TaskState = "InProgress"
)

// OperationState represents the state of last operation run via the task.
type OperationState string

const (
  OperationStateInProgress OperationState = "InProgress"
  OperationStateCompleted OperationState = "Completed"
  OperationStateFailed OperationState = "Failed"
)

```
</details>

---

### Example: EtcdOperatorTask YAML


```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: EtcdOperatorTask
metadata:
    name: <name of operator task resource>
    namespace: <namespace>
    generation: <specific generation of the desired state>
spec:
    type: <type/category of supported out-of-band task>
    ttlSecondsAfterFinished: <time-to-live to garbage collect the custom resource after it has been completed>
    config: <task specific configuration>
    etcdReference: <refer to corresponding etcd owner name and namespace for which task has been invoked>
status:
    observedGeneration: <specific observedGeneration of the resource>
    state: <last known current state of the out-of-band task>
    initiatedAt: <time at which task move to any other state from "pending" state>
    lastErrors:
    - code: <error-code>
      description: <description of the error>
      observedAt: <time the error was observed>
    lastOperation:
      description: <meaningful description>
      state: <task state as seen at the completion of last operation>
      lastTransitionTime: <time of transition to this state>

```
</details>

## TaskHandler Interface

The `TaskHandler` interface defines the contract for implementing out-of-band task logic. Each task type must provide its own handler, encapsulating admission checks, execution, and cleanup.


```go
type TaskResult struct {
    Description string
    Error error
    RequeueAfter  time.Duration // Duration to requeue the task
    Completed bool
}

// OperatorTask defines the interface for task execution.
type TaskHandler interface {
    EtcdReference() types.NamespacedName // based on if etcdReference is set in the spec.
    Name() string
    Type() druidv1alpha1.EtcdOperatorTaskType
    Logger() logr.Logger
    // Checks if the task is permitted to run. This is a one-time gate; once passed, it is not checked again for the same task execution.
    Admit(ctx context.Context) *TaskResult  
    Run(ctx context.Context) *TaskResult // The Run method will have to check if the necessary pre conditions hold true upon each call. 
    Cleanup(ctx context.Context) *TaskResult // Will be triggered once the task is in a completed state.
}

```

**Interface responsibilities:**

- `Admit`: One-time precondition check before task execution.
- `Run`: Main execution logic, called on each reconcile until completion.
- `Cleanup`: Post-completion or TTL-based cleanup.
- `Name`, `Type`, `EtcdReference`: Used for metrics and logging.


## TaskHandler Instantiation

The controller uses a factory function (`createTaskHandlerInstance`) to instantiate the correct handler for each task type. This enables easy extension and decouples task logic from the reconciler.

```go
// Add a new TaskHandler as shown below:
func (r *Reconciler) createTaskHandlerInstance(task *v1alpha1.EtcdOperatorTask) (tasks.TaskHandler, error) {
    switch task.Spec.Type {
    case v1alpha1.EtcdOperatorTaskTypeOnDemandSnapshot:
        return ondemandsnapshot.New(r.client, r.logger, task) // New() initializes the TaskHandler.
    // Add more cases for other task types
    default:
        return nil, fmt.Errorf("unsupported task type: %s", task.Spec.Type)
    }
}

```

### Adding New Task Types

To add a new task type:
1. Implement the `TaskHandler` interface for your task.
2. Register the handler in `createTaskHandlerInstance` with a new case for your type.


## Validating Admission Webhook

To ensure only valid `EtcdOperatorTask` resources are accepted, a validating admission webhook is used. This webhook performs the following checks:

- **Required Fields:** Ensures all mandatory fields in the spec are present and valid (e.g., `type`, `config`, ...).
- **Config Validation:** Attempts to decode and validate the `config` field (`runtime.RawExtension`) according to the schema for the specified task type. If decoding or validation fails, the CR is rejected with a clear error message.
- **TTL Validation:** If `ttlSecondsAfterFinished` is set, ensures it is greater than zero.


## RawExtension Parsing and Task Config Validation

Each task implementor **must** provide:

1. **A config struct** that defines the schema for the task’s configuration.
2. **A decode function** that parses the `runtime.RawExtension` into the config struct and performs validation.

This ensures that each task type can enforce its own config requirements and validation logic, both in the webhook and at runtime.

**Example for OnDemandSnapshot:**
```go
type OnDemandSnapshotConfig struct {
    SnapshotType *string `json:"snapshotType,omitempty"`
    Timeout      *int    `json:"timeoutSeconds,omitempty"`
}

func decodeOnDemandSnapshotConfig(config runtime.RawExtension) (*OnDemandSnapshotConfig, error) {
    var snapshotConfig OnDemandSnapshotConfig
    if err := json.Unmarshal(config.Raw, &snapshotConfig); err != nil {
        return nil, fmt.Errorf("failed to decode config: %w", err)
    }
    if snapshotConfig.Timeout == nil {
        snapshotConfig.Timeout = ptr.Int(DEFAULT_TIMEOUT)
    }
    // Add further validation as needed
    return &snapshotConfig, nil
}
```

This approach ensures that invalid or incomplete configs are rejected early, and each task type can evolve its config schema independently.


## Reconciliation Flow

The controller's reconciliation loop manages the lifecycle of each `EtcdOperatorTask` resource, including validation, execution, status updates, and cleanup.

```go
func (r *TaskReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    // The below block checks for the OperatorTask.
    task := &v1alpha1.EtcdOperatorTask{}
    if err := r.client.Get(ctx, req.NamespacedName, task); err != nil {
        if client.IgnoreNotFound(err) != nil {
            return reconcile.Result{}, err
        }
        return reconcile.Result{}, nil
    }

    logger := r.logger.WithValues("runId", string(controller.ReconcileIDFromContext(ctx)))
    // create a instance of TaskHandler
    taskHandlerInstance := r.createTaskHandlerInstance(task)
    
    // Triggers the deletion flow in case the task is in a completed state or if it has been marked for deletion.
    if task.IsCompleted() || task.IsMarkedForDeletion() {
        return r.triggerDeletionFlow(ctx, taskHandlerInstance, task).ReconcileResult()
    }
    // triggers the task execution flow.
    return r.reconcileTask(ctx, client.ObjectKeyFromObject(task), taskHandlerInstance).ReconcileResult()
}

```

```go
// reconcileTask manages finalizer, execution, and status updates.
func (r *Reconciler) reconcileTask(ctx context.Context, taskObjKey client.ObjectKey, taskHandler tasks.TaskHandler) ctrlutils.ReconcileStepResult {
    reconcileStepFns := []reconcileFn{
        r.recordTaskStartOperation,
        r.ensureTaskFinalizer,
        r.updateStatusObservedGeneration,
        r.transitionToPendingState, // updates the status field. Skipped if already done.
        r.admitTaskPreconditions, // calls the implemented interface method. Run only at the first time.
        r.transitionToInProgressState, // updates the status. Skipped if already marked.
        r.runTask, // calls the implemented interface method.
    }

    for _, step := range reconcileStepFns {
        ctx.Logger.Info("Executing step", "step", step)
        result := step(ctx, taskObjKey, taskHandler)
        if ctrlutils.ShortCircuitReconcileFlow(result) {
            return result
        }
    }

    return ctrlutils.ReconcileAfter(task.Spec.TTLSecondsAfterFinished, "Task completed, waiting for TTL to expire")
}
```


### Deletion Flow


```go
func (r *Reconciler) triggerTaskDeletionFlow(
    ctx context.Context,
    logger logr.Logger,
    taskObjKey client.ObjectKey,
    taskHandler tasks.TaskHandler,
) ctrlutils.ReconcileStepResult {
    if task.IsCompleted() && !task.IsMarkedForDeletion() {
        if !task.TTLHasExpired() {
            return ctrlutils.ReconcileAfter(task.Spec.TTLSecondsAfterFinished, "Task completed, waiting for TTL to expire")
        }
    }

    deletionStepFns := []reconcileFn{
        r.recordTaskDeletionStartOperation, // update the status. Skip if already done.
        r.cleanupTaskResources, // Cleanup of any kind of resources used to run the task. Runs the interface method.
        r.recordTaskDeletionSuccessOperation,
        r.removeTaskFinalizer,
        r.removeTask, // Removes the CR.
    }
    for _, fn := range deletionStepFns {
        result := fn(ctx, taskObjKey, taskHandler)
        if ctrlutils.ShortCircuitReconcileFlow(result) {
            return r.recordTaskIncompleteDeletionOperation(ctx, logger, taskObjKey, result)
        }
    }
    return ctrlutils.DoNotRequeue()
}

```

## Example: TaskHandler Implementation


```go
// OnDemandSnapshot implements the TaskHandler interface for on-demand snapshots.
type OnDemandSnapshot struct {
    client        client.Client
    httpClient    *http.Client
    logger        logr.Logger
    name          string
    etcdReference types.NamespacedName
    config        *Config
}
// the config provided in the spec will be parsed as follows:
const ERR_PRECONDITION_CHECK_ON_DEMAND_SNAPSHOT druidv1alpha1.ErrorCode = "ERR_PRECONDITION_CHECK_ON_DEMAND_SNAPSHOT"


func New(k8sclient client.Client, logger logr.Logger, task *v1alpha1.EtcdOperatorTask) (operatortask.OperatorTask, error) {
     
    config,err := decodeOnDemandSnapshotConfig(task.Spec.Config)
    if err != nil {
        return nil, druiderr.WrapError(err, operatortask.ERR_INVALID_CONFIG, operationNew, "failed to decode config for OnDemandSnapshot")
    }
    
    return &OnDemandSnapshot{
        client:        k8sclient,
        logger:        logger,
        name:          task.Name,
        httpClient:    &http.Client{Timeout: config.Timeout},
        etcdReference: types.NamespacedName(*task.Spec.EtcdRef),
        config:        config,
    }, nil
}

func (o *OnDemandSnapshot) Run(ctx context.Context) *operatortask.TaskResult {
    // Implement the snapshot logic
    etcd := &v1alpha1.Etcd{}
    if err := o.client.Get(ctx, o.etcdReference, etcd); err != nil {
        return &operatortask.TaskResult{Description: "Failed to get Etcd resource", Error: err}
    }

    snapType := "full"
    if o.config.SnapshotType != nil && *o.config.SnapshotType != "" {
        snapType = *o.config.SnapshotType
    }

    o.logger.Info("Snapshot config", "snapshotType", snapType, "timeout", o.config.Timeout)

    url := fmt.Sprintf("http://%s.%s:%d/snapshot/full?final=true", v1alpha1.GetClientServiceName(etcd.ObjectMeta), etcd.Namespace, ptr.Deref(etcd.Spec.Backup.Port, common.DefaultPortEtcdBackupRestore))

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
    if err != nil {
        return &operatortask.TaskResult{
            Description: fmt.Sprintf("Failed to create HTTP request for %s", url),
            Error:       err,
        }
    }
    o.logger.Info("Triggering snapshot", "url", url)

    resp, err := o.httpClient.Do(req)
    if err != nil {
        return &operatortask.TaskResult{Description: "Snapshot HTTP request failed", Error: err}
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return &operatortask.TaskResult{Description: fmt.Sprintf("Snapshot failed, status: %s", resp.Status), Completed: false}
    }
    return &operatortask.TaskResult{Description: "Triggered snapshot on pod", Completed: true}
}

func (o *OnDemandSnapshot) Admit(ctx context.Context) *operatortask.TaskResult {
    // Implement the admission logic (e.g., check if Etcd is ready)
    etcd := &v1alpha1.Etcd{}
    if err := o.client.Get(ctx, o.etcdReference, etcd); err != nil {
        return &operatortask.TaskResult{Description: "Failed to get Etcd resource", Error: err}
    }
    if etcd.Status.ReadyReplicas < 1 {
        return &operatortask.TaskResult{
            Description: "No ready replicas for Etcd",
            Error: druiderr.WrapError(nil,
                ERR_PRECONDITION_CHECK_ON_DEMAND_SNAPSHOT,
                operationAdmit,
                fmt.Sprintf("No ready replicas for Etcd: %v", o.etcdReference)),
            Completed: true,
        }
    }
    return &operatortask.TaskResult{Description: "Admission successful", Completed: true}
}

func (o *OnDemandSnapshot) Cleanup(ctx context.Context) *operatortask.TaskResult {
    // No-op for snapshot tasks
    return &operatortask.TaskResult{Description: "Cleanup done", Completed: true}
}


```

#### Example YAML for an On-Demand Snapshot Task

```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: EtcdOperatorTask
metadata:
  name: on-demand-snapshot-task
  namespace: default
spec:
  type: OnDemandSnapshot
  etcdRef:
    name: etcd-test
    namespace: default
  config:
    snapshotType: "full"
    timeoutSeconds: 60
  ttlSecondsAfterFinished: 600
```

### Registering a New OperatorTask

1. Implement the `TaskHandler` and its constructor.
2. Add the new handler to `createTaskHandlerInstance`:
   ```go
   func (r *Reconciler) createTaskHandlerInstance(task *v1alpha1.EtcdOperatorTask) (tasks.TaskHandler) {
       switch task.Spec.Type {
       case v1alpha1.<NewTaskType>:
           return <NewTaskType>.New(r.client, r.logger, task)
       // Add more cases for other task types
       default:
       }
   }
   ```

**Benefits:**
- **Extensible:** New OperatorTasks can be added without modifying the core controller logic.
- **Decoupled:** OperatorTask implementations are independent from the reconciler logic.

---

## Next Steps

- Design and implement HTTP endpoints in the etcd-backup-restore server to expose the etcd maintenance API, supporting both sync and async operations.
- Ensure the snapshot API follows the same extensible pattern.

This approach allows `EtcdOperatorTask` to trigger etcd maintenance operations and monitor their completion by requeuing tasks as needed.

---