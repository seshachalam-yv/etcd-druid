package tasks

import (
	"fmt"
	"net/http"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/common"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OnDemandSnapshot struct {
	client client.Client
	logger logr.Logger
}

func NewOnDemandSnapshot(client client.Client) TaskExecutor {
	return &OnDemandSnapshot{
		client: client,
	}
}

func (ods *OnDemandSnapshot) CheckPreconditions(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) error {
	ods.logger = ctx.Logger.WithValues("task", task.Name, "type", task.Spec.Type, "operation", OperationCheckPreconditions)
	ods.logger.Info("Checking preconditions for task")
	etcd := &v1alpha1.Etcd{}
	if err := ods.client.Get(ctx.Context, client.ObjectKey{Namespace: task.Spec.EtcdRef.Namespace, Name: task.Spec.EtcdRef.Name}, etcd); err != nil {
		return err
	}
	if !etcd.IsBackupStoreEnabled() {
		return fmt.Errorf("backup store is not enabled for etcd %s", etcd.Name)
	}

	// Should we need to check if already full snapshot for on-demand delta snapshot trigger

	if !*etcd.Status.Ready {
		return fmt.Errorf("etcd is not ready")
	}
	return nil
}

func (ods *OnDemandSnapshot) Execute(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) (completed bool, opStatus *v1alpha1.EtcdOperatorLastOperation, err error) {
	ods.logger = ctx.Logger.WithValues("task", task.Name, "type", task.Spec.Type, "operation", OperationExecute)
	ods.logger.Info("Executing task")
	etcd := &v1alpha1.Etcd{}
	if err := ods.client.Get(ctx.Context, client.ObjectKey{Namespace: task.Spec.EtcdRef.Namespace, Name: task.Spec.EtcdRef.Name}, etcd); err != nil {
		return false, nil, err
	}
	if !*etcd.Status.Ready {
		return false, &v1alpha1.EtcdOperatorLastOperation{
			Name:               "Snapshot",
			State:              v1alpha1.OperationStateFailed,
			Reason:             "Etcd is not ready",
			LastTransitionTime: metav1.Now(),
		}, nil
	}
	// TODO: check why final parameter is being used
	url := fmt.Sprintf("http://%s.%s:%d/snapshot/full?final=true", v1alpha1.GetClientServiceName(etcd.ObjectMeta), etcd.Namespace, ptr.Deref(etcd.Spec.Backup.Port, common.DefaultPortEtcdBackupRestore))

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, nil, err
	}

	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, &v1alpha1.EtcdOperatorLastOperation{
			Name:               "Snapshot",
			State:              v1alpha1.OperationStateFailed,
			Reason:             fmt.Sprintf("error occurred while initiating ETCD snapshot: %s", err),
			LastTransitionTime: metav1.Now(),
		}, nil
	}

	ods.logger.Info("ETCD snapshot initiated", "status", resp.Status, "statusCode", resp.StatusCode)
	if resp != nil && resp.StatusCode != http.StatusOK {
		return false, &v1alpha1.EtcdOperatorLastOperation{
			Name:               "Snapshot",
			State:              v1alpha1.OperationStateFailed,
			Reason:             fmt.Sprintf("error occurred while initiating ETCD snapshot: %s", resp.Status),
			LastTransitionTime: metav1.Now(),
		}, nil
	}

	return true, &v1alpha1.EtcdOperatorLastOperation{
		State: v1alpha1.OperationStateCompleted,
	}, nil
}

func (ods *OnDemandSnapshot) Cleanup(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) error {
	ods.logger = ctx.Logger.WithValues("task", task.Name, "type", task.Spec.Type, "operation", OperationCleanup)
	ods.logger.Info("Cleaning up task")
	return nil
}
