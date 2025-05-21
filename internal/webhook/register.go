// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"github.com/gardener/etcd-druid/internal/webhook/etcdcomponents"
	"github.com/gardener/etcd-druid/internal/webhook/etcdoperatortask"

	"golang.org/x/exp/slog"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Register registers all etcd-druid webhooks with the controller manager.
func Register(mgr ctrl.Manager, config *Config) error {
	// Add Etcd Components webhook to the manager
	if config.EtcdComponents.Enabled {
		etcdComponentsWebhook, err := etcdcomponents.NewHandler(
			mgr,
			config.EtcdComponents,
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
		slog.Info("Registering EtcdOperatorTask Webhook with manager")
		if err := etcdoperatortask.RegisterWithManager(mgr); err != nil {
			return err
		}
	}
	return nil
}
