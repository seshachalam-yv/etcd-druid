// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: After adding these types, you MUST run the following to generate deepcopy and CRD manifests:
//   cd api && make generate
// The zz_generated.deepcopy.go file must be regenerated to include DeepCopy methods for the new types.
// The CRD YAML must be generated and placed in api/core/v1alpha1/crds/.

// ---------------------------------- Annotations ----------------------------------

const (
	// AnnotationCreateAsLearner is an annotation set by druid on an EtcdMember resource
	// to indicate that the corresponding etcd member should join the cluster as a learner.
	// This annotation is added to newly created members during scale-up.
	// The etcd-member should remove this annotation once it is promoted to a voting member.
	AnnotationCreateAsLearner = "druid.gardener.cloud/create-as-learner"
)

// ---------------------------------- MemberState ----------------------------------

// MemberState represents the top-level state of an etcd cluster member.
// +kubebuilder:validation:Enum=New;Initializing;Starting;Started
type MemberState string

const (
	// MemberStateNew indicates a newly created etcd member that has not yet started.
	// This is the initial/start state for all newly created etcd members.
	MemberStateNew MemberState = "New"
	// MemberStateInitializing indicates that the backup-restore container has started
	// initialization, performing DB validation and optionally restoration.
	MemberStateInitializing MemberState = "Initializing"
	// MemberStateStarting indicates that the etcd process is being started.
	// The member may be waiting to join as a learner or is currently a learner.
	MemberStateStarting MemberState = "Starting"
	// MemberStateStarted indicates that the etcd member has fully started
	// and is a voting member (Leader or Follower).
	MemberStateStarted MemberState = "Started"
)

// ---------------------------------- MemberSubState ----------------------------------

// MemberSubState provides additional detail within a MemberState.
// +kubebuilder:validation:Enum=DBValidationSanity;DBValidationFull;Restoration;PendingLearner;Learner;Follower;Leader
type MemberSubState string

const (
	// MemberSubStateDBValidationSanity indicates that a sanity DB validation is in progress.
	// This sub-state is valid when the top-level state is Initializing.
	MemberSubStateDBValidationSanity MemberSubState = "DBValidationSanity"
	// MemberSubStateDBValidationFull indicates that a full DB validation is in progress.
	// This sub-state is valid when the top-level state is Initializing.
	MemberSubStateDBValidationFull MemberSubState = "DBValidationFull"
	// MemberSubStateRestoration indicates that restoration of the etcd DB from backup is in progress.
	// This sub-state is valid when the top-level state is Initializing.
	// An etcd member transitions to this sub-state only in a single-node cluster.
	MemberSubStateRestoration MemberSubState = "Restoration"
	// MemberSubStatePendingLearner indicates that the member is waiting to be added as a learner.
	// Since only one learner can be added at a time, the member may wait in this state.
	// This sub-state is valid when the top-level state is Starting.
	MemberSubStatePendingLearner MemberSubState = "PendingLearner"
	// MemberSubStateLearner indicates that the member has been added as a learner
	// and is syncing its DB from the leader.
	// This sub-state is valid when the top-level state is Starting.
	MemberSubStateLearner MemberSubState = "Learner"
	// MemberSubStateFollower indicates that the member is a voting follower in the cluster.
	// This sub-state is valid when the top-level state is Started.
	MemberSubStateFollower MemberSubState = "Follower"
	// MemberSubStateLeader indicates that the member is the leader of the cluster.
	// This sub-state is valid when the top-level state is Started.
	MemberSubStateLeader MemberSubState = "Leader"
)

// ---------------------------------- Restoration Types ----------------------------------

// RestorationType defines the type of restoration performed on an etcd member.
// +kubebuilder:validation:Enum=FromSnapshot;FromLeader
type RestorationType string

const (
	// RestorationTypeFromSnapshot indicates restoration from a backup snapshot.
	RestorationTypeFromSnapshot RestorationType = "FromSnapshot"
	// RestorationTypeFromLeader indicates restoration by learning from the cluster leader.
	RestorationTypeFromLeader RestorationType = "FromLeader"
)

// ---------------------------------- Operation Status ----------------------------------

// OperationStatus defines the status of an operation (restoration or defragmentation).
// +kubebuilder:validation:Enum=InProgress;Succeeded;Failed
type OperationStatus string

const (
	// OperationStatusInProgress indicates that the operation is currently in progress.
	OperationStatusInProgress OperationStatus = "InProgress"
	// OperationStatusSucceeded indicates that the operation completed successfully.
	OperationStatusSucceeded OperationStatus = "Succeeded"
	// OperationStatusFailed indicates that the operation failed.
	OperationStatusFailed OperationStatus = "Failed"
)

// ---------------------------------- EtcdMember Resource ----------------------------------

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=em
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="SubState",type=string,JSONPath=`.status.subState`
// +kubebuilder:printcolumn:name="MemberID",type=string,JSONPath=`.status.id`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// EtcdMember represents a single member of an etcd cluster.
// It captures the state and operational information of an individual etcd member,
// enabling etcd-druid to perform informed orchestration and remediation.
type EtcdMember struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            EtcdMemberResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// EtcdMemberList contains a list of EtcdMember resources.
type EtcdMemberList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EtcdMember `json:"items"`
}

// ---------------------------------- EtcdMemberResourceStatus ----------------------------------

// EtcdMemberResourceStatus defines the observed state of an EtcdMember resource.
// All fields are updated exclusively by the corresponding etcd member (backup-sidecar/etcd-steward);
// etcd-druid only reads this information.
// NOTE: This type is named EtcdMemberResourceStatus to avoid collision with the legacy
// EtcdMemberStatus type in etcd.go (used in Etcd.Status.Members). When the legacy type
// is removed, this can be renamed back to EtcdMemberStatus.
type EtcdMemberResourceStatus struct {
	// ID is the unique etcd member ID assigned by the etcd cluster.
	// +optional
	ID *string `json:"id,omitempty"`
	// ClusterID is the etcd cluster ID this member belongs to.
	// In a well-formed cluster, all members share the same ClusterID.
	// +optional
	ClusterID *string `json:"clusterID,omitempty"`
	// State is the top-level state of the member (New, Initializing, Starting, Started).
	// +optional
	State *MemberState `json:"state,omitempty"`
	// SubState provides additional detail within a State.
	// +optional
	SubState *MemberSubState `json:"subState,omitempty"`
	// PeerTLSEnabled indicates whether TLS is enabled for peer communication of this member.
	// +optional
	PeerTLSEnabled *bool `json:"peerTLSEnabled,omitempty"`
	// DBSize is the total size of the etcd database in bytes, including free pages.
	// +optional
	DBSize *int64 `json:"dbSize,omitempty"`
	// DBSizeInUse is the logical size of the etcd database in use (excluding free pages).
	// The difference (DBSize - DBSizeInUse) indicates how much space can be reclaimed by defragmentation.
	// +optional
	DBSizeInUse *int64 `json:"dbSizeInUse,omitempty"`
	// Snapshots contains information about the latest backup snapshots taken by this member.
	// Only the leading backup-sidecar (associated with the etcd leader) takes snapshots.
	// +optional
	Snapshots *MemberSnapshotStatus `json:"snapshots,omitempty"`
	// LastRestoration captures information about the last restoration operation performed by this member.
	// +optional
	LastRestoration *MemberRestorationStatus `json:"lastRestoration,omitempty"`
	// LastDefragmentation captures information about the last defragmentation operation performed on this member.
	// +optional
	LastDefragmentation *MemberDefragmentationStatus `json:"lastDefragmentation,omitempty"`
	// Transitions records the state transition history of this member.
	// Entries are appended as the member transitions through different states and sub-states.
	// +optional
	Transitions []MemberTransition `json:"transitions,omitempty"`
	// Conditions represents the latest available observations of the member's current state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ---------------------------------- MemberSnapshotStatus ----------------------------------

// MemberSnapshotStatus captures information about the latest backup snapshots.
type MemberSnapshotStatus struct {
	// LastFull captures information about the last full snapshot taken.
	// +optional
	LastFull *SnapshotInfo `json:"lastFull,omitempty"`
	// LastDelta captures information about the last delta snapshot taken.
	// +optional
	LastDelta *SnapshotInfo `json:"lastDelta,omitempty"`
	// AccumulatedDeltaSize is the total size in bytes of delta snapshots accumulated
	// since the last full snapshot. This is used by druid to decide when to trigger
	// snapshot compaction.
	// +optional
	AccumulatedDeltaSize *int64 `json:"accumulatedDeltaSize,omitempty"`
}

// SnapshotInfo captures details about a single snapshot (full or delta).
type SnapshotInfo struct {
	// Timestamp is the time at which the snapshot was taken.
	// +optional
	Timestamp *metav1.Time `json:"timestamp,omitempty"`
	// Name is the name of the snapshot file that was uploaded.
	// +optional
	Name *string `json:"name,omitempty"`
	// Size is the size of the uncompressed snapshot file in bytes.
	// +optional
	Size *int64 `json:"size,omitempty"`
	// StartRevision is the start revision of the etcd DB captured in the snapshot.
	// +optional
	StartRevision *int64 `json:"startRevision,omitempty"`
	// EndRevision is the end revision of the etcd DB captured in the snapshot.
	// +optional
	EndRevision *int64 `json:"endRevision,omitempty"`
}

// ---------------------------------- MemberRestorationStatus ----------------------------------

// MemberRestorationStatus captures information about the last restoration operation.
type MemberRestorationStatus struct {
	// Type indicates the type of restoration (FromSnapshot or FromLeader).
	// +optional
	Type *RestorationType `json:"type,omitempty"`
	// Status indicates the current status of the restoration (InProgress, Succeeded, Failed).
	// +optional
	Status *OperationStatus `json:"status,omitempty"`
	// Reason is a machine-readable reason code for the current status.
	// +optional
	Reason *string `json:"reason,omitempty"`
	// Message is a human-readable message providing additional context.
	// +optional
	Message *string `json:"message,omitempty"`
	// StartTime is the time at which the restoration started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// EndTime is the time at which the restoration ended.
	// +optional
	EndTime *metav1.Time `json:"endTime,omitempty"`
}

// ---------------------------------- MemberDefragmentationStatus ----------------------------------

// MemberDefragmentationStatus captures information about the last defragmentation operation.
type MemberDefragmentationStatus struct {
	// Status indicates the current status of the defragmentation (InProgress, Succeeded, Failed).
	// +optional
	Status *OperationStatus `json:"status,omitempty"`
	// Reason is a machine-readable reason code for the current status.
	// +optional
	Reason *string `json:"reason,omitempty"`
	// Message is a human-readable message providing additional context.
	// +optional
	Message *string `json:"message,omitempty"`
	// StartTime is the time at which the defragmentation started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// EndTime is the time at which the defragmentation ended.
	// +optional
	EndTime *metav1.Time `json:"endTime,omitempty"`
	// InitialDBSize is the size of the etcd DB in bytes prior to defragmentation.
	// +optional
	InitialDBSize *int64 `json:"initialDBSize,omitempty"`
	// FinalDBSize is the size of the etcd DB in bytes after defragmentation.
	// +optional
	FinalDBSize *int64 `json:"finalDBSize,omitempty"`
}

// ---------------------------------- MemberTransition ----------------------------------

// MemberTransition captures a single state transition in the lifecycle of an etcd member.
type MemberTransition struct {
	// State is the top-level state that the member transitioned to.
	// +required
	State MemberState `json:"state"`
	// SubState is the sub-state within the top-level state, if applicable.
	// +optional
	SubState *MemberSubState `json:"subState,omitempty"`
	// Reason is a machine-readable reason code for the transition.
	// +required
	Reason string `json:"reason"`
	// TransitionTime is the time at which the transition occurred.
	// +required
	TransitionTime metav1.Time `json:"transitionTime"`
	// Message is a human-readable message providing additional context for the transition.
	// +optional
	Message *string `json:"message,omitempty"`
}

// ---------------------------------- Transition Reason Constants ----------------------------------

const (
	// TransitionReasonClusterScaledUp indicates a new member was created due to cluster scale-up.
	TransitionReasonClusterScaledUp = "ClusterScaledUp"
	// TransitionReasonNewSingleNodeClusterCreated indicates the first member of a new single-node cluster.
	TransitionReasonNewSingleNodeClusterCreated = "NewSingleNodeClusterCreated"
	// TransitionReasonDetectedPreviousCleanExit indicates a clean exit was detected during initialization.
	TransitionReasonDetectedPreviousCleanExit = "DetectedPreviousCleanExit"
	// TransitionReasonDetectedPreviousUncleanExit indicates an unclean exit was detected during initialization.
	TransitionReasonDetectedPreviousUncleanExit = "DetectedPreviousUncleanExit"
	// TransitionReasonDBValidationFailed indicates that DB validation failed.
	TransitionReasonDBValidationFailed = "DBValidationFailed"
	// TransitionReasonDBValidationSucceeded indicates that DB validation succeeded.
	TransitionReasonDBValidationSucceeded = "DBValidationSucceeded"
	// TransitionReasonRestorationSucceeded indicates that restoration from snapshot succeeded.
	TransitionReasonRestorationSucceeded = "RestorationSucceeded"
	// TransitionReasonWaitingToJoinAsLearner indicates the member is waiting to join as a learner.
	TransitionReasonWaitingToJoinAsLearner = "WaitingToJoinAsLearner"
	// TransitionReasonJoinedAsLearner indicates the member has joined the cluster as a learner.
	TransitionReasonJoinedAsLearner = "JoinedAsLearner"
	// TransitionReasonPromotedAsVotingMember indicates the learner has been promoted to a voting member.
	TransitionReasonPromotedAsVotingMember = "PromotedAsVotingMember"
	// TransitionReasonGainedClusterLeadership indicates the member gained cluster leadership.
	TransitionReasonGainedClusterLeadership = "GainedClusterLeadership"
	// TransitionReasonLostClusterLeadership indicates the member lost cluster leadership.
	TransitionReasonLostClusterLeadership = "LostClusterLeadership"
	// TransitionReasonDBCorruptionDetected indicates DB corruption was detected during initialization.
	TransitionReasonDBCorruptionDetected = "DBCorruptionDetected"
)

// ---------------------------------- Helper Functions ----------------------------------

// GetEtcdMemberName returns the name of the EtcdMember resource for the given ordinal.
func GetEtcdMemberName(etcdObjMeta metav1.ObjectMeta, ordinal int) string {
	return GetOrdinalPodName(etcdObjMeta, ordinal)
}

// GetEtcdMemberNames returns the names of all EtcdMember resources for the given replica count.
func GetEtcdMemberNames(etcdObjMeta metav1.ObjectMeta, replicas int32) []string {
	names := make([]string, replicas)
	for i := range int(replicas) {
		names[i] = GetEtcdMemberName(etcdObjMeta, i)
	}
	return names
}
