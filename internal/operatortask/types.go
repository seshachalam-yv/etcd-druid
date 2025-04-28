package operatortask

import (
	"context"
	"time"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ERR_INVALID_CONFIG druidv1alpha1.ErrorCode = "ERR_INVALID_CONFIG"

type TaskResult struct {
	Description  string
	Requeue      bool
	RequeueAfter time.Duration
	Completed    bool
	Error        error
}

type OperatorTask interface {
	EtcdReference() types.NamespacedName
	Name() string
	Type() druidv1alpha1.EtcdOperatorTaskType
	Admit(ctx context.Context) *TaskResult
	Run(ctx context.Context) *TaskResult
	Cleanup(ctx context.Context) *TaskResult
	Logger() logr.Logger
}

type OperatorTaskFactory func(client.Client, logr.Logger, *druidv1alpha1.EtcdOperatorTask) (OperatorTask, error)
