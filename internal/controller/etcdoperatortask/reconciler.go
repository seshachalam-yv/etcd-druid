/*
SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
and Gardener contributors

SPDX-License-Identifier: Apache-2.0
*/

package etcdoperatortask

import (
	"context"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/operatortask"
	"github.com/gardener/etcd-druid/internal/operatortask/ondemandsnapshot"
	"github.com/gardener/etcd-druid/internal/tasks"
	"k8s.io/client-go/tools/record"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "etcdoperatortask-controller"
	FinalizerName  = "etcd-druid.gardener.cloud/etcd-operator-task"
)

type reconcileFn func(ctx context.Context, taskObjKey client.ObjectKey, operatorTask operatortask.OperatorTask) ctrlutils.ReconcileStepResult

type Reconciler struct {
	client   client.Client
	recorder record.EventRecorder
	logger   logr.Logger
	config   *Config
	registry *operatortask.OperatorTaskRegistry
}

func New(mgr manager.Manager, cfg *Config) *Reconciler {
	registry := operatortask.NewOperatorTaskRegistry()
	logger := log.Log.WithName(ControllerName)
	registry.Register(v1alpha1.EtcdOperatorTaskTypeOnDemandSnapshot, ondemandsnapshot.New)
	// Register more executors as needed

	return &Reconciler{
		client:   mgr.GetClient(),
		recorder: mgr.GetEventRecorderFor(ControllerName),
		logger:   logger,
		config:   cfg,
		registry: registry,
	}
}

// +kubebuilder:rbac:groups=druid.gardener.cloud,resources=etcdoperatortasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=druid.gardener.cloud,resources=etcdoperatortasks/status,verbs=list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=druid.gardener.cloud,resources=etcds,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=druid.gardener.cloud,resources=etcds/status,verbs=get;create;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;get;list

// Reconcile implements the main reconciliation loop for EtcdOperatorTask.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	task := &v1alpha1.EtcdOperatorTask{}
	if err := r.client.Get(ctx, req.NamespacedName, task); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	logger := r.logger.WithValues("runId", string(controller.ReconcileIDFromContext(ctx)))
	operatorTask, err := r.registry.CreateOperatorTaskInstance(r.client, logger, task)
	if err != nil {
		return reconcile.Result{}, err
	}
	if task.IsCompleted() || task.IsMarkedForDeletion() {
		return r.triggerDeletionFlow(ctx, operatorTask, task).ReconcileResult()
	}

	return r.reconcileTask(tasks.TaskContext{Context: ctx, Logger: logger}, client.ObjectKeyFromObject(task), operatorTask).ReconcileResult()
}
