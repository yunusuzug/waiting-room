package waitingroom

import "errors"

// Common errors returned by the waiting-room library.
var (
	// ErrTaskNotFound indicates the requested task does not exist.
	ErrTaskNotFound = errors.New("task not found")

	// ErrTaskAlreadyExists indicates a task with the given ID already exists.
	ErrTaskAlreadyExists = errors.New("task already exists")

	// ErrInvalidTaskStatus indicates the task is in an invalid status for the requested operation.
	ErrInvalidTaskStatus = errors.New("invalid task status for operation")

	// ErrHandlerNotFound indicates no handler is registered for the task type.
	ErrHandlerNotFound = errors.New("task handler not found")

	// ErrHandlerAlreadyRegistered indicates a handler is already registered for the task type.
	ErrHandlerAlreadyRegistered = errors.New("task handler already registered")

	// ErrHandlerNotImplemented indicates the handler function is not implemented.
	ErrHandlerNotImplemented = errors.New("task handler not implemented")

	// ErrInvalidPayload indicates the task payload is invalid.
	ErrInvalidPayload = errors.New("invalid task payload")

	// ErrLockAcquisitionFailed indicates the distributed lock could not be acquired.
	ErrLockAcquisitionFailed = errors.New("failed to acquire distributed lock")

	// ErrDatabaseConnection indicates a database connection error.
	ErrDatabaseConnection = errors.New("database connection error")

	// ErrConfigInvalid indicates the configuration is invalid.
	ErrConfigInvalid = errors.New("invalid configuration")

	// ErrWorkersAlreadyRunning indicates the workers are already running.
	ErrWorkersAlreadyRunning = errors.New("workers are already running")

	// ErrWorkersNotRunning indicates the workers are not running.
	ErrWorkersNotRunning = errors.New("workers are not running")

	// ErrCannotApprove indicates the task cannot be approved in its current state.
	ErrCannotApprove = errors.New("task cannot be approved")

	// ErrCannotCancel indicates the task cannot be cancelled in its current state.
	ErrCannotCancel = errors.New("task cannot be cancelled")

	// ErrCannotRetry indicates the task cannot be retried in its current state.
	ErrCannotRetry = errors.New("task cannot be retried")
)
