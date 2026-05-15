// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// MemberToRemove identifies an etcd member to be removed.
type MemberToRemove struct {
	// Name is the pod name of the member.
	// +required
	Name string `json:"name"`
	// PeerURL is the peer URL of the member.
	// +required
	PeerURL string `json:"peerURL"`
}

// RemoveMembersConfig holds configuration for a RemoveMembers operation.
type RemoveMembersConfig struct {
	// MembersToRemove lists the members to be removed from the etcd cluster.
	// +required
	// +kubebuilder:validation:MinItems=1
	MembersToRemove []MemberToRemove `json:"membersToRemove"`
}
