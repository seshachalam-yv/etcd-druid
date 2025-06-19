// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdopstaskprotection

import (
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	handlerName = "etcd-opstask-webhook"
	webhookPath = "/webhooks/etcdopstask"
)

// RegisterWithManager registers the EtcdOperatorTask webhook handler with the manager.
func (h *Handler) RegisterWithManager(mgr manager.Manager) error {
	webhook := &admission.Webhook{
		Handler:      h,
		RecoverPanic: ptr.To(true),
	}
	mgr.GetWebhookServer().Register(webhookPath, webhook)
	return nil
}
