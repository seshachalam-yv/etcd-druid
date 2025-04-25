package operatortask

import (
	"fmt"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OperatorTaskFactory func(client.Client, logr.Logger, *v1alpha1.EtcdOperatorTask) OperatorTask
type OperatorTaskRegistry struct {
	tasks map[v1alpha1.EtcdOperatorTaskType]OperatorTaskFactory
}

func NewOperatorTaskRegistry() *OperatorTaskRegistry {
	return &OperatorTaskRegistry{tasks: make(map[v1alpha1.EtcdOperatorTaskType]OperatorTaskFactory)}
}

func (r *OperatorTaskRegistry) Register(taskType v1alpha1.EtcdOperatorTaskType, factory OperatorTaskFactory) {
	r.tasks[taskType] = factory
}

func (r *OperatorTaskRegistry) CreateOperatorTaskInstance(client client.Client, logger logr.Logger, task *v1alpha1.EtcdOperatorTask) (OperatorTask, error) {
	factory, ok := r.tasks[task.Spec.Type]
	if !ok {
		return nil, fmt.Errorf("no operator task registered for task type %s", task.Spec.Type)
	}
	return factory(client, logger, task), nil
}
