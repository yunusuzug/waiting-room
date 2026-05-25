package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/yunusuzug/waiting-room"
)

// EmailTask is a task handler for sending emails.
type EmailTask struct{}

// Type returns the task type identifier.
func (e *EmailTask) Type() string {
	return "send_email"
}

// Execute sends the email.
// The handler receives the full task object, not just the payload.
func (e *EmailTask) Execute(ctx context.Context, task *waitingroom.Task) error {
	// Parse the payload
	var data struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}

	if err := json.Unmarshal(task.Payload, &data); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	// Validate required fields
	if data.To == "" {
		return fmt.Errorf("recipient email is required")
	}
	if data.Subject == "" {
		return fmt.Errorf("subject is required")
	}

	// Access task metadata if needed
	if task.Metadata != nil {
		var meta map[string]string
		json.Unmarshal(task.Metadata, &meta)
		log.Printf("Task metadata: %v", meta)
	}

	// Simulate sending an email
	log.Printf("Sending email to %s: %s", data.To, data.Subject)
	log.Printf("Body: %s", data.Body)

	// Simulate a delay
	time.Sleep(100 * time.Millisecond)

	return nil
}

// ReportTask is a task handler for generating reports.
type ReportTask struct{}

// Type returns the task type identifier.
func (r *ReportTask) Type() string {
	return "generate_report"
}

// Execute generates a report.
func (r *ReportTask) Execute(ctx context.Context, task *waitingroom.Task) error {
	var data struct {
		ReportType string `json:"report_type"`
		DateRange  string `json:"date_range"`
	}

	if err := json.Unmarshal(task.Payload, &data); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	// Validate required fields
	if data.ReportType == "" {
		return fmt.Errorf("report_type is required")
	}

	log.Printf("Generating %s report for %s (task ID: %s)", data.ReportType, data.DateRange, task.ID)

	// Simulate report generation
	time.Sleep(500 * time.Millisecond)

	return nil
}

func main() {
	ctx := context.Background()

	// Get database credentials from environment or use defaults
	dbHost := getEnvOrDefault("DB_HOST", "localhost")
	dbPort := getEnvOrDefault("DB_PORT", "5432")
	dbName := getEnvOrDefault("DB_NAME", "waitingroom")
	dbUser := getEnvOrDefault("DB_USER", "postgres")
	dbPassword := getEnvOrDefault("DB_PASSWORD", "postgres")

	// Create approval decision function
	// Auto-approve simple emails, require approval for bulk and reports
	approvalFunc := func(ctx context.Context, taskType string, payload json.RawMessage) waitingroom.ApprovalDecision {
		switch taskType {
		case "send_email":
			var data struct {
				Bulk bool `json:"bulk"`
			}
			json.Unmarshal(payload, &data)

			if data.Bulk {
				return waitingroom.ApprovalDecision{
					RequiresApproval: true,
					Reason:           "Bulk email requires approval",
				}
			}
			return waitingroom.ApprovalDecision{
				RequiresApproval: false,
				Reason:           "Single email auto-approved",
			}

		case "generate_report":
			return waitingroom.ApprovalDecision{
				RequiresApproval: true,
				Reason:           "Reports require manual approval",
			}

		default:
			return waitingroom.ApprovalDecision{
				RequiresApproval: true,
				Reason:           "Unknown task type requires approval",
			}
		}
	}

	// Create configuration
	// Migration runs automatically by default
	config := waitingroom.Config{
		Database: waitingroom.DatabaseConfig{
			Host:     dbHost,
			Port:     dbPort,
			Name:     dbName,
			User:     dbUser,
			Password: dbPassword,
			SSLMode:  "disable",
		},
		WorkerInterval:     30 * time.Second,
		LockTimeout:        5 * time.Minute,
		MaxConcurrentTasks: 5,
		// SkipMigration: false, // Set to true to skip automatic migration
	}

	// Initialize the task manager
	// Database tables are created automatically
	tm, err := waitingroom.New(config, approvalFunc)
	if err != nil {
		log.Fatalf("Failed to create task manager: %v", err)
	}
	defer tm.Close()
	log.Println("Task manager initialized successfully")

	// Register task handlers
	if err := tm.RegisterHandler(&EmailTask{}); err != nil {
		log.Fatalf("Failed to register email handler: %v", err)
	}
	if err := tm.RegisterHandler(&ReportTask{}); err != nil {
		log.Fatalf("Failed to register report handler: %v", err)
	}
	log.Printf("Registered %d task handlers", tm.HandlerCount())

	// Start background workers
	if err := tm.StartWorkers(ctx); err != nil {
		log.Fatalf("Failed to start workers: %v", err)
	}
	defer tm.StopWorkers()
	log.Println("Background workers started")

	// Create HTTP API
	mux := http.NewServeMux()

	// POST /tasks - Create a new task
	mux.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Type        string          `json:"type"`
			Payload     json.RawMessage `json:"payload"`
			ScheduledAt *time.Time      `json:"scheduled_at,omitempty"`
			Metadata    json.RawMessage `json:"metadata,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		opts := waitingroom.CreateOptions{
			ScheduledAt: req.ScheduledAt,
			Metadata:    req.Metadata,
		}

		task, err := tm.CreateTask(r.Context(), req.Type, req.Payload, opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	})

	// GET /tasks - List tasks
	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		filter := waitingroom.ListFilter{
			Status: waitingroom.TaskStatus(r.URL.Query().Get("status")),
			Type:   r.URL.Query().Get("type"),
			Limit:  100,
		}

		tasks, err := tm.List(r.Context(), filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tasks)
	})

	// GET /tasks/{id} - Get a task
	mux.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		task, err := tm.Get(r.Context(), id)
		if err != nil {
			if err == waitingroom.ErrTaskNotFound {
				http.Error(w, "Task not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	})

	// POST /tasks/{id}/approve - Approve a task
	mux.HandleFunc("POST /tasks/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		approvedBy := r.URL.Query().Get("by")
		if approvedBy == "" {
			approvedBy = "api"
		}

		task, err := tm.Approve(r.Context(), id, approvedBy)
		if err != nil {
			if err == waitingroom.ErrTaskNotFound {
				http.Error(w, "Task not found", http.StatusNotFound)
				return
			}
			if err == waitingroom.ErrCannotApprove {
				http.Error(w, "Task cannot be approved in current state", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	})

	// POST /tasks/{id}/cancel - Cancel a task
	mux.HandleFunc("POST /tasks/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		task, err := tm.Cancel(r.Context(), id)
		if err != nil {
			if err == waitingroom.ErrTaskNotFound {
				http.Error(w, "Task not found", http.StatusNotFound)
				return
			}
			if err == waitingroom.ErrCannotCancel {
				http.Error(w, "Task cannot be cancelled in current state", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	})

	// POST /tasks/{id}/retry - Retry a failed task
	mux.HandleFunc("POST /tasks/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		task, err := tm.Retry(r.Context(), id)
		if err != nil {
			if err == waitingroom.ErrTaskNotFound {
				http.Error(w, "Task not found", http.StatusNotFound)
				return
			}
			if err == waitingroom.ErrCannotRetry {
				http.Error(w, "Task cannot be retried in current state", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	})

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	log.Printf("Example commands:")
	log.Printf("  curl -X POST http://localhost:%s/tasks -d '{\"type\":\"send_email\",\"payload\":{\"to\":\"user@example.com\",\"subject\":\"Hello\",\"body\":\"World\"},\"metadata\":{\"source\":\"api\"}}'", port)
	log.Printf("  curl -X GET http://localhost:%s/tasks", port)
	log.Printf("  curl -X POST 'http://localhost:%s/tasks/{id}/approve?by=admin'", port)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// getEnvOrDefault returns the value of an environment variable or a default value.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
