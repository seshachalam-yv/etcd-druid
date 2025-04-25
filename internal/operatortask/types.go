package operatortask

import (
	"context"
	"time"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/go-logr/logr"
)

type TaskResult struct {
	Description  string
	Requeue      bool
	RequeueAfter time.Duration
	Completed    bool
	Error        error
}

type OperatorTask interface {
	EtcdReference() v1alpha1.EtcdReference
	Name() string
	Type() v1alpha1.EtcdOperatorTaskType
	Admit(ctx context.Context) *TaskResult
	Run(ctx context.Context) *TaskResult
	Cleanup(ctx context.Context) *TaskResult
	Logger() logr.Logger
}
