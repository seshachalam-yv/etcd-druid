// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdoperatortask

import (
	"time"

	flag "github.com/spf13/pflag"
)

const (
	workersFlagName                            = "etcd-operator-task-workers"
	requeueIntervalFlagName                    = "etcd-operator-task-requeue-interval"
	enableEtcdSpecAutoReconcileFlagName        = "enable-etcd-spec-auto-reconcile"
	disableEtcdServiceAccountAutomountFlagName = "disable-etcd-serviceaccount-automount"
	etcdStatusSyncPeriodFlagName               = "etcd-status-sync-period"
	etcdMemberNotReadyThresholdFlagName        = "etcd-member-notready-threshold"
	etcdMemberUnknownThresholdFlagName         = "etcd-member-unknown-threshold"
)

const (
	defaultWorkers                     = 3
	defaultRequeueInterval             = 10 * time.Second
	defaultEtcdStatusSyncPeriod        = 15 * time.Second
	defaultEtcdMemberNotReadyThreshold = 5 * time.Minute
	defaultEtcdMemberUnknownThreshold  = 1 * time.Minute

	defaultEnableEtcdSpecAutoReconcile        = false
	defaultDisableEtcdServiceAccountAutomount = false
)

// Config is the configuration struct for the etcd-operator-task controller.
type Config struct {
	// TODO: Add configuration fields as needed.
	// Workers is the number of workers concurrently processing reconciliation requests.
	Workers int
	// EnableEtcdSpecAutoReconcile controls how the Etcd Spec is reconciled. If set to true, then any change in Etcd spec
	// will automatically trigger a reconciliation of the Etcd resource. If set to false, then an operator needs to
	// explicitly set gardener.cloud/operation=reconcile annotation on the Etcd resource to trigger reconciliation
	// of the Etcd spec.
	// NOTE: Decision to enable it should be carefully taken as spec updates could potentially result in rolling update
	// of the StatefulSet which will cause a minor downtime for a single node etcd cluster and can potentially cause a
	// downtime for a multi-node etcd cluster.
	EnableEtcdSpecAutoReconcile bool
	// DisableEtcdServiceAccountAutomount controls the auto-mounting of service account token for ETCD StatefulSets.
	DisableEtcdServiceAccountAutomount bool
	// EtcdStatusSyncPeriod is the duration after which an event will be re-queued ensuring ETCD status synchronization.
	EtcdStatusSyncPeriod time.Duration
	RequeueInterval      time.Duration
}

// NewDefaultConfig returns a new Config with default values.
func NewDefaultConfig() *Config {
	return &Config{}
}

// InitFromFlags initializes the Config from the provided CLI flag set.
func (c *Config) InitFromFlags(fs *flag.FlagSet) {
	// No flags to register yet. Add flag registrations here when needed.
	// fs.IntVar(&c.Workers, workersFlagName, defaultWorkers,
	// 	"Number of workers spawned for concurrent reconciles of etcd spec and status changes. If not specified then default of 3 is assumed.")
	// flag.BoolVar(&c.EnableEtcdSpecAutoReconcile, enableEtcdSpecAutoReconcileFlagName, defaultEnableEtcdSpecAutoReconcile,
	// 	fmt.Sprintf("If true then automatically reconciles Etcd Spec. If false, waits for explicit annotation `%s: %s` to be placed on the Etcd resource to trigger reconcile.", druidv1alpha1.DruidOperationAnnotation, druidv1alpha1.DruidOperationReconcile))
	// fs.BoolVar(&c.DisableEtcdServiceAccountAutomount, disableEtcdServiceAccountAutomountFlagName, defaultDisableEtcdServiceAccountAutomount,
	// 	"If true then .automountServiceAccountToken will be set to false for the ServiceAccount created for etcd StatefulSets.")
	// fs.DurationVar(&c.EtcdStatusSyncPeriod, etcdStatusSyncPeriodFlagName, defaultEtcdStatusSyncPeriod,
	// 	"Period after which an etcd status sync will be attempted.")
	// fs.DurationVar(&c.RequeueInterval, requeueIntervalFlagName, defaultRequeueInterval,
	// 	"Period after which an event will be re-queued.")
}
