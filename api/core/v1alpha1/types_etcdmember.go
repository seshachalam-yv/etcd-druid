// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EtcdMemberState is the top-level state of an EtcdMember.
type EtcdMemberState string

const (
	// EtcdMemberStateNew is the initial state of a newly created EtcdMember.
	EtcdMemberStateNew EtcdMemberState = "New"
	// EtcdMemberStateInitializing indicates backup-restore has started initialization (DB validation / restoration).
	EtcdMemberStateInitializing EtcdMemberState = "Initializing"
	// EtcdMemberStateStarting indicates the member is joining the cluster as a learner.
	EtcdMemberStateStarting EtcdMemberState = "Starting"
	// EtcdMemberStateStarted indicates the member is a full voting member (Leader or Follower).
	EtcdMemberStateStarted EtcdMemberState = "Started"
)

// EtcdMemberSubState is the sub-state of an EtcdMember within its top-level state.
type EtcdMemberSubState string

const (
	// EtcdMemberSubStateNew is the sub-state for a newly created member.
	EtcdMemberSubStateNew EtcdMemberSubState = "New"
	// EtcdMemberSubStateDBValidationSanity indicates sanity DB validation is in progress.
	EtcdMemberSubStateDBValidationSanity EtcdMemberSubState = "DBValidationSanity"
	// EtcdMemberSubStateDBValidationFull indicates full DB validation is in progress.
	EtcdMemberSubStateDBValidationFull EtcdMemberSubState = "DBValidationFull"
	// EtcdMemberSubStateRestoration indicates DB restoration is in progress (single-node only).
	EtcdMemberSubStateRestoration EtcdMemberSubState = "Restoration"
	// EtcdMemberSubStatePendingLearner indicates the member is waiting to join as a learner.
	EtcdMemberSubStatePendingLearner EtcdMemberSubState = "PendingLearner"
	// EtcdMemberSubStateLearner indicates the member has joined as a learner and is syncing from the leader.
	EtcdMemberSubStateLearner EtcdMemberSubState = "Learner"
	// EtcdMemberSubStateFollower indicates the member is a voting follower.
	EtcdMemberSubStateFollower EtcdMemberSubState = "Follower"
	// EtcdMemberSubStateLeader indicates the member is the cluster leader.
	EtcdMemberSubStateLeader EtcdMemberSubState = "Leader"
)

// EtcdMemberTransitionReason is the reason code for a state transition.
type EtcdMemberTransitionReason string

const (
	// EtcdMemberReasonClusterScaledUp indicates the member was added due to a cluster scale-up.
	EtcdMemberReasonClusterScaledUp EtcdMemberTransitionReason = "ClusterScaledUp"
	// EtcdMemberReasonNewSingleNodeClusterCreated indicates a new single-node cluster was created.
	EtcdMemberReasonNewSingleNodeClusterCreated EtcdMemberTransitionReason = "NewSingleNodeClusterCreated"
	// EtcdMemberReasonDetectedPreviousCleanExit indicates a clean previous exit was detected; sanity validation started.
	EtcdMemberReasonDetectedPreviousCleanExit EtcdMemberTransitionReason = "DetectedPreviousCleanExit"
	// EtcdMemberReasonDetectedPreviousUncleanExit indicates an unclean previous exit was detected; full validation started.
	EtcdMemberReasonDetectedPreviousUncleanExit EtcdMemberTransitionReason = "DetectedPreviousUncleanExit"
	// EtcdMemberReasonDBValidationFailed indicates DB validation failed.
	EtcdMemberReasonDBValidationFailed EtcdMemberTransitionReason = "DBValidationFailed"
	// EtcdMemberReasonDBValidationSucceeded indicates DB validation succeeded.
	EtcdMemberReasonDBValidationSucceeded EtcdMemberTransitionReason = "DBValidationSucceeded"
	// EtcdMemberReasonRestorationSucceeded indicates DB restoration succeeded.
	EtcdMemberReasonRestorationSucceeded EtcdMemberTransitionReason = "RestorationSucceeded"
	// EtcdMemberReasonWaitingToJoinAsLearner indicates the member is waiting to join as a learner.
	EtcdMemberReasonWaitingToJoinAsLearner EtcdMemberTransitionReason = "WaitingToJoinAsLearner"
	// EtcdMemberReasonJoinedAsLearner indicates the member successfully joined as a learner.
	EtcdMemberReasonJoinedAsLearner EtcdMemberTransitionReason = "JoinedAsLearner"
	// EtcdMemberReasonPromotedAsVotingMember indicates the learner was promoted to a voting member.
	EtcdMemberReasonPromotedAsVotingMember EtcdMemberTransitionReason = "PromotedAsVotingMember"
	// EtcdMemberReasonGainedClusterLeadership indicates the member became the cluster leader.
	EtcdMemberReasonGainedClusterLeadership EtcdMemberTransitionReason = "GainedClusterLeadership"
	// EtcdMemberReasonLostClusterLeadership indicates the member lost cluster leadership.
	EtcdMemberReasonLostClusterLeadership EtcdMemberTransitionReason = "LostClusterLeadership"
)

// EtcdMemberRestorationStatus is the status of the last restoration operation.
type EtcdMemberRestorationStatus string

const (
	// EtcdMemberRestorationStatusInProgress indicates restoration is in progress.
	EtcdMemberRestorationStatusInProgress EtcdMemberRestorationStatus = "InProgress"
	// EtcdMemberRestorationStatusSucceeded indicates restoration succeeded.
	EtcdMemberRestorationStatusSucceeded EtcdMemberRestorationStatus = "Succeeded"
	// EtcdMemberRestorationStatusFailed indicates restoration failed.
	EtcdMemberRestorationStatusFailed EtcdMemberRestorationStatus = "Failed"
)

// EtcdMemberRestorationType is the type of restoration.
type EtcdMemberRestorationType string

const (
	// EtcdMemberRestorationTypeFromSnapshot indicates restoration from a backup snapshot.
	EtcdMemberRestorationTypeFromSnapshot EtcdMemberRestorationType = "FromSnapshot"
	// EtcdMemberRestorationTypeFromLeader indicates restoration by syncing from the cluster leader (learner join).
	EtcdMemberRestorationTypeFromLeader EtcdMemberRestorationType = "FromLeader"
)

// EtcdMemberTransition captures a single state transition of an EtcdMember.
type EtcdMemberTransition struct {
	// State is the top-level state the member transitioned to.
	State EtcdMemberState `json:"state"`
	// SubState is the sub-state the member transitioned to.
	// +optional
	SubState *EtcdMemberSubState `json:"subState,omitempty"`
	// Reason is the reason code for the transition.
	Reason EtcdMemberTransitionReason `json:"reason"`
	// TransitionTime is the time the transition occurred.
	TransitionTime metav1.Time `json:"transitionTime"`
	// Message is an optional human-readable message describing the transition.
	// +optional
	Message *string `json:"message,omitempty"`
}

// EtcdMemberVolumeMismatch captures information about a volume mismatch event detected by etcd-steward.
type EtcdMemberVolumeMismatch struct {
	// IdentifiedAt is the time at which the wrong volume mount was identified.
	IdentifiedAt metav1.Time `json:"identifiedAt"`
	// FixedAt is the time at which the correct volume was mounted.
	// +optional
	FixedAt *metav1.Time `json:"fixedAt,omitempty"`
	// VolumeID is the ID of the wrong volume that was mounted.
	VolumeID string `json:"volumeID"`
	// NumRestarts is the number of pod restarts attempted while this volume was mounted.
	// +optional
	NumRestarts *int32 `json:"numRestarts,omitempty"`
}

// EtcdMemberRestoration captures information about the last restoration operation.
type EtcdMemberRestoration struct {
	// Type is the type of restoration (FromSnapshot or FromLeader).
	Type EtcdMemberRestorationType `json:"type"`
	// Status is the status of the restoration.
	Status EtcdMemberRestorationStatus `json:"status"`
	// StartTime is the start time of the restoration.
	StartTime metav1.Time `json:"startTime"`
	// EndTime is the end time of the restoration.
	// +optional
	EndTime *metav1.Time `json:"endTime,omitempty"`
	// Message is an optional human-readable message.
	// +optional
	Message *string `json:"message,omitempty"`
}

// EtcdMemberResourceStatus defines the observed state of an EtcdMember.
type EtcdMemberResourceStatus struct {
	// ID is the etcd member ID.
	// +optional
	ID *string `json:"id,omitempty"`
	// ClusterID is the etcd cluster ID.
	// +optional
	ClusterID *string `json:"clusterID,omitempty"`
	// PeerTLSEnabled indicates whether TLS is enabled for peer communication for this member.
	// +optional
	PeerTLSEnabled *bool `json:"peerTLSEnabled,omitempty"`
	// DBSize is the total storage space used by the etcd DB on this member.
	// +optional
	DBSize *resource.Quantity `json:"dbSize,omitempty"`
	// DBSizeInUse is the logical storage space actively used by the etcd DB (excludes free pages).
	// +optional
	DBSizeInUse *resource.Quantity `json:"dbSizeInUse,omitempty"`
	// Transitions is the list of state transitions for this member, in chronological order.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Transitions []EtcdMemberTransition `json:"transitions,omitempty"`
	// VolumeMismatches captures volume mismatch events detected for this member.
	// +optional
	VolumeMismatches []EtcdMemberVolumeMismatch `json:"volumeMismatches,omitempty"`
	// LastRestoration captures information about the most recent restoration operation.
	// +optional
	LastRestoration *EtcdMemberRestoration `json:"lastRestoration,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EtcdMember is the Schema for the etcdmembers API.
// Each EtcdMember resource represents one member of an etcd cluster managed by etcd-druid.
// etcd-druid creates and deletes EtcdMember resources as part of Etcd cluster reconciliation.
// The status of an EtcdMember is updated exclusively by etcd-steward (the per-member lifecycle agent).
type EtcdMember struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status EtcdMemberResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// EtcdMemberList contains a list of EtcdMember.
type EtcdMemberList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EtcdMember `json:"items"`
}
