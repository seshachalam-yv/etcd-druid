// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type EtcdOperatorTaskType string

const (
	EtcdOperatorTaskTypeOnDemandSnapshot EtcdOperatorTaskType = "OnDemandSnapshot"
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

	// Spec is the specification of the EtcdOperatorTask resource.
	Spec EtcdOperatorTaskSpec `json:"spec"`
	// Status is most recently observed status of the EtcdOperatorTask resource.
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

// EtcdOperatorTaskSpec is the spec for an EtcdOperatorTask resource.
type EtcdOperatorTaskSpec struct {
	// Type specifies the type of out-of-band operator task to be performed.
	Type EtcdOperatorTaskType `json:"type"`

	// Config is a task-specific configuration.
	// +optional
	Config Config `json:"config,omitempty"`

	// TTLSecondsAfterFinished is the time-to-live to garbage collect the
	// related resource(s) of the task once it has been completed.
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// OwnerEtcdReference refers to the name and namespace of the corresponding
	// Etcd owner for which the task has been invoked.
	EtcdRef *EtcdReference `json:"etcdRef"`
}

type Config struct {
	// +optional
	OnDemandSnapshotConfig *OnDemandSnapshotConfig `json:"onDemandSnapshotConfig,omitempty"`
	// +optional
	TestConfig             *TestConfig             `json:"testConfig,omitempty"`
}

type OnDemandSnapshotConfig struct {
	// SnapshotType specifies the type of snapshot to be taken.
	// +required
	// +kubebuilder:validation:Enum=full;delta
	SnapshotType *string `json:"snapshotType"`

	// TimeoutSeconds specifies the timeout for the snapshot operation in seconds.
	// +optional
	// +kubebuilder:default:=60
	TimeoutSeconds *int `json:"timeoutSeconds,omitempty"`
}

type TestConfig struct{
	Test bool `json:"test"`
}

// EtcdReference is a custom struct to hold the name and namespace of the Etcd owner.
type EtcdReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// EtcdOperatorTaskStatus is the status for an EtcdOperatorTask resource.
type EtcdOperatorTaskStatus struct {
	// ObservedGeneration is the most recent generation observed for the resource.
	ObservedGeneration *int64 `json:"observedGeneration,omitempty"`
	// State is the last known state of the task.
	State TaskState `json:"state"`
	// +optional
	// InitiatedAt is the time at which the task has moved from "pending" state to any other state.
	InitiatedAt metav1.Time `json:"initiatedAt,omitempty"`
	// LastErrors represents the errors when processing the task.
	// +optional
	LastErrors []EtcdOperatorTaskLastError `json:"lastErrors"`
	// Captures the last operation status if task involves many stages.
	// +optional
	LastOperation *EtcdOperatorLastOperation `json:"lastOperation,omitempty"`
}

type EtcdOperatorLastOperation struct {
	// Status of the last operation, one of pending, progress, completed, failed.
	State OperationState `json:"state"`
	// LastTransitionTime is the time at which the operation state last transitioned from one state to another.
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
	// A human readable message indicating details about the last operation.
	Description string `json:"description"`
}

// LastError stores details of the most recent error encountered for the task.
type EtcdOperatorTaskLastError struct {
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

// NamespacedName is a copy of types.NamespacedName for CRD serialization.
type NamespacedName struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// IsCompleted returns true if the task is completed.
func (t *EtcdOperatorTask) IsCompleted() bool {
	return t.Status.State == TaskStateSucceeded || t.Status.State == TaskStateFailed || t.Status.State == TaskStateRejected
}

// IsMarkedForDeletion returns true if the deletion timestamp is set.
func (t *EtcdOperatorTask) IsMarkedForDeletion() bool {
	return t.ObjectMeta.DeletionTimestamp != nil
}

// TTLHasExpired returns true if the TTL after finished has expired.
func (t *EtcdOperatorTask) TTLHasExpired() bool {
	if t.Spec.TTLSecondsAfterFinished == nil || t.Status.InitiatedAt.IsZero() {
		return false
	}
	expiry := t.Status.InitiatedAt.Add(time.Duration(*t.Spec.TTLSecondsAfterFinished) * time.Second)
	return time.Now().After(expiry)
}
