package waitingroom

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// scheduler manages the execution of scheduled tasks with distributed locking.
// It ensures that only one instance processes tasks at a time while allowing
// multiple instances to share the workload.
type scheduler struct {
	room          *room
	registry      *handlerRegistry
	interval      time.Duration
	lockTimeout   time.Duration
	instanceID    string
	maxConcurrent int
	stopCh        chan struct{}
	wg            sync.WaitGroup
	isRunning     bool
	mu            sync.Mutex
	semaphore     chan struct{} // Limits concurrent task execution
}

// newScheduler creates a new scheduler with the given dependencies.
// instanceID is auto-generated internally for each instance.
func newScheduler(room *room, registry *handlerRegistry, interval, lockTimeout time.Duration, maxConcurrent int) *scheduler {
	// Generate a unique instance ID for this running instance
	instanceID := uuid.New().String()

	return &scheduler{
		room:          room,
		registry:      registry,
		interval:      interval,
		lockTimeout:   lockTimeout,
		instanceID:    instanceID,
		maxConcurrent: maxConcurrent,
		stopCh:        make(chan struct{}),
		semaphore:     make(chan struct{}, maxConcurrent),
	}
}

// start begins the scheduler's polling loop for tasks.
// It runs until stop() is called or the context is cancelled.
func (s *scheduler) start(ctx context.Context) error {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return ErrWorkersAlreadyRunning
	}
	s.isRunning = true
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run(ctx)

	return nil
}

// stop gracefully shuts down the scheduler, waiting for running tasks to complete.
func (s *scheduler) stop() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}
	s.isRunning = false
	s.mu.Unlock()

	close(s.stopCh)
	s.wg.Wait()

	// Release the scheduler lock if held
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.room.releaseLock(ctx, "scheduler", s.instanceID)
}

// run is the main polling loop.
func (s *scheduler) run(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run immediately on start
	s.pollAndProcess(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.pollAndProcess(ctx)
		}
	}
}

// pollAndProcess attempts to acquire the scheduler lock and process tasks.
func (s *scheduler) pollAndProcess(ctx context.Context) {
	// Try to acquire the scheduler lock
	acquired, err := s.room.acquireLock(ctx, "scheduler", s.instanceID, s.lockTimeout)
	if err != nil {
		log.Printf("Scheduler: failed to acquire lock: %v", err)
		return
	}
	if !acquired {
		// Another instance is currently processing
		return
	}

	// Release lock when done (unless renewed during processing)
	defer s.room.releaseLock(ctx, "scheduler", s.instanceID)

	// Process scheduled tasks
	s.processScheduledTasks(ctx)
}

// processScheduledTasks fetches and executes tasks that are ready to run.
func (s *scheduler) processScheduledTasks(ctx context.Context) {
	now := time.Now().UTC()

	// Get tasks ready for execution
	tasks, err := s.room.getScheduledTasks(ctx, now, s.maxConcurrent*2)
	if err != nil {
		log.Printf("Scheduler: failed to get scheduled tasks: %v", err)
		return
	}

	for _, task := range tasks {
		// Try to acquire a task-specific lock to prevent duplicate execution
		lockKey := fmt.Sprintf("task:%s", task.ID)
		acquired, err := s.room.acquireLock(ctx, lockKey, s.instanceID, s.lockTimeout)
		if err != nil {
			log.Printf("Scheduler: failed to acquire task lock for %s: %v", task.ID, err)
			continue
		}
		if !acquired {
			// Another instance is processing this task
			continue
		}

		// Execute the task in a goroutine with semaphore for concurrency control
		s.wg.Add(1)
		s.semaphore <- struct{}{} // Acquire slot

		go func(t *Task) {
			defer s.wg.Done()
			defer func() { <-s.semaphore }() // Release slot
			defer s.room.releaseLock(ctx, fmt.Sprintf("task:%s", t.ID), s.instanceID)

			s.executeTask(ctx, t)
		}(task)
	}
}

// executeTask runs a single task with proper status management.
func (s *scheduler) executeTask(ctx context.Context, task *Task) {
	// Get the handler for this task type
	handler, err := s.registry.get(task.Type)
	if err != nil {
		log.Printf("Scheduler: no handler for task type %s: %v", task.Type, err)
		s.failTask(ctx, task.ID)
		return
	}

	// Mark task as running
	task.Status = TaskStatusRunning
	task.RunAt = timePtr(time.Now().UTC())
	if err := s.room.Update(ctx, task); err != nil {
		log.Printf("Scheduler: failed to update task status to running: %v", err)
		return
	}

	// Create a context with timeout for task execution
	execCtx, cancel := context.WithTimeout(ctx, s.lockTimeout)
	defer cancel()

	// Execute the task
	// The handler receives the full task, not just the payload
	err = handler.Execute(execCtx, task)

	// Update task status based on execution result
	if err != nil {
		log.Printf("Scheduler: task %s failed: %v", task.ID, err)
		s.failTask(ctx, task.ID)
	} else {
		log.Printf("Scheduler: task %s completed successfully", task.ID)
		s.completeTask(ctx, task.ID)
	}
}

// failTask marks a task as failed.
// Error messages are logged but not stored in the database.
func (s *scheduler) failTask(ctx context.Context, taskID string) {
	if err := s.room.updateTaskStatus(ctx, taskID, TaskStatusFailed); err != nil {
		log.Printf("Scheduler: failed to update task as failed: %v", err)
	}
}

// completeTask marks a task as completed.
func (s *scheduler) completeTask(ctx context.Context, taskID string) {
	if err := s.room.updateTaskStatus(ctx, taskID, TaskStatusCompleted); err != nil {
		log.Printf("Scheduler: failed to update task as completed: %v", err)
	}
}
