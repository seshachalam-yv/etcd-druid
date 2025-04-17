package tasks

import (
	"context"

	"github.com/go-logr/logr"
)

// TaskContext carries context and logger for task reconciliation.
type TaskContext struct {
	context.Context
	Logger logr.Logger
	// RunID is unique ID identifying a single reconciliation run.
	RunID string
}

func NewTaskContext(ctx context.Context, logger logr.Logger, runID string) TaskContext {
	return TaskContext{
		Context: ctx,
		Logger:  logger,
		RunID:   runID,
	}
}

func (tc *TaskContext) SetLogger(logger logr.Logger) {
	tc.Logger = logger
}
