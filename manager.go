package waitingroom

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// TaskManager is the main API for the waiting-room library.
// Applications use this to create, manage, and execute tasks.
//
// Create a new TaskManager using New(), register handlers with RegisterHandler(),
// and start background workers with StartWorkers().
type TaskManager struct {
	config       Config
	room         *room
	registry     *handlerRegistry
	scheduler    *scheduler
	approvalFunc ApprovalDecisionFunc
	db           *sql.DB
}

// New creates a new TaskManager with the given configuration and approval function.
// This initializes the database connection and runs migrations automatically.
//
// The approvalFunc determines whether tasks require approval before execution.
// If nil, DefaultApprovalDecision is used (auto-approves all tasks).
//
// Example:
//
//	config := waitingroom.Config{
//	    Database: waitingroom.DatabaseConfig{
//	        Host:     "localhost",
//	        Port:     "5432",
//	        Name:     "mydb",
//	        User:     "user",
//	        Password: "pass",
//	    },
//	}
//
//	tm, err := waitingroom.New(config, myApprovalFunc)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer tm.Close()
func New(config Config, approvalFunc ApprovalDecisionFunc) (*TaskManager, error) {
	config.setDefaults()

	// Get database connection URL
	databaseURL, err := config.Database.ConnectionString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfigInvalid, err)
	}

	if approvalFunc == nil {
		approvalFunc = DefaultApprovalDecision
	}

	// Open database connection
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, ErrDatabaseConnection
	}

	r := newRoom(db)

	// Run migrations unless explicitly disabled
	if !config.SkipMigration {
		if err := migrate(ctx, r); err != nil {
			db.Close()
			return nil, err
		}
	}

	tm := &TaskManager{
		config:       config,
		room:         r,
		registry:     newHandlerRegistry(),
		approvalFunc: approvalFunc,
		db:           db,
	}

	return tm, nil
}

// RegisterHandler registers a task handler for a specific task type.
// Returns ErrHandlerAlreadyRegistered if a handler for this type is already registered.
//
// Handlers must be registered before calling StartWorkers().
func (tm *TaskManager) RegisterHandler(handler TaskHandler) error {
	return tm.registry.register(handler)
}

// UnregisterHandler removes a task handler.
// This only affects future task creation; already queued tasks will still execute.
func (tm *TaskManager) UnregisterHandler(taskType string) {
	tm.registry.unregister(taskType)
}

// HandlerTypes returns a list of all registered task handler types.
func (tm *TaskManager) HandlerTypes() []string {
	return tm.registry.types()
}

// HandlerCount returns the number of registered handlers.
func (tm *TaskManager) HandlerCount() int {
	return tm.registry.count()
}

// CreateTask creates a new task with the given type and payload.
// The approval decision function determines if the task requires approval.
// If auto-approved and no schedule is set, the task runs immediately.
//
// The taskType must have a registered handler, or ErrHandlerNotFound is returned.
// The payload is passed to the handler's Execute method when the task runs.
// Metadata can be used to store custom data with the task.
//
// Returns the created task with its assigned ID and initial status.
func (tm *TaskManager) CreateTask(ctx context.Context, taskType string, payload json.RawMessage, opts CreateOptions) (*Task, error) {
	// Validate that a handler exists for this task type
	handler, err := tm.registry.get(taskType)
	if err != nil {
		return nil, err
	}

	// Determine if approval is required
	approvalDecision := tm.approvalFunc(ctx, taskType, payload)

	now := time.Now().UTC()
	task := &Task{
		ID:               uuid.New().String(),
		Type:             taskType,
		Payload:          payload,
		Metadata:         opts.Metadata,
		RequiresApproval: approvalDecision.RequiresApproval,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Set status based on approval decision
	if approvalDecision.RequiresApproval {
		task.Status = TaskStatusPending
	} else {
		// Auto-approved - check if scheduling is required
		if opts.ScheduledAt != nil && opts.ScheduledAt.After(now) {
			task.Status = TaskStatusScheduled
			task.ScheduledAt = opts.ScheduledAt
		} else {
			task.Status = TaskStatusApproved
			task.ApprovedBy = strPtr("system")
			task.ApprovedAt = &now
		}
	}

	// Save the task
	if err := tm.room.Create(ctx, task); err != nil {
		return nil, err
	}

	// If auto-approved and not scheduled, execute immediately
	if !approvalDecision.RequiresApproval && task.Status == TaskStatusApproved {
		// Execute synchronously
		task.Status = TaskStatusRunning
		task.RunAt = timePtr(time.Now().UTC())
		tm.room.Update(ctx, task)

		execErr := handler.Execute(ctx, task)

		if execErr != nil {
			log.Printf("Task %s failed: %v", task.ID, execErr)
			task.Status = TaskStatusFailed
		} else {
			task.Status = TaskStatusCompleted
		}

		task.UpdatedAt = time.Now().UTC()
		tm.room.Update(ctx, task)
	}

	return task, nil
}

// Get retrieves a task by its ID.
// Returns ErrTaskNotFound if the task does not exist.
func (tm *TaskManager) Get(ctx context.Context, taskID string) (*Task, error) {
	return tm.room.Get(ctx, taskID)
}

// List retrieves tasks matching the filter criteria.
// If filter.Status is empty, tasks of any status are returned.
// Results are ordered by created_at descending (newest first).
func (tm *TaskManager) List(ctx context.Context, filter ListFilter) ([]*Task, error) {
	return tm.room.List(ctx, filter)
}

// Approve approves a pending task.
// If the task has a scheduled time in the future, it transitions to "scheduled" status.
// If no schedule is set or the time has passed, it transitions to "approved" status
// and will be picked up by the scheduler for execution.
//
// Returns ErrCannotApprove if the task is not in pending status.
// Returns ErrTaskNotFound if the task does not exist.
func (tm *TaskManager) Approve(ctx context.Context, taskID string, approvedBy string) (*Task, error) {
	task, err := tm.room.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if !task.CanApprove() {
		return nil, ErrCannotApprove
	}

	now := time.Now().UTC()
	task.ApprovedBy = &approvedBy
	task.ApprovedAt = &now
	task.UpdatedAt = now

	// Determine next status based on scheduled time
	if task.ScheduledAt != nil && task.ScheduledAt.After(now) {
		task.Status = TaskStatusScheduled
	} else {
		task.Status = TaskStatusApproved
	}

	if err := tm.room.Update(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

// Cancel cancels a task that is not yet running.
// Tasks that are already running, completed, failed, or cancelled cannot be cancelled.
//
// Returns ErrCannotCancel if the task cannot be cancelled in its current state.
// Returns ErrTaskNotFound if the task does not exist.
func (tm *TaskManager) Cancel(ctx context.Context, taskID string) (*Task, error) {
	task, err := tm.room.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if !task.CanCancel() {
		return nil, ErrCannotCancel
	}

	task.Status = TaskStatusCancelled
	task.UpdatedAt = time.Now().UTC()

	if err := tm.room.Update(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

// Retry retries a failed task.
// The task status is reset to "approved" and it will be picked up by the scheduler.
// The retry count is incremented.
//
// Returns ErrCannotRetry if the task is not in failed status.
// Returns ErrTaskNotFound if the task does not exist.
func (tm *TaskManager) Retry(ctx context.Context, taskID string) (*Task, error) {
	task, err := tm.room.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if !task.CanRetry() {
		return nil, ErrCannotRetry
	}

	now := time.Now().UTC()
	task.Status = TaskStatusApproved
	task.RetryCount++
	task.UpdatedAt = now
	task.RunAt = nil

	if err := tm.room.Update(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

// StartWorkers starts the background scheduler for processing scheduled tasks.
// This should be called after all handlers are registered.
//
// The scheduler runs in the background and polls for tasks to execute.
// It uses distributed locking to ensure only one instance processes tasks at a time.
//
// Returns ErrWorkersAlreadyRunning if workers are already running.
func (tm *TaskManager) StartWorkers(ctx context.Context) error {
	if tm.scheduler != nil {
		return ErrWorkersAlreadyRunning
	}

	// instanceID is auto-generated internally for each scheduler instance
	tm.scheduler = newScheduler(
		tm.room,
		tm.registry,
		tm.config.WorkerInterval,
		tm.config.LockTimeout,
		tm.config.MaxConcurrentTasks,
	)

	return tm.scheduler.start(ctx)
}

// StopWorkers stops the background scheduler gracefully.
// This waits for any currently running tasks to complete before returning.
//
// Returns ErrWorkersNotRunning if workers are not running.
func (tm *TaskManager) StopWorkers() error {
	if tm.scheduler == nil {
		return ErrWorkersNotRunning
	}

	tm.scheduler.stop()
	tm.scheduler = nil
	return nil
}

// IsWorkersRunning returns true if the background workers are currently running.
func (tm *TaskManager) IsWorkersRunning() bool {
	return tm.scheduler != nil
}

// Close closes the database connection and stops workers if running.
// This should be called when shutting down the application to ensure
// proper cleanup of resources.
func (tm *TaskManager) Close() error {
	if tm.scheduler != nil {
		tm.StopWorkers()
	}
	return tm.db.Close()
}

// strPtr returns a pointer to a string.
func strPtr(s string) *string {
	return &s
}

// timePtr returns a pointer to a time.Time.
func timePtr(t time.Time) *time.Time {
	return &t
}
