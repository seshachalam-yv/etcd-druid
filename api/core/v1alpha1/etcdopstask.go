// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OnDemandSnapshotType represents the type of on-demand snapshot i.e, full or delta.
type OnDemandSnapshotType string

const (
	// OnDemandSnapshotTypeFull represents a full snapshot.
	OnDemandSnapshotTypeFull OnDemandSnapshotType = "full"
	// OnDemandSnapshotTypeDelta represents a delta snapshot.
	OnDemandSnapshotTypeDelta OnDemandSnapshotType = "delta"
)

// TaskState represents the state of an EtcdOpsTask.
type TaskState string

const (
	// TaskStateFailed indicates that the task has failed.
	TaskStateFailed TaskState = "Failed"
	// TaskStatePending indicates that the task is pending and has not been picked up for execution.
	TaskStatePending TaskState = "Pending"
	// TaskStateRejected indicates that the task has been rejected.
	TaskStateRejected TaskState = "Rejected"
	// TaskStateSucceeded indicates that the task has succeeded.
	TaskStateSucceeded TaskState = "Succeeded"
	// TaskStateInProgress indicates that the task is currently in progress.
	TaskStateInProgress TaskState = "InProgress"
)

// OperationState represents the state of an operation within an EtcdOpsTask.
type OperationState string

const (
	// OperationStateInProgress represents an operation that is currently in progress.
	OperationStateInProgress OperationState = "InProgress"
	// OperationStateCompleted represents an operation that has completed successfully.
	OperationStateCompleted OperationState = "Completed"
	// OperationStateFailed represents an operation that has failed.
	OperationStateFailed OperationState = "Failed"
)

// OperationPhase represents the lifecycle phase of an operation within an EtcdOpsTask.
type OperationPhase string

const (
	// OperationPhaseAdmit represents the phase where the operation has passed the pre-conditions.
	OperationPhaseAdmit OperationPhase = "Admit"
	// OperationPhaseRunning represents the phase where the operation is currently running.
	OperationPhaseRunning OperationPhase = "Run"
	// OperationPhaseCleanup represents the phase where the operation is cleaning up after completion.
	OperationPhaseCleanup OperationPhase = "Cleanup"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EtcdOpsTask represents an out-of-band operator task resource.
type EtcdOpsTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of the EtcdOpsTask resource.
	// +kubebuilder:validation:Required
	Spec EtcdOpsTaskSpec `json:"spec"`

	// Status is the most recently observed status of the EtcdOpsTask resource.
	// +optional
	Status EtcdOpsTaskStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// EtcdOpsTaskList contains a list of EtcdOpsTask objects.
type EtcdOpsTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EtcdOpsTask `json:"items"`
}

// EtcdOpsTaskSpec defines the desired state of EtcdOpsTask.
type EtcdOpsTaskSpec struct {

	// Config defines the configuration for the EtcdOpsTask.
	// Only one of the configurations can be specified at a time.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="The config field in spec is immutable and cannot be changed after creation."
	Config EtcdOpsTaskConfig `json:"config"`

	// TTLSecondsAfterFinished is the time-to-live (in seconds) to garbage collect the
	// related resource(s) of the task once it has been completed.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="The ttlSecondsAfterFinished field in spec is immutable and cannot be changed after creation."
	// +kubebuilder:default:=3600
	TTLSecondsAfterFinished int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// EtcdRef refers to the name and namespace of the corresponding
	// Etcd owner for which the task has been invoked.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="The etcdRef field in spec is immutable and cannot be changed after creation."
	EtcdRef *EtcdReference `json:"etcdRef,omitempty"`
}

// EtcdOpsTaskConfig defines the configuration for the EtcdOpsTask.
// Only one of the configurations can be specified at a time.
type EtcdOpsTaskConfig struct {
	// OnDemandSnapshotConfig specifies configuration for on-demand snapshot tasks.
	// +optional
	OnDemandSnapshot *OnDemandSnapshotConfig `json:"onDemandSnapshotConfig,omitempty"` // +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
}

// EtcdReference is a reference to an Etcd resource on which the task is to be performed.
type EtcdReference struct {
	// Name is the name of the Etcd resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Etcd resource.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// EtcdOpsTaskStatus defines the observed state of EtcdOpsTask.
type EtcdOpsTaskStatus struct {
	// State is the last known state of the task.
	// +optional
	State *TaskState `json:"state,omitempty"`

	// LastTransitionTime is the last time the task transitioned from one state to another.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// InitiatedAt is the time at which the task has moved from "pending" state to the inProgress state.
	// +optional
	InitiatedAt *metav1.Time `json:"initiatedAt,omitempty"`

	// LastErrors represents the errors when processing the task.
	// +kubebuilder:validation:MaxItems=10
	// +optional
	LastErrors []EtcdOpsTaskLastError `json:"lastErrors,omitempty"`

	// LastOperation captures the last operation status if task involves many stages.
	// +optional
	LastOperation *EtcdOpsLastOperation `json:"lastOperation,omitempty"`
}

// EtcdOpsTaskLastError represents the last error encountered while processing the task.
type EtcdOpsTaskLastError struct {
	// Code is an error code that uniquely identifies an error.
	Code ErrorCode `json:"code"`

	// Description is a human-readable message indicating details of the error.
	Description string `json:"description"`

	// ObservedAt is the time at which the error was observed.
	ObservedAt metav1.Time `json:"observedAt"`
}

// EtcdOpsLastOperation represents the last known operation status of an EtcdOpsTask.
type EtcdOpsLastOperation struct {
	// State is the status of the last operation, one of pending, progress, completed, failed.
	State OperationState `json:"state"`

	// Phase represents the current phase of the operation with respect to the interface methods.
	Phase OperationPhase `json:"phase"`

	// LastTransitionTime records the timestamp of the most recent state transition, marking when the operation moved from its previous state to the current value specified in the 'State' field.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// Description is a human readable message indicating details about the last operation.
	Description string `json:"description"`
}

// IsCompleted returns true if the task is completed.
func (t *EtcdOpsTask) IsCompleted() bool {
	if t.Status.State == nil {
		return false
	}
	return *t.Status.State == TaskStateSucceeded || *t.Status.State == TaskStateFailed || *t.Status.State == TaskStateRejected
}

// IsMarkedForDeletion returns true if the deletion timestamp is set.
func (t *EtcdOpsTask) IsMarkedForDeletion() bool {
	return t.ObjectMeta.DeletionTimestamp != nil
}

// HasTTLExpired returns true if the TTL after finished has expired.
func (t *EtcdOpsTask) HasTTLExpired() bool {
	return t.GetTimeToExpiry() <= 0
}

// GetTTL returns the TTL duration for the task as set in the spec.
func (t *EtcdOpsTask) GetTTL() time.Duration {
	return time.Duration(t.Spec.TTLSecondsAfterFinished) * time.Second
}

// GetTimeToExpiry returns the remaining duration until the task's TTL expires.
// If the task is not completed, it returns zero.
// If LastTransitionTime is nil, uses InitiatedAt; if that is also nil, uses CreationTimestamp.
func (t *EtcdOpsTask) GetTimeToExpiry() time.Duration {
	var baseTime time.Time
	switch {
	case t.Status.LastTransitionTime != nil:
		baseTime = t.Status.LastTransitionTime.Time
	case t.Status.InitiatedAt != nil:
		baseTime = t.Status.InitiatedAt.Time
	default:
		// Fallback to CreationTimestamp if both LastTransitionTime and InitiatedAt are not set.
		// This covers cases where the task transistioned to rejected state
		// 	- has not yet transitioned to in-progress, since admit failed.
		// 	- unsupported task type.
		// Ensures a valid base time for TTL expiry calculation in all lifecycle states.
		baseTime = t.ObjectMeta.CreationTimestamp.Time
	}
	expiry := baseTime.Add(t.GetTTL())
	remaining := expiry.Sub(time.Now().UTC())
	if remaining < 0 {
		return 0
	}
	return remaining
}

// OnDemandSnapshotConfig defines the configuration for on-demand snapshot tasks.
type OnDemandSnapshotConfig struct {
	// Type specifies the type of snapshot: "full" or "delta".
	// +kubebuilder:validation:Enum=full;delta
	// +kubebuilder:validation:Required
	Type OnDemandSnapshotType `json:"type"`

	// IsFinal indicates if this is the final snapshot. Only applicable for full snapshots.
	// +optional
	IsFinal *bool `json:"isFinal,omitempty"`

	// TimeoutSeconds is the timeout in seconds for the snapshot operation.
	// +optional
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`
}
