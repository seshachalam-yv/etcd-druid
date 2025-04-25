package ondemandsnapshot

import (
	"context"
	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/operatortask"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OnDemandSnapshot implements the OperatorTask interface for on-demand snapshots.
type OnDemandSnapshot struct {
	client client.Client
}

func New(client client.Client) *OnDemandSnapshot {
	return &OnDemandSnapshot{client: client}
}

func (o *OnDemandSnapshot) EtcdReference() string {
	// TODO: implement actual etcd reference logic
	return ""
}

func (o *OnDemandSnapshot) Name() string {
	return "OnDemandSnapshot"
}

func (o *OnDemandSnapshot) Type() v1alpha1.EtcdOperatorTaskType {
	return v1alpha1.EtcdOperatorTaskTypeOnDemandSnapshot
}

func (o *OnDemandSnapshot) Run(ctx context.Context) *operatortask.TaskResult {
	// TODO: implement the snapshot logic
	return &operatortask.TaskResult{Description: "Snapshot run", Completed: true}
}

func (o *OnDemandSnapshot) Cleanup(ctx context.Context) *operatortask.TaskResult {
	// TODO: implement cleanup logic
	return &operatortask.TaskResult{Description: "Cleanup done", Completed: true}
}
