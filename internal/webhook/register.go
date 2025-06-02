// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	druidconfigv1alpha1 "github.com/gardener/etcd-druid/api/config/v1alpha1"
	"github.com/gardener/etcd-druid/internal/webhook/etcdcomponentprotection"

	"golang.org/x/exp/slog"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Register registers all etcd-druid webhooks with the controller manager.
func Register(mgr ctrl.Manager, config druidconfigv1alpha1.WebhookConfiguration) error {
	// Add Etcd Components webhook to the manager
	if config.EtcdComponentProtection.Enabled {
		etcdComponentsWebhook, err := etcdcomponentprotection.NewHandler(
			mgr,
			config.EtcdComponentProtection,
		)
		if err != nil {
			return err
		}
		slog.Info("Registering EtcdComponents Webhook with manager")
		if err := etcdComponentsWebhook.RegisterWithManager(mgr); err != nil {
			return err
		}
	}
	// Add EtcdOperatorTask webhook to the manager
	if config.EtcdOperatorTask.Enabled {
		etcdOperatorTaskWebhook, err := etcdoperatortask.NewHandler(
			mgr,
			config.EtcdOperatorTask,
		)
		if err != nil {
			return err
		}
		slog.Info("Registering EtcdOperatorTask Webhook with manager")
		if err := etcdOperatorTaskWebhook.RegisterWithManager(mgr); err != nil {
			return err
		}
	}
	return nil
}
