package waitingroom

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	config         Config
	room           *room
	registry       *handlerRegistry
	scheduler      *scheduler
	slackNotifier  *SlackNotifier
	db             *sql.DB
	instanceID     string
}

// New creates a new TaskManager with the given configuration.
// This initializes the database connection and runs migrations automatically.
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
//	tm, err := waitingroom.New(config)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer tm.Close()
func New(config Config) (*TaskManager, error) {
	config.setDefaults()

	// Get database connection URL
	databaseURL, err := config.Database.ConnectionString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfigInvalid, err)
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
		config:     config,
		room:       r,
		registry:   newHandlerRegistry(),
		db:         db,
		instanceID: uuid.New().String(),
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
// Approved tasks are queued for execution by the scheduler; they do not run immediately.
//
// The taskType must have a registered handler, or ErrHandlerNotFound is returned.
// The payload is passed to the handler's Execute method when the task runs.
// Metadata can be used to store custom data with the task.
//
// A custom approval function can be provided via opts.ApprovalFunc to override
// the default approval logic for this specific task.
//
// If opts is nil, default values are used (auto-approve, no schedule, no metadata).
//
// Returns the created task with its assigned ID and initial status.
func (tm *TaskManager) CreateTask(ctx context.Context, taskType string, payload json.RawMessage, opts *CreateOptions) (*Task, error) {
	// Validate that a handler exists for this task type
	if _, err := tm.registry.get(taskType); err != nil {
		return nil, err
	}

	// Use default options if nil
	if opts == nil {
		opts = &CreateOptions{}
	}

	// Determine if approval is required
	// Use per-task approval function if provided, otherwise auto-approve
	approvalFunc := opts.ApprovalFunc
	if approvalFunc == nil {
		approvalFunc = DefaultApprovalDecision
	}
	approvalDecision := approvalFunc(ctx, taskType, payload)

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
// If opts.ScheduledAt is set to a future time, the task transitions to "scheduled" status.
// If opts.ScheduledAt is nil, the task's original schedule is used.
// If no schedule is set or opts.ScheduledAt is in the past, the task is invalid.
//
// The opts.ScheduledAt overrides any schedule set during task creation.
//
// Returns ErrCannotApprove if the task is not in pending status.
// Returns ErrTaskNotFound if the task does not exist.
func (tm *TaskManager) Approve(ctx context.Context, taskID string, opts *ApproveOptions) (*Task, error) {
	if opts == nil {
		return nil, ErrApproveOptionsRequired
	}
	if opts.ApprovedBy == "" {
		return nil, ErrApprovedByRequired
	}

	task, err := tm.room.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if !task.CanApprove() {
		return nil, ErrCannotApprove
	}

	now := time.Now().UTC()
	task.ApprovedBy = &opts.ApprovedBy
	task.ApprovedAt = &now
	task.UpdatedAt = now

	// Use schedule from approve options if provided, otherwise use task's original schedule
	scheduleAt := opts.ScheduledAt
	if scheduleAt == nil {
		scheduleAt = task.ScheduledAt
	}

	// Determine next status based on scheduled time
	if scheduleAt != nil {
		if !scheduleAt.After(now) {
			return nil, ErrInvalidSchedule
		}
		task.Status = TaskStatusScheduled
		task.ScheduledAt = scheduleAt
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
// If Slack webhook is configured, this also starts the Slack notifier which sends
// periodic summaries of tasks requiring attention. The notifier uses distributed
// locking to ensure only one instance sends notifications.
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

	if err := tm.scheduler.start(ctx); err != nil {
		return err
	}

	// Start Slack notifier if webhook is configured
	if tm.config.Slack.WebhookURL != "" {
		tm.slackNotifier = newSlackNotifier(tm.room, tm.config.Slack, tm.instanceID)
		tm.slackNotifier.start(ctx)
	}

	return nil
}

// StopWorkers stops the background scheduler and Slack notifier gracefully.
// This waits for any currently running tasks to complete before returning.
//
// Returns ErrWorkersNotRunning if workers are not running.
func (tm *TaskManager) StopWorkers() error {
	if tm.scheduler == nil {
		return ErrWorkersNotRunning
	}

	tm.scheduler.stop()
	tm.scheduler = nil

	// Stop Slack notifier if running
	if tm.slackNotifier != nil {
		tm.slackNotifier.stop()
		tm.slackNotifier = nil
	}

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
