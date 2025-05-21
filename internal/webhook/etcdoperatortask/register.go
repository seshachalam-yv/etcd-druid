// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdoperatortask

import (
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	handlerName = "etcd-operator-task-webhook"
)

// RegisterWithManager registers the EtcdOperatorTask webhook handler with the manager.
func RegisterWithManager(mgr manager.Manager) error {
	h := NewHandler(mgr)
	return h.RegisterWithManager(mgr)
}
