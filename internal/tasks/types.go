package tasks

type OperationStatus struct {
	State         string
	LastOperation string
	LastError     string
	Reason        string
	Message       string
}

const (
	OperationCheckPreconditions = "CheckPreconditions"
	OperationExecute            = "Execute"
	OperationCleanup            = "Cleanup"
)
