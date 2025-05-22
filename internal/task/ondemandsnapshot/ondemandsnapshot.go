package ondemandsnapshot

import (
	"context"
	"fmt"
	"time"

	"net/http"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/common"
	"github.com/gardener/etcd-druid/internal/task"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OnDemandSnapshotTask struct {
	client        client.Client
	logger        logr.Logger
	name          string
	etcdReference v1alpha1.EtcdReference
	config        v1alpha1.OnDemandSnapshotConfig
}

func New(k8sclient client.Client, logger logr.Logger, task *v1alpha1.EtcdOperatorTask) (task.Handler, error) {

	return &OnDemandSnapshotTask{
		client:        k8sclient,
		logger:        logger,
		name:          task.Name,
		etcdReference: *task.Spec.EtcdRef,
		config:        *task.Spec.Config.OnDemandSnapshot,
	}, nil
}

func (o *OnDemandSnapshotTask) EtcdReference() types.NamespacedName {
	return types.NamespacedName{
		Name:      o.etcdReference.Name,
		Namespace: o.etcdReference.Namespace,
	}
}

func (o *OnDemandSnapshotTask) Name() string {
	return o.name
}

func (o *OnDemandSnapshotTask) Logger() logr.Logger {
	return o.logger
}

// Conditions:
// 1) Backup should be enabled
// 2) TODO: Duplicate check via admission controller.
// 3) Add function to check quorum in helper
func (o *OnDemandSnapshotTask) Admit(ctx context.Context) *task.Result {
	var etcd v1alpha1.Etcd
	if err := o.client.Get(ctx, o.EtcdReference(), &etcd); err != nil {
		return &task.Result{
			Description: "Failed to get etcd object",
			Error:       err,
			Completed:   false,
		}
	}

	isBackupEnabled := etcd.IsBackupStoreEnabled()
	if !isBackupEnabled {
		return &task.Result{
			Description: "Backup is not enabled for etcd",
			Error:       fmt.Errorf("backup is not enabled for etcd"),
			Completed:   true,
		}
	}

	if err := CheckEtcdReadiness(ctx, &etcd); err != nil {
		return &task.Result{
			Description: "Etcd is not ready",
			Error:       fmt.Errorf("etcd is not ready"),
			Completed:   true,
		}
	}
	return &task.Result{
		Description: "Admit check passed",
		Completed:   true,
	}
}

func (o *OnDemandSnapshotTask) Run(ctx context.Context) *task.Result {
	etcd := &v1alpha1.Etcd{}
	if err := o.client.Get(ctx, o.EtcdReference(), etcd); err != nil {
		return &task.Result{
			Description: "Failed to get etcd object",
			Error:       err,
			Completed:   false,
		}
	}
	if err := CheckEtcdReadiness(ctx, etcd); err != nil {
		return &task.Result{
			Description: "Etcd is not ready",
			Error:       fmt.Errorf("etcd is not ready"),
			Completed:   true,
		}
	}

	url := fmt.Sprintf("http://%s.%s:%d/snapshot/%s", v1alpha1.GetClientServiceName(etcd.ObjectMeta), etcd.Namespace, ptr.Deref(etcd.Spec.Backup.Port, common.DefaultPortEtcdBackupRestore), o.config.Type)
	if ptr.Deref(o.config.IsFinal, false) {
		url += "?final=true"
	}
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return &task.Result{
			Description: "Failed to create HTTP request",
			Error:       err,
			Completed:   false,
		}
	}

	httpClient := &http.Client{Timeout: time.Second * time.Duration(*o.config.TimeoutSeconds)}
	resp, err := httpClient.Do(req)
	if err != nil {
		return &task.Result{
			Description: "Failed to execute HTTP request",
			Error:       err,
			Completed:   false,
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &task.Result{
			Description: "Failed to create snapshot",
			Error:       fmt.Errorf("failed to create snapshot, status code: %d", resp.StatusCode),
			Completed:   true,
		}
	}
	return &task.Result{
		Description: "Snapshot created successfully",
		Completed:   true,
	}

}
func (o *OnDemandSnapshotTask) Cleanup(ctx context.Context) *task.Result {
	return &task.Result{
		Description: "Cleanup completed",
		Completed:   true,
	}
}

func CheckEtcdReadiness(ctx context.Context, etcd *v1alpha1.Etcd) error {
	for _, condition := range etcd.Status.Conditions {
		if condition.Type == v1alpha1.ConditionTypeReady {
			return nil
		}
	}
	return fmt.Errorf("etcd is not ready")
}
