package etcdoperatortask

import (
	"context"
	"fmt"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/task"
	"github.com/gardener/etcd-druid/internal/task/ondemandsnapshot"

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

type reconcileFn func(ctx context.Context, taskObjKey client.ObjectKey, taskHandler task.Handler) ctrlutils.ReconcileStepResult

type Reconciler struct {
	client   client.Client
	recorder record.EventRecorder
	logger   logr.Logger
	config   *Config
}

func New(mgr manager.Manager, cfg *Config) *Reconciler {
	logger := log.Log.WithName(ControllerName)

	return &Reconciler{
		client:   mgr.GetClient(),
		recorder: mgr.GetEventRecorderFor(ControllerName),
		logger:   logger,
		config:   cfg,
	}
}


func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    // The below block checks for the OperatorTask.
    task := &v1alpha1.EtcdOperatorTask{}
    if err := r.client.Get(ctx, req.NamespacedName, task); err != nil {
        if client.IgnoreNotFound(err) != nil {
            return reconcile.Result{}, err
        }
        return reconcile.Result{}, nil
    }

    logger := r.logger.WithValues("runId", string(controller.ReconcileIDFromContext(ctx)))
    // create a instance of TaskHandler
    taskHandlerInstance, err := r.createTaskHandlerInstance(task)
	// TODO: Check if error handling is needed here.
	if err != nil {
		logger.Error(err, "failed to create task handler instance")
		return reconcile.Result{}, err
	}

    
    // Triggers the deletion flow in case the task is in a completed state or if it has been marked for deletion.
    if task.IsCompleted() || task.IsMarkedForDeletion() {
        return r.triggerDeletionFlow(ctx, taskHandlerInstance, task).ReconcileResult()
    }
    // triggers the task execution flow.
    return r.reconcileTask(ctx, client.ObjectKeyFromObject(task), taskHandlerInstance).ReconcileResult()
}


func (r *Reconciler) createTaskHandlerInstance(task *v1alpha1.EtcdOperatorTask) (task.Handler, error) {
	if task.Spec.Config.OnDemandSnapshot != nil {
		return ondemandsnapshot.New(r.client, r.logger, task)
	}
	return nil, fmt.Errorf("task type not supported")
   
}
