package tasks

import (
	"fmt"

	"github.com/gardener/etcd-druid/api/core/v1alpha1"
)

type OperatorTask interface {
	// CheckPreconditions(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) error
	// Execute(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) (completed bool, opStatus *v1alpha1.EtcdOperatorLastOperation)
	// Cleanup(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) error
	CheckPreconditions(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult
    Execute(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) (bool, ctrlutils.ReconcileStepResult, error)
    Cleanup(ctx TaskContext, task *v1alpha1.EtcdOperatorTask) ctrlutils.ReconcileStepResult
}

// type TaskExecutorRegistry struct {
// 	registry map[v1alpha1.EtcdOperatorTaskType]EtcdOperatorTask
// }

// func NewTaskExecutorRegistry() *TaskExecutorRegistry {
// 	return &TaskExecutorRegistry{
// 		registry: make(map[v1alpha1.EtcdOperatorTaskType]EtcdOperatorTask),
// 	}
// }

// func (r *TaskExecutorRegistry) Register(key v1alpha1.EtcdOperatorTaskType, executor EtcdOperatorTask) {
// 	r.registry[key] = executor
// }

// func (r *TaskExecutorRegistry) Get(key v1alpha1.EtcdOperatorTaskType) (EtcdOperatorTask, error) {
// 	executor, found := r.registry[key]
// 	if !found {
// 		return nil, fmt.Errorf("no executor for type %s", key)
// 	}
// 	return executor, nil
// }
