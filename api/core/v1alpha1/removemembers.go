// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// RemoveMembersConfig defines the configuration for a member removal task.
type RemoveMembersConfig struct {
	// MembersToRemove is the list of etcd members to be removed from the cluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	MembersToRemove []MemberToRemove `json:"membersToRemove"`
}

// MemberToRemove defines an etcd member to be removed.
type MemberToRemove struct {
	// Name is the name of the etcd member (matches the pod name).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// PeerURL is the peer URL of the etcd member to be removed.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	PeerURL string `json:"peerUrl"`
}
