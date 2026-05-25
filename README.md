# Waiting-Room Task Manager

A Go library for managing tasks with approval workflows, scheduling, and distributed execution support using PostgreSQL.

## Features

- **Approval Workflows**: Define custom approval logic for different task types
- **Scheduled Tasks**: Schedule tasks to run at specific times
- **Distributed Execution**: Multiple instances can share the workload without conflicts using PostgreSQL-based distributed locking
- **Manual Retry**: Failed tasks require explicit retry via API
- **Class-Based Handlers**: Applications implement the `TaskHandler` interface for task types
- **Custom Metadata**: Store additional custom data with each task
- **Automatic Migrations**: Database tables are created automatically on initialization

## Installation

```bash
go get github.com/example/waiting-room
```

## Quick Start

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "time"

    "github.com/example/waiting-room"
)

// Define a task handler
type EmailTask struct{}

func (e *EmailTask) Type() string { return "send_email" }

// Execute receives the full Task object
func (e *EmailTask) Execute(ctx context.Context, task *waitingroom.Task) error {
    var data struct {
        To      string `json:"to"`
        Subject string `json:"subject"`
    }
    if err := json.Unmarshal(task.Payload, &data); err != nil {
        return err
    }
    
    // Validate in Execute
    if data.To == "" {
        return fmt.Errorf("recipient is required")
    }
    
    log.Printf("Sending email to %s: %s", data.To, data.Subject)
    return nil
}

func main() {
    ctx := context.Background()

    // Define approval logic
    approvalFunc := func(ctx context.Context, taskType string, payload json.RawMessage) waitingroom.ApprovalDecision {
        if taskType == "send_email" {
            return waitingroom.ApprovalDecision{RequiresApproval: false}
        }
        return waitingroom.ApprovalDecision{RequiresApproval: true}
    }

    // Initialize the library - database tables are created automatically
    config := waitingroom.Config{
        Database: waitingroom.DatabaseConfig{
            Host:     "localhost",
            Port:     "5432",
            Name:     "mydb",
            User:     "user",
            Password: "password",
            SSLMode:  "disable",
        },
        WorkerInterval: time.Minute,
    }

    tm, err := waitingroom.New(config, approvalFunc)
    if err != nil {
        log.Fatal(err)
    }
    defer tm.Close()

    // Register task handlers
    tm.RegisterHandler(&EmailTask{})

    // Start background workers
    tm.StartWorkers(ctx)
    defer tm.StopWorkers()

    // Create a task with metadata
    payload, _ := json.Marshal(map[string]string{
        "to":      "user@example.com",
        "subject": "Hello",
    })
    metadata, _ := json.Marshal(map[string]string{
        "source": "api",
        "ip": "192.168.1.1",
    })

    task, err := tm.CreateTask(ctx, "send_email", payload, waitingroom.CreateOptions{
        Metadata: metadata,
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Created task: %s (status: %s)", task.ID, task.Status)
}
```

## Configuration

### DatabaseConfig

```go
type DatabaseConfig struct {
    Host     string  // PostgreSQL host (default: "localhost")
    Port     string  // PostgreSQL port (default: "5432")
    Name     string  // Database name (required)
    User     string  // Database username (required)
    Password string  // Database password (required)
    SSLMode  string  // SSL mode (default: "disable")
}
```

### Config

```go
type Config struct {
    Database           DatabaseConfig  // PostgreSQL config (required)
    WorkerInterval     time.Duration   // Polling interval (default: 1m)
    LockTimeout        time.Duration   // Lock timeout (default: 5m)
    MaxConcurrentTasks int             // Max concurrent tasks (default: 10)
    SkipMigration      bool            // Skip automatic migration (default: false)
}
```

**Note**: `instanceID` is auto-generated internally on each startup.

## Automatic Migrations

Database tables are created automatically when you call `New()`. The migration:
- Creates `waiting_room_tasks` table for task storage
- Creates `waiting_room_locks` table for distributed locking
- Creates necessary indexes
- Uses `IF NOT EXISTS` so it's safe to run multiple times

To skip automatic migration (e.g., if you manage migrations externally):

```go
config := waitingroom.Config{
    Database: dbConfig,
    SkipMigration: true,  // Skip automatic migration
}
```

## Public API

The library exposes a minimal public API surface:

### Types
- `TaskManager` - Main API for creating and managing tasks
- `TaskHandler` - Interface for implementing task handlers
- `Task` - Task model with all task data
- `TaskStatus` - Task status constants
- `Config`, `DatabaseConfig` - Configuration
- `CreateOptions`, `ListFilter` - Options for operations
- `ApprovalDecision`, `ApprovalDecisionFunc` - Approval logic
- Error variables (e.g., `ErrTaskNotFound`, `ErrHandlerNotFound`)

### Functions
- `New(config, approvalFunc)` - Create a new TaskManager (with auto-migration)
- `NewTaskHandler(taskType, execFunc)` - Create handler from function
- `DefaultApprovalDecision`, `AlwaysRequireApproval`, `ConditionalApproval` - Pre-built approval functions

All other types are internal implementation details.

## Task Handler Interface

Applications must implement the `TaskHandler` interface:

```go
type TaskHandler interface {
    Type() string                                      // Unique task type identifier
    Execute(ctx context.Context, task *Task) error     // Run the task
}
```

The handler receives the full `Task` object, which includes:
- `ID` - Task UUID
- `Payload` - Task-specific data
- `Metadata` - Custom user-defined data
- `RetryCount` - Number of retry attempts
- `CreatedAt`, `UpdatedAt` - Timestamps

### Handler Implementation Example

```go
type EmailTask struct{}

func (e *EmailTask) Type() string { 
    return "send_email" 
}

func (e *EmailTask) Execute(ctx context.Context, task *waitingroom.Task) error {
    var data struct {
        To      string `json:"to"`
        Subject string `json:"subject"`
    }
    
    if err := json.Unmarshal(task.Payload, &data); err != nil {
        return err
    }
    
    // Access metadata if needed
    if task.Metadata != nil {
        var meta map[string]string
        json.Unmarshal(task.Metadata, &meta)
        log.Printf("Request from: %s", meta["source"])
    }
    
    // Task execution logic here
    return sendEmail(data.To, data.Subject)
}
```

### Functional Handler

For simple handlers, use `NewTaskHandler`:

```go
handler := waitingroom.NewTaskHandler("log_task", func(ctx context.Context, task *waitingroom.Task) error {
    var data map[string]string
    json.Unmarshal(task.Payload, &data)
    log.Printf("Task %s: %v", task.ID, data)
    return nil
})

tm.RegisterHandler(handler)
```

## Approval Decision Function

The approval function determines whether a task requires approval before execution:

```go
type ApprovalDecisionFunc func(ctx context.Context, taskType string, payload json.RawMessage) ApprovalDecision

type ApprovalDecision struct {
    RequiresApproval bool
    Reason           string
}
```

### Pre-built Functions

```go
// Auto-approve all tasks
approvalFunc := waitingroom.DefaultApprovalDecision

// Require approval for all tasks
approvalFunc := waitingroom.AlwaysRequireApproval

// Require approval for specific types
approvalFunc := waitingroom.ConditionalApproval("bulk_email", "delete_data")
```

### Custom Function

```go
approvalFunc := func(ctx context.Context, taskType string, payload json.RawMessage) waitingroom.ApprovalDecision {
    if taskType == "bulk_email" {
        var data struct{ Count int `json:"count"` }
        json.Unmarshal(payload, &data)
        
        if data.Count > 100 {
            return waitingroom.ApprovalDecision{
                RequiresApproval: true,
                Reason:           "Bulk emails require approval",
            }
        }
    }
    return waitingroom.ApprovalDecision{RequiresApproval: false}
}
```

## Task Lifecycle

```
Create Task
    |
    +-- Approval required? --+-- YES --> PENDING --> Approve() --> APPROVED/scheduled
                             |
                             +-- NO --> APPROVED
                                              |
                                              v
                                         (Scheduler picks up)
                                              |
                                              v
                                          RUNNING
                                              |
                              +-- Success --+--+-+-- Failure --+
                              |                             |
                              v                             v
                          COMPLETED                      FAILED
                                                                   |
                                                                   +-- Retry() --+
```

## API Methods

### CreateTask
Creates a new task. The approval function determines if it requires approval.

```go
task, err := tm.CreateTask(ctx, "send_email", payload, waitingroom.CreateOptions{
    ScheduledAt: &futureTime,  // Optional: schedule for later
    Metadata:    metadata,     // Optional: custom data
})
```

### Get
Retrieves a single task by ID.

```go
task, err := tm.Get(ctx, taskID)
```

### List
Lists tasks matching filter criteria.

```go
tasks, err := tm.List(ctx, waitingroom.ListFilter{
    Status: waitingroom.TaskStatusPending,
    Type:   "send_email",
    Limit:  100,
})
```

### Approve
Approves a pending task.

```go
task, err := tm.Approve(ctx, taskID, "admin@example.com")
```

### Cancel
Cancels a task that hasn't started running.

```go
task, err := tm.Cancel(ctx, taskID)
```

### Retry
Retries a failed task.

```go
task, err := tm.Retry(ctx, taskID)
```

### RegisterHandler / UnregisterHandler
Registers or unregisters a task handler.

```go
err := tm.RegisterHandler(&MyHandler{})
tm.UnregisterHandler("my_task_type")
```

### HandlerTypes / HandlerCount
Returns registered handler information.

```go
types := tm.HandlerTypes()
count := tm.HandlerCount()
```

### StartWorkers / StopWorkers / IsWorkersRunning
Controls the background scheduler.

```go
err := tm.StartWorkers(ctx)
err := tm.StopWorkers()
running := tm.IsWorkersRunning()
```

### Close
Closes the database connection and stops workers.

```go
err := tm.Close()
```

## Database Schema

The library creates two tables automatically:

### waiting_room_tasks

| Column | Type | Description |
|--------|------|-------------|
| id | UUID | Primary key |
| type | VARCHAR(255) | Task type identifier |
| status | VARCHAR(50) | Task status |
| payload | JSONB | Task-specific data |
| metadata | JSONB | Custom user-defined data |
| requires_approval | BOOLEAN | Whether approval is needed |
| approved_by | VARCHAR(255) | Who approved the task |
| approved_at | TIMESTAMP | When the task was approved |
| scheduled_at | TIMESTAMP | When the task should run |
| created_at | TIMESTAMP | Creation time |
| updated_at | TIMESTAMP | Last modification time |
| run_at | TIMESTAMP | When the task was executed |
| retry_count | INT | Number of retries |

### waiting_room_locks

| Column | Type | Description |
|--------|------|-------------|
| lock_key | VARCHAR(255) | Primary key |
| locked_at | TIMESTAMP | When the lock was acquired |
| expires_at | TIMESTAMP | When the lock expires |
| instance_id | VARCHAR(255) | Which instance holds the lock |

## Metadata

Store custom data with each task:

```go
metadata, _ := json.Marshal(map[string]string{
    "source": "webhook",
    "user_id": "12345",
    "ip_address": "192.168.1.1",
})

task, err := tm.CreateTask(ctx, "send_email", payload, waitingroom.CreateOptions{
    Metadata: metadata,
})

// Access in handler
func (h *MyHandler) Execute(ctx context.Context, task *waitingroom.Task) error {
    var meta map[string]string
    json.Unmarshal(task.Metadata, &meta)
    log.Printf("Source: %s", meta["source"])
    return nil
}
```

## Example Application

See the [examples/simple](examples/simple) directory for a complete REST API example.

### Running the Example

1. Start PostgreSQL:
```bash
docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=waitingroom postgres:15
```

2. Run the example:
```bash
cd examples/simple
go run main.go
```

3. Create a task:
```bash
curl -X POST http://localhost:8080/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "type": "send_email",
    "payload": {
      "to": "user@example.com",
      "subject": "Welcome",
      "body": "Hello!"
    },
    "metadata": {
      "source": "api",
      "ip": "192.168.1.1"
    }
  }'
```

4. List tasks:
```bash
curl http://localhost:8080/tasks
```

5. Approve a task:
```bash
curl -X POST "http://localhost:8080/tasks/{task-id}/approve?by=admin"
```

## Package Structure

| File | Purpose |
|------|---------|
| `handler.go` | Package documentation and `TaskHandler` interface |
| `models.go` | `Task` model, status types, options, filters |
| `config.go` | `Config` and `DatabaseConfig` |
| `approval.go` | `ApprovalDecision` and helper functions |
| `errors.go` | Exported error variables |
| `manager.go` | `TaskManager` - main public API |
| `room.go` | `room` (internal database operations) |
| `registry.go` | `handlerRegistry` (internal) |
| `scheduler.go` | `scheduler` (internal) |
| `migrations.go` | Automatic migration (internal) |

All internal implementation details are unexported. Only `TaskManager` and related types/methods are public.

## Distributed Execution

When running multiple instances, the library uses PostgreSQL-based distributed locking to ensure:

1. Only one instance polls for scheduled tasks at a time
2. Each task is executed by only one instance
3. Failed instances automatically release their locks after the timeout

The `instanceID` is auto-generated internally on each startup, so no configuration is needed regardless of deployment environment.

## Error Handling

```go
var (
    ErrTaskNotFound              = errors.New("task not found")
    ErrHandlerNotFound           = errors.New("task handler not found")
    ErrHandlerAlreadyRegistered  = errors.New("task handler already registered")
    ErrCannotApprove             = errors.New("task cannot be approved")
    ErrCannotCancel              = errors.New("task cannot be cancelled")
    ErrCannotRetry               = errors.New("task cannot be retried")
    ErrWorkersAlreadyRunning     = errors.New("workers are already running")
    ErrConfigInvalid             = errors.New("invalid configuration")
    // ... etc
)
```

**Note**: Error messages from failed task execution are logged but not stored in the database. Handlers should validate and return errors as needed in the `Execute` method.

## License

MIT License
