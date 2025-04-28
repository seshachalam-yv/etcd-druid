package ondemandsnapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/common"
	"github.com/gardener/etcd-druid/internal/operatortask"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	druiderr "github.com/gardener/etcd-druid/internal/errors"
)

const ERR_PRECONDITION_CHECK_ON_DEMAND_SNAPSHOT druidv1alpha1.ErrorCode = "ERR_PRECONDITION_CHECK_ON_DEMAND_SNAPSHOT"

// parseConfig parses the raw config map into the strongly-typed OnDemandSnapshotConfig struct.
func parseConfig(raw map[string]interface{}) (*Config, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// OnDemandSnapshot implements the OperatorTask interface for on-demand snapshots.
type OnDemandSnapshot struct {
	client        client.Client
	logger        logr.Logger
	name          string
	etcdReference types.NamespacedName
	config        *Config
}

const operationAdmit = "Admit"
const operationNew = "New"

func New(k8sclient client.Client, logger logr.Logger, task *v1alpha1.EtcdOperatorTask) (operatortask.OperatorTask, error) {
	// task.Spec.Config is a string (JSON) or empty. Accept string only.
	var cfg *Config
	if task.Spec.Config == "" {
		return nil, druiderr.New(operatortask.ERR_INVALID_CONFIG, operationNew, "config for OnDemandSnapshot is empty")
	} else {
		var m map[string]interface{}
		err := json.Unmarshal([]byte(task.Spec.Config), &m)
		if err != nil {
			return nil, druiderr.WrapError(err, operatortask.ERR_INVALID_CONFIG, operationNew, "failed to parse config for OnDemandSnapshot")
		}
		cfg, err = parseConfig(m)
		if err != nil {
			return nil, druiderr.WrapError(err, operatortask.ERR_INVALID_CONFIG, operationNew, "failed to parse config for OnDemandSnapshot")
		}
	}
	return &OnDemandSnapshot{
		client:        k8sclient,
		logger:        logger,
		name:          task.Name,
		etcdReference: types.NamespacedName(*task.Spec.EtcdRef),
		config:        cfg,
	}, nil
}

func (o *OnDemandSnapshot) EtcdReference() types.NamespacedName {
	return o.etcdReference
}

func (o *OnDemandSnapshot) Name() string {
	return o.name
}

func (o *OnDemandSnapshot) Type() v1alpha1.EtcdOperatorTaskType {
	return v1alpha1.EtcdOperatorTaskTypeOnDemandSnapshot
}

func (o *OnDemandSnapshot) Run(ctx context.Context) *operatortask.TaskResult {
	// Implement the snapshot logic
	etcd := &v1alpha1.Etcd{}
	if err := o.client.Get(ctx, o.etcdReference, etcd); err != nil {
		return &operatortask.TaskResult{Description: "Failed to get Etcd resource", Error: err}
	}

	snapType := "full"
	if o.config.SnapshotType != nil && *o.config.SnapshotType != "" {
		snapType = *o.config.SnapshotType
	}
	timeout := 30 * time.Second
	if o.config.TimeoutSeconds != nil && *o.config.TimeoutSeconds > 0 {
		timeout = time.Duration(*o.config.TimeoutSeconds) * time.Second
	}

	o.logger.Info("Snapshot config", "snapshotType", snapType, "timeout", timeout)

	url := fmt.Sprintf("http://%s.%s:%d/snapshot/full?final=true", v1alpha1.GetClientServiceName(etcd.ObjectMeta), etcd.Namespace, ptr.Deref(etcd.Spec.Backup.Port, common.DefaultPortEtcdBackupRestore))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return &operatortask.TaskResult{
			Description: fmt.Sprintf("Failed to create HTTP request for %s", url),
			Error:       err,
		}
	}
	o.logger.Info("Triggering snapshot", "url", url)

	httpClient := &http.Client{Timeout: timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return &operatortask.TaskResult{Description: "Snapshot HTTP request failed", Error: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &operatortask.TaskResult{Description: fmt.Sprintf("Snapshot failed, status: %s", resp.Status), Completed: false}
	}
	return &operatortask.TaskResult{Description: "Triggered snapshot on pod", Completed: true}
}

func (o *OnDemandSnapshot) Admit(ctx context.Context) *operatortask.TaskResult {
	// Implement the admission logic (e.g., check if Etcd is ready)
	etcd := &v1alpha1.Etcd{}
	if err := o.client.Get(ctx, o.etcdReference, etcd); err != nil {
		return &operatortask.TaskResult{Description: "Failed to get Etcd resource", Error: err}
	}
	if etcd.Status.ReadyReplicas < 1 {
		return &operatortask.TaskResult{
			Description: "No ready replicas for Etcd",
			Error: druiderr.WrapError(nil,
				ERR_PRECONDITION_CHECK_ON_DEMAND_SNAPSHOT,
				operationAdmit,
				fmt.Sprintf("No ready replicas for Etcd: %v", o.etcdReference)),
			Completed: true,
		}
	}
	return &operatortask.TaskResult{Description: "Admission successful", Completed: true}
}

func (o *OnDemandSnapshot) Cleanup(ctx context.Context) *operatortask.TaskResult {
	// No-op for snapshot tasks
	return &operatortask.TaskResult{Description: "Cleanup done", Completed: true}
}

func (o *OnDemandSnapshot) Logger() logr.Logger {
	return o.logger
}
