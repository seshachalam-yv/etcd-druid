// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdmember

import (
	"github.com/gardener/etcd-druid/internal/component"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterIfEnabled registers the EtcdMember component operator with the given registry
// if the UseEtcdSteward feature gate is enabled.
//
// Usage in internal/controller/etcd/reconciler.go in createAndInitializeOperatorRegistry():
//
//	if features.DefaultFeatureGates.IsEnabled(features.UseEtcdSteward) {
//	    etcdmember.RegisterIfEnabled(reg, client)
//	}
//
// NOTE: This is gated behind the UseEtcdSteward feature gate because EtcdMember CRs are only
// relevant when using etcd-steward as the sidecar container. The etcd-steward sidecar publishes
// member state to EtcdMember resources, replacing the legacy lease-based approach.
func RegisterIfEnabled(reg component.Registry, c client.Client) {
	reg.Register(component.EtcdMemberKind, New(c))
}
