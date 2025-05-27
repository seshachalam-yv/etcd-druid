// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdoperatortask

import (
	flag "github.com/spf13/pflag"
)

const (
	enableEtcdOperatorTaskWebhookFlagName = "enable-etcd-operator-task-webhook"
	defaultEnableWebhook                  = true
)

type Config struct {
	// Enabled indicates whether the EtcdOperatorTask validating webhook is enabled.
	Enabled bool
	// ExemptServiceAccounts is a list of service accounts that are exempt from EtcdOperatorTask validating webhook checks.
	ExemptServiceAccounts []string
	// todo: Check if ReconcilerServiceAccount is needed
}

func InitFromFlags(fs *flag.FlagSet, cfg *Config) {
	fs.BoolVar(&cfg.Enabled, enableEtcdOperatorTaskWebhookFlagName, defaultEnableWebhook,
		"Enable EtcdOperatorTask validating webhook.")
}
