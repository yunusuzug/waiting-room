package waitingroom

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// table names are fixed as per requirements
const (
	tasksTableName = "waiting_room_tasks"
	locksTableName = "waiting_room_locks"
)

// room provides database operations for tasks using PostgreSQL.
type room struct {
	db *sql.DB
}

// newRoom creates a new room with the given database connection.
func newRoom(db *sql.DB) *room {
	return &room{
		db: db,
	}
}

// Create inserts a new task into the database.
func (r *room) Create(ctx context.Context, task *Task) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (id, type, status, payload, metadata, requires_approval, approved_by, approved_at, 
			scheduled_at, created_at, updated_at, run_at, retry_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, tasksTableName)

	_, err := r.db.ExecContext(ctx, query,
		task.ID, task.Type, task.Status, task.Payload, task.Metadata,
		task.RequiresApproval, task.ApprovedBy, task.ApprovedAt, task.ScheduledAt,
		task.CreatedAt, task.UpdatedAt, task.RunAt, task.RetryCount,
	)
	return err
}

// Get retrieves a task by its ID.
func (r *room) Get(ctx context.Context, id string) (*Task, error) {
	query := fmt.Sprintf(`
		SELECT id, type, status, payload, metadata, requires_approval, approved_by, approved_at,
			scheduled_at, created_at, updated_at, run_at, retry_count
		FROM %s WHERE id = $1
	`, tasksTableName)

	task := &Task{}
	var payload, metadata []byte
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&task.ID, &task.Type, &task.Status, &payload, &metadata,
		&task.RequiresApproval, &task.ApprovedBy, &task.ApprovedAt,
		&task.ScheduledAt, &task.CreatedAt, &task.UpdatedAt, &task.RunAt, &task.RetryCount,
	)
	if err == sql.ErrNoRows {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}

	task.Payload = json.RawMessage(payload)
	if metadata != nil {
		task.Metadata = json.RawMessage(metadata)
	}
	return task, nil
}

// Update updates an existing task in the database.
func (r *room) Update(ctx context.Context, task *Task) error {
	task.UpdatedAt = time.Now().UTC()

	query := fmt.Sprintf(`
		UPDATE %s SET
			type = $2, status = $3, payload = $4, metadata = $5, requires_approval = $6,
			approved_by = $7, approved_at = $8, scheduled_at = $9, updated_at = $10,
			run_at = $11, retry_count = $12
		WHERE id = $1
	`, tasksTableName)

	result, err := r.db.ExecContext(ctx, query,
		task.ID, task.Type, task.Status, task.Payload, task.Metadata,
		task.RequiresApproval, task.ApprovedBy, task.ApprovedAt, task.ScheduledAt,
		task.UpdatedAt, task.RunAt, task.RetryCount,
	)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTaskNotFound
	}

	return nil
}

// List retrieves tasks matching the given filter criteria.
func (r *room) List(ctx context.Context, filter ListFilter) ([]*Task, error) {
	filter.setDefaults()

	whereClause := "WHERE 1=1"
	args := []interface{}{}
	argIndex := 1

	if filter.Status != "" {
		whereClause += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, filter.Status)
		argIndex++
	}
	if filter.Type != "" {
		whereClause += fmt.Sprintf(" AND type = $%d", argIndex)
		args = append(args, filter.Type)
		argIndex++
	}

	query := fmt.Sprintf(`
		SELECT id, type, status, payload, metadata, requires_approval, approved_by, approved_at,
			scheduled_at, created_at, updated_at, run_at, retry_count
		FROM %s
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, tasksTableName, whereClause, argIndex, argIndex+1)

	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTasks(rows)
}

// getPendingTasks retrieves tasks that are pending approval.
func (r *room) getPendingTasks(ctx context.Context, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT id, type, status, payload, metadata, requires_approval, approved_by, approved_at,
			scheduled_at, created_at, updated_at, run_at, retry_count
		FROM %s
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT $2
	`, tasksTableName)

	rows, err := r.db.QueryContext(ctx, query, TaskStatusPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTasks(rows)
}

// getScheduledTasks retrieves tasks that are ready to run (approved or scheduled, and due).
func (r *room) getScheduledTasks(ctx context.Context, before time.Time, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT id, type, status, payload, metadata, requires_approval, approved_by, approved_at,
			scheduled_at, created_at, updated_at, run_at, retry_count
		FROM %s
		WHERE (status = $1 OR (status = $2 AND scheduled_at <= $3))
		ORDER BY COALESCE(scheduled_at, created_at) ASC
		LIMIT $4
	`, tasksTableName)

	rows, err := r.db.QueryContext(ctx, query, TaskStatusApproved, TaskStatusScheduled, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTasks(rows)
}

// acquireLock attempts to acquire a distributed lock with the given key and timeout.
func (r *room) acquireLock(ctx context.Context, lockKey string, instanceID string, timeout time.Duration) (bool, error) {
	// First, try to clean up expired locks
	cleanupQuery := fmt.Sprintf(`
		DELETE FROM %s WHERE expires_at < NOW()
	`, locksTableName)
	r.db.ExecContext(ctx, cleanupQuery)

	// Try to insert a new lock
	expiresAt := time.Now().UTC().Add(timeout)
	insertQuery := fmt.Sprintf(`
		INSERT INTO %s (lock_key, locked_at, expires_at, instance_id)
		VALUES ($1, NOW(), $2, $3)
		ON CONFLICT (lock_key) DO NOTHING
	`, locksTableName)

	result, err := r.db.ExecContext(ctx, insertQuery, lockKey, expiresAt, instanceID)
	if err != nil {
		return false, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows > 0, nil
}

// releaseLock releases a distributed lock.
func (r *room) releaseLock(ctx context.Context, lockKey string, instanceID string) error {
	query := fmt.Sprintf(`
		DELETE FROM %s WHERE lock_key = $1 AND instance_id = $2
	`, locksTableName)

	_, err := r.db.ExecContext(ctx, query, lockKey, instanceID)
	return err
}

// updateTaskStatus updates only the status of a task (for atomic status transitions).
func (r *room) updateTaskStatus(ctx context.Context, taskID string, status TaskStatus) error {
	query := fmt.Sprintf(`
		UPDATE %s SET status = $1, updated_at = NOW()
		WHERE id = $2
	`, tasksTableName)

	result, err := r.db.ExecContext(ctx, query, status, taskID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTaskNotFound
	}

	return nil
}

// scanTasks helper to scan rows into Task objects.
func (r *room) scanTasks(rows *sql.Rows) ([]*Task, error) {
	var tasks []*Task

	for rows.Next() {
		task := &Task{}
		var payload, metadata []byte

		err := rows.Scan(
			&task.ID, &task.Type, &task.Status, &payload, &metadata,
			&task.RequiresApproval, &task.ApprovedBy, &task.ApprovedAt,
			&task.ScheduledAt, &task.CreatedAt, &task.UpdatedAt, &task.RunAt, &task.RetryCount,
		)
		if err != nil {
			return nil, err
		}

		task.Payload = json.RawMessage(payload)
		if metadata != nil {
			task.Metadata = json.RawMessage(metadata)
		}
		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// getTaskSummary retrieves the count of tasks grouped by status.
func (r *room) getTaskSummary(ctx context.Context) (*TaskSummary, error) {
	query := fmt.Sprintf(`
		SELECT status, COUNT(*) 
		FROM %s 
		WHERE status IN ($1, $2, $3, $4, $5)
		GROUP BY status
	`, tasksTableName)

	rows, err := r.db.QueryContext(ctx, query,
		TaskStatusPending, TaskStatusApproved, TaskStatusScheduled, TaskStatusRunning, TaskStatusFailed,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summary := &TaskSummary{}
	for rows.Next() {
		var status TaskStatus
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}

		switch status {
		case TaskStatusPending:
			summary.Pending = count
		case TaskStatusApproved:
			summary.Approved = count
		case TaskStatusScheduled:
			summary.Scheduled = count
		case TaskStatusRunning:
			summary.Running = count
		case TaskStatusFailed:
			summary.Failed = count
		}
	}

	return summary, rows.Err()
}
