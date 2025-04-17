/*
SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
and Gardener contributors

SPDX-License-Identifier: Apache-2.0
*/

package etcdoperatortask

import (
	"context"
	"errors"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/tasks"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "etcdoperatortask-controller"
	FinalizerName  = "etcd-druid.gardener.cloud/etcd-operator-task"
)

type Reconciler struct {
	client   client.Client
	recorder record.EventRecorder
	logger   logr.Logger
	config   *Config
	registry *tasks.TaskExecutorRegistry
}

type reconcileFn func(ctx tasks.TaskContext, taskObjKey client.ObjectKey, executor tasks.TaskExecutor) ctrlutils.ReconcileStepResult

func New(mgr ctrl.Manager, cfg *Config) *Reconciler {
	registry := tasks.NewTaskExecutorRegistry()
	registry.Register(v1alpha1.EtcdOperatorTaskTypeOnDemandSnapshot, tasks.NewOnDemandSnapshot(mgr.GetClient()))
	// Register more executors as needed

	return &Reconciler{
		client:   mgr.GetClient(),
		recorder: mgr.GetEventRecorderFor(ControllerName),
		logger:   ctrl.Log.WithName(ControllerName),
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
	runID := string(controller.ReconcileIDFromContext(ctx))
	taskCtx := tasks.NewTaskContext(ctx, r.logger.WithValues("runId", runID), runID)

	task := &v1alpha1.EtcdOperatorTask{}
	taskCtx.Logger.Info("Fetching task", "namespace", req.Namespace, "name", req.Name)
	if err := r.client.Get(taskCtx.Context, req.NamespacedName, task); err != nil {
		if client.IgnoreNotFound(err) == nil {
			taskCtx.Logger.Info("Task not found, ignoring")
			return ctrlutils.DoNotRequeue().ReconcileResult()
		}
		taskCtx.Logger.Error(err, "Failed to fetch task")
		return ctrlutils.ReconcileWithError(err).ReconcileResult()
	}

	taskExecutor, err := r.registry.Get(task.Spec.Type)
	if err != nil {
		r.logger.Error(err, "Failed to get task executor")
		return ctrl.Result{}, err
	}

	if result := r.reconcileEtcdOperatorTaskDeletion(taskCtx, taskExecutor, task); ctrlutils.ShortCircuitReconcileFlow(result) {
		return result.ReconcileResult()
	}

	if task.IsCompleted() {
		if result := r.GC(taskCtx, task); ctrlutils.ShortCircuitReconcileFlow(result) {
			return result.ReconcileResult()
		}
	}

	return r.reconcileTask(taskCtx, client.ObjectKeyFromObject(task), taskExecutor).ReconcileResult()
}

// recordReconcileStartOperation records the start of a reconcile operation for the given EtcdOperatorTask.
func (r *Reconciler) recordReconcileStartOperation(ctx context.Context, task *v1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult {
	r.logger.Info("Recording start of reconcile operation", "namespace", task.Namespace, "name", task.Name)
	// Optionally, update status or add an event here.
	if r.recorder != nil {
		r.recorder.Eventf(task, "Normal", "ReconcileStart", "Started reconcile operation for task %s/%s", task.Namespace, task.Name)
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) GC(taskCtx tasks.TaskContext, task *v1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult {
	if task.Status.InitiatedAt.IsZero() || task.Spec.TTLSecondsAfterFinished == nil {
		return ctrlutils.ReconcileWithError(errors.New("TTL not set"))
	}

	elapsed := time.Since(task.Status.InitiatedAt.Time)
	if elapsed <= time.Duration(*task.Spec.TTLSecondsAfterFinished)*time.Second {
		remaining := time.Duration(*task.Spec.TTLSecondsAfterFinished)*time.Second - elapsed
		if remaining > time.Minute {
			remaining = time.Minute
		}
		return ctrlutils.ReconcileAfter(remaining, "TTL not expired")
	}

	taskCtx.Logger.Info("TTL expired, ensuring finalizer for deletion")
	if !controllerutil.ContainsFinalizer(task, FinalizerName) {
		controllerutil.AddFinalizer(task, FinalizerName)
		if err := r.client.Update(taskCtx.Context, task); err != nil {
			taskCtx.Logger.Error(err, "Failed to add finalizer before deletion")
			return ctrlutils.ReconcileWithError(err)
		}
	}

	taskCtx.Logger.Info("Deleting task")
	if err := r.client.Delete(taskCtx.Context, task); err != nil {
		taskCtx.Logger.Error(err, "Failed to delete task")
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.DoNotRequeue()
}
