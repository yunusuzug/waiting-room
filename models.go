package waitingroom

import (
	"encoding/json"
	"time"
)

// TaskStatus represents the current state of a task.
type TaskStatus string

const (
	// TaskStatusPending indicates the task is awaiting approval.
	TaskStatusPending TaskStatus = "pending"

	// TaskStatusApproved indicates the task has been approved and is ready to run.
	TaskStatusApproved TaskStatus = "approved"

	// TaskStatusRejected indicates the task has been rejected.
	TaskStatusRejected TaskStatus = "rejected"

	// TaskStatusScheduled indicates the task is approved but waiting for its scheduled time.
	TaskStatusScheduled TaskStatus = "scheduled"

	// TaskStatusRunning indicates the task is currently executing.
	TaskStatusRunning TaskStatus = "running"

	// TaskStatusCompleted indicates the task has finished successfully.
	TaskStatusCompleted TaskStatus = "completed"

	// TaskStatusFailed indicates the task has failed and needs manual retry.
	TaskStatusFailed TaskStatus = "failed"

	// TaskStatusCancelled indicates the task has been cancelled.
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task represents a unit of work managed by the waiting-room library.
type Task struct {
	// ID is the unique identifier for the task (UUID).
	ID string `json:"id"`

	// Type identifies the task handler that will execute this task.
	Type string `json:"type"`

	// Status represents the current state of the task.
	Status TaskStatus `json:"status"`

	// Payload contains task-specific data as JSON.
	Payload json.RawMessage `json:"payload"`

	// Metadata contains custom user-defined data as JSON.
	// Applications can use this to store additional information about the task.
	Metadata json.RawMessage `json:"metadata,omitempty"`

	// RequiresApproval indicates whether this task needs approval before execution.
	RequiresApproval bool `json:"requiresApproval"`

	// ApprovedBy identifies who approved the task (null for auto-approved tasks).
	ApprovedBy *string `json:"approvedBy,omitempty"`

	// ApprovedAt is the time when the task was approved.
	ApprovedAt *time.Time `json:"approvedAt,omitempty"`

	// ScheduledAt is when the task should be executed (nil means run immediately when approved).
	ScheduledAt *time.Time `json:"scheduledAt,omitempty"`

	// CreatedAt is when the task was created.
	CreatedAt time.Time `json:"createdAt"`

	// UpdatedAt is when the task was last modified.
	UpdatedAt time.Time `json:"updatedAt"`

	// RunAt is when the task was actually executed (nil if not yet run).
	RunAt *time.Time `json:"runAt,omitempty"`

	// RetryCount tracks how many times this task has been retried.
	RetryCount int `json:"retryCount"`
}

// IsTerminal returns true if the task is in a terminal state (completed, failed, or cancelled).
func (t *Task) IsTerminal() bool {
	switch t.Status {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
		return true
	default:
		return false
	}
}

// CanApprove returns true if the task can be approved (must be in pending status).
func (t *Task) CanApprove() bool {
	return t.Status == TaskStatusPending
}

// CanCancel returns true if the task can be cancelled.
func (t *Task) CanCancel() bool {
	switch t.Status {
	case TaskStatusPending, TaskStatusApproved, TaskStatusScheduled:
		return true
	default:
		return false
	}
}

// CanRetry returns true if the task can be retried (must be in failed status).
func (t *Task) CanRetry() bool {
	return t.Status == TaskStatusFailed
}

// CreateOptions contains options for creating a new task.
type CreateOptions struct {
	// ScheduledAt specifies when the task should be executed.
	// If nil, the task runs immediately after approval.
	ScheduledAt *time.Time

	// Metadata contains custom user-defined data to store with the task.
	Metadata json.RawMessage

	// ApprovalFunc is an optional per-task approval function.
	// If provided, this function is used instead of the TaskManager's
	// default approval function to determine if this specific task
	// requires approval. If nil, the default approval function is used.
	ApprovalFunc ApprovalDecisionFunc
}

// ApproveOptions contains options for approving a task.
type ApproveOptions struct {
	// ApprovedBy identifies who approved the task (e.g., email or user ID).
	// Required.
	ApprovedBy string

	// ScheduledAt specifies when the task should be executed.
	// If nil, the task runs immediately after approval.
	// This overrides any schedule set during task creation.
	ScheduledAt *time.Time
}

// ListFilter contains criteria for listing tasks.
type ListFilter struct {
	// Status filters tasks by status.
	// If empty, returns tasks with any status.
	Status TaskStatus

	// Type filters tasks by task type.
	// If empty, returns tasks of any type.
	Type string

	// Limit restricts the number of results.
	// Default is 100.
	Limit int

	// Offset is for pagination.
	Offset int
}

// setDefaults applies default values for unspecified filter options.
func (f *ListFilter) setDefaults() {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
}
