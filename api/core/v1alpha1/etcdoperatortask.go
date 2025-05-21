// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type OnDemandSnapshotType string

const (
	// OnDemandSnapshotTypeFull represents a full snapshot.
	OnDemandSnapshotTypeFull OnDemandSnapshotType = "full"
	// OnDemandSnapshotTypeDelta represents a delta snapshot.
	OnDemandSnapshotTypeDelta OnDemandSnapshotType = "delta"
)

type TaskState string

const (
	TaskStateFailed     TaskState = "Failed"
	TaskStatePending    TaskState = "Pending"
	TaskStateRejected   TaskState = "Rejected"
	TaskStateSucceeded  TaskState = "Succeeded"
	TaskStateInProgress TaskState = "InProgress"
)

type OperationState string

const (
	OperationStateInProgress OperationState = "InProgress"
	OperationStateCompleted  OperationState = "Completed"
	OperationStateFailed     OperationState = "Failed"
)

type OperationPhase string

const (
	OperationPhaseAdmit   OperationPhase = "Admit"
	OperationPhaseRunning OperationPhase = "Run"
	OperationPhaseCleanup OperationPhase = "Cleanup"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EtcdOperatorTask represents an out-of-band operator task resource.
type EtcdOperatorTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of the EtcdOperatorTask resource.
	// +kubebuilder:validation:Required
	Spec EtcdOperatorTaskSpec `json:"spec"`

	// Status is the most recently observed status of the EtcdOperatorTask resource.
	// +optional
	Status EtcdOperatorTaskStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// EtcdOperatorTaskList contains a list of EtcdOperatorTask objects.
type EtcdOperatorTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EtcdOperatorTask `json:"items"`
}

// EtcdOperatorTaskSpec defines the desired state of EtcdOperatorTask.
type EtcdOperatorTaskSpec struct {

	// Config defines the configuration for the EtcdOperatorTask.
	// Only one of the configurations can be specified at a time.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="The config field in spec is immutable and cannot be changed after creation."
	Config EtcdOperatorTaskConfig `json:"config"`

	// TTLSecondsAfterFinished is the time-to-live (in seconds) to garbage collect the
	// related resource(s) of the task once it has been completed.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="The ttlSecondsAfterFinished field in spec is immutable and cannot be changed after creation."
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// EtcdRef refers to the name and namespace of the corresponding
	// Etcd owner for which the task has been invoked.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="The etcdRef field in spec is immutable and cannot be changed after creation."
	EtcdRef *EtcdReference `json:"etcdRef,omitempty"`
}

// EtcdOperatorTaskConfig defines the configuration for the EtcdOperatorTask.
// Only one of the configurations can be specified at a time.
type EtcdOperatorTaskConfig struct {
	// OnDemandSnapshotConfig specifies configuration for on-demand snapshot tasks.
	// +optional
	OnDemandSnapshot *OnDemandSnapshotConfig `json:"onDemandSnapshotConfig,omitempty"` // +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
}

type EtcdReference struct {
	// Name is the name of the Etcd resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Etcd resource.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

type EtcdOperatorTaskStatus struct {
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
	LastErrors []EtcdOperatorTaskLastError `json:"lastErrors,omitempty"`

	// LastOperation captures the last operation status if task involves many stages.
	// +optional
	LastOperation *EtcdOperatorLastOperation `json:"lastOperation,omitempty"`
}

type EtcdOperatorTaskLastError struct {
	// Code is an error code that uniquely identifies an error.
	Code ErrorCode `json:"code"`

	// Description is a human-readable message indicating details of the error.
	Description string `json:"description"`

	// ObservedAt is the time at which the error was observed.
	ObservedAt metav1.Time `json:"observedAt"`
}

type EtcdOperatorLastOperation struct {
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
func (t *EtcdOperatorTask) IsCompleted() bool {
	if t.Status.State == nil {
		return false
	}
	return *t.Status.State == TaskStateSucceeded || *t.Status.State == TaskStateFailed || *t.Status.State == TaskStateRejected
}

// IsMarkedForDeletion returns true if the deletion timestamp is set.
func (t *EtcdOperatorTask) IsMarkedForDeletion() bool {
	return t.ObjectMeta.DeletionTimestamp != nil
}

// TTLHasExpired returns true if the TTL after finished has expired.
func (t *EtcdOperatorTask) HasTTLExpired() bool {
	if t.Spec.TTLSecondsAfterFinished == nil || t.Status.InitiatedAt.IsZero() {
		return false
	}
	if !t.IsCompleted() {
		return false
	}
	lastTransitionTime := t.Status.LastTransitionTime
	expiry := lastTransitionTime.Add(time.Duration(*t.Spec.TTLSecondsAfterFinished) * time.Second)
	return time.Now().After(expiry)
}

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
