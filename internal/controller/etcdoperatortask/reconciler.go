/*
SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
and Gardener contributors

SPDX-License-Identifier: Apache-2.0
*/

package etcdoperatortask

import (
	"context"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	druiderr "github.com/gardener/etcd-druid/internal/errors"
	"github.com/gardener/etcd-druid/internal/operatortask"
	"github.com/gardener/etcd-druid/internal/operatortask/ondemandsnapshot"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	logger.Info("Reconciling EtcdOperatorTask", "namespace", task.Namespace, "name", task.Name)

	if task.Status.State == v1alpha1.TaskStateRejected {
		return r.triggerRejectionDeletionFlow(ctx, task, logger).ReconcileResult()
	}

	var derr *druiderr.DruidError

	operatorTask, err := r.registry.CreateOperatorTaskInstance(r.client, logger, task)

	if err != nil {
		if derr = druiderr.AsDruidError(err); derr != nil && derr.Code == operatortask.ERR_INVALID_CONFIG {
			if err := r.markTaskRejected(ctx, task, derr, logger); err != nil {
				return ctrlutils.ReconcileWithError(err).GetResult(), nil
			}
		}
		logger.Error(err, "Failed to create operator task instance")
		return reconcile.Result{}, err
	}
	logger.Info("IsCompleted", "namespace", task.Namespace, "name", task.Name)
	if task.IsCompleted() || task.IsMarkedForDeletion() {
		return r.triggerDeletionFlow(ctx, operatorTask, task).ReconcileResult()
	}

	return r.reconcileTask(ctx, client.ObjectKeyFromObject(task), operatorTask).ReconcileResult()
}

func (r *Reconciler) markTaskRejected(
	ctx context.Context,
	task *v1alpha1.EtcdOperatorTask,
	derr *druiderr.DruidError,
	logger logr.Logger,
) error {
	task.Status.State = v1alpha1.TaskStateRejected
	if task.Status.InitiatedAt.IsZero() {
		task.Status.InitiatedAt = metav1.Time{Time: time.Now().UTC()}
	}
	task.Status.LastErrors = append(task.Status.LastErrors, v1alpha1.EtcdOperatorTaskLastError{
		Code:        derr.Code,
		Description: derr.Message,
		ObservedAt:  metav1.Time{Time: time.Now().UTC()},
	})
	if err := r.client.Status().Update(ctx, task); err != nil {
		logger.Error(err, "Failed to update task status")
		return err
	}
	return nil
}
