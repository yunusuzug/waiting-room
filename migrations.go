package waitingroom

import (
	"context"
	"fmt"
)

// migrationSQL contains the database schema for the waiting-room library.
// This is executed automatically when creating a new TaskManager.
const migrationSQL = `-- Migration: Initial schema for waiting-room task manager

CREATE TABLE IF NOT EXISTS waiting_room_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    metadata JSONB,
    requires_approval BOOLEAN NOT NULL DEFAULT false,
    approved_by VARCHAR(255),
    approved_at TIMESTAMP,
    scheduled_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    run_at TIMESTAMP,
    retry_count INT NOT NULL DEFAULT 0,
    CONSTRAINT valid_status CHECK (status IN (
        'pending', 'approved', 'rejected', 'scheduled', 'running', 'completed', 'failed', 'cancelled'
    ))
);

CREATE INDEX IF NOT EXISTS idx_waiting_room_tasks_status ON waiting_room_tasks(status);
CREATE INDEX IF NOT EXISTS idx_waiting_room_tasks_status_scheduled ON waiting_room_tasks(status, scheduled_at) WHERE status IN ('approved', 'scheduled');
CREATE INDEX IF NOT EXISTS idx_waiting_room_tasks_type ON waiting_room_tasks(type);
CREATE INDEX IF NOT EXISTS idx_waiting_room_tasks_created ON waiting_room_tasks(created_at DESC);

CREATE TABLE IF NOT EXISTS waiting_room_locks (
    lock_key VARCHAR(255) PRIMARY KEY,
    locked_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL,
    instance_id VARCHAR(255) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_waiting_room_locks_expires ON waiting_room_locks(expires_at);
`

// migrate runs the database migration to create the necessary tables.
// This is called automatically by New() unless SkipMigration is set in Config.
func migrate(ctx context.Context, r *room) error {
	_, err := r.db.ExecContext(ctx, migrationSQL)
	if err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	return nil
}
