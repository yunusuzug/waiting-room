// Package waitingroom provides a task management library with approval workflows,
// scheduling, and distributed execution support using PostgreSQL.
//
// The main entry point is the TaskManager, which provides methods to create,
// manage, and execute tasks. Applications implement the TaskHandler interface
// to define task types and their execution logic.
//
// Basic usage:
//
//	config := waitingroom.Config{
//	    Database: waitingroom.DatabaseConfig{
//	        Host:     "localhost",
//	        Port:     "5432",
//	        Name:     "mydb",
//	        User:     "user",
//	        Password: "pass",
//	    },
//	    ApplicationID: "my-app",
//	}
//
//	tm, err := waitingroom.New(config, myApprovalFunc)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer tm.Close()
//
//	tm.RegisterHandler(&MyTaskHandler{})
//	tm.StartWorkers(ctx)
package waitingroom

import (
	"context"
)

// TaskHandler defines the interface that applications must implement
// for each task type they want to register with the waiting-room library.
type TaskHandler interface {
	// Type returns a unique identifier for this task type.
	// This identifier is used to match tasks to their handlers.
	// Example: "send_email", "process_payment", "generate_report"
	Type() string

	// Execute runs the task logic with the provided task.
	// The context can be used for cancellation and timeouts.
	// The task contains all task data including ID, Payload, Metadata, etc.
	// Returns an error if the task fails.
	Execute(ctx context.Context, task *Task) error
}

// TaskHandlerFunc is a convenience type for creating simple task handlers
type TaskHandlerFunc struct {
	taskType string
	execFunc func(ctx context.Context, task *Task) error
}

// Type returns the task type identifier.
func (f *TaskHandlerFunc) Type() string {
	return f.taskType
}

// Execute runs the task logic.
func (f *TaskHandlerFunc) Execute(ctx context.Context, task *Task) error {
	if f.execFunc == nil {
		return ErrHandlerNotImplemented
	}
	return f.execFunc(ctx, task)
}

// NewTaskHandler creates a new task handler from a function.
func NewTaskHandler(
	taskType string,
	execFunc func(ctx context.Context, task *Task) error,
) TaskHandler {
	return &TaskHandlerFunc{
		taskType: taskType,
		execFunc: execFunc,
	}
}
