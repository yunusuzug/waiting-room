package waitingroom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SlackConfig holds the configuration for Slack notifications.
type SlackConfig struct {
	// WebhookURL is the Slack incoming webhook URL.
	// If empty, Slack notifications are disabled.
	WebhookURL string

	// NotificationInterval is the interval between summary notifications.
	// Default is 24 hours (daily).
	NotificationInterval time.Duration
}

// SlackMessage represents a Slack webhook payload.
type SlackMessage struct {
	Text        string       `json:"text,omitempty"`
	Blocks      []Block      `json:"blocks,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Block represents a Slack block for rich formatting.
type Block struct {
	Type string `json:"type"`
	Text *Text  `json:"text,omitempty"`
}

// Text represents a Slack text object.
type Text struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji bool   `json:"emoji,omitempty"`
}

// Attachment represents a Slack message attachment.
type Attachment struct {
	Color  string  `json:"color"`
	Title  string  `json:"title"`
	Text   string  `json:"text"`
	Fields []Field `json:"fields"`
}

// Field represents a field in a Slack attachment.
type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// SlackNotifier handles sending Slack notifications with distributed locking.
type SlackNotifier struct {
	room     *room
	config   SlackConfig
	interval time.Duration
	stopCh   chan struct{}
	instanceID string
}

// TaskSummary holds the count of tasks by status for the Slack message.
type TaskSummary struct {
	Pending   int
	Approved  int
	Scheduled int
	Running   int
	Failed    int
}

// newSlackNotifier creates a new Slack notifier with the given configuration.
func newSlackNotifier(room *room, config SlackConfig, instanceID string) *SlackNotifier {
	interval := config.NotificationInterval
	if interval <= 0 {
		interval = 24 * time.Hour // Default to daily
	}

	return &SlackNotifier{
		room:       room,
		config:     config,
		interval:   interval,
		stopCh:     make(chan struct{}),
		instanceID: instanceID,
	}
}

// start begins the notifier's polling loop for sending Slack summaries.
func (s *SlackNotifier) start(ctx context.Context) {
	if s.config.WebhookURL == "" {
		return // Slack notifications disabled
	}

	go s.run(ctx)
}

// stop gracefully shuts down the notifier.
func (s *SlackNotifier) stop() {
	close(s.stopCh)
}

// run is the main polling loop for sending Slack notifications.
func (s *SlackNotifier) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run immediately on start
	s.sendSummaryIfLeader(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.sendSummaryIfLeader(ctx)
		}
	}
}

// sendSummaryIfLeader attempts to acquire the lock and send a summary.
// Only the instance that acquires the lock will send the message, ensuring
// the message is sent exactly once regardless of how many pods are running.
func (s *SlackNotifier) sendSummaryIfLeader(ctx context.Context) {
	// Try to acquire the slack notifier lock
	lockKey := "slack_notifier"
	lockTimeout := s.interval + 5*time.Minute // Lock expires after interval + buffer

	acquired, err := s.room.acquireLock(ctx, lockKey, s.instanceID, lockTimeout)
	if err != nil {
		return // Silently fail - logging would be noisy
	}
	if !acquired {
		// Another instance is the leader for notifications
		return
	}

	// We are the leader - release lock when done
	defer s.room.releaseLock(ctx, lockKey, s.instanceID)

	// Get task summary
	summary, err := s.getTaskSummary(ctx)
	if err != nil {
		return // Silently fail
	}

	// Send Slack message
	s.sendSlackMessage(ctx, summary)
}

// getTaskSummary retrieves the count of tasks by status.
func (s *SlackNotifier) getTaskSummary(ctx context.Context) (*TaskSummary, error) {
	return s.room.getTaskSummary(ctx)
}

// sendSlackMessage sends the task summary to the configured Slack webhook.
func (s *SlackNotifier) sendSlackMessage(ctx context.Context, summary *TaskSummary) error {
	if s.config.WebhookURL == "" {
		return nil
	}

	message := s.buildSlackMessage(summary)

	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.config.WebhookURL, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack webhook returned status: %d", resp.StatusCode)
	}

	return nil
}

// buildSlackMessage creates a rich Slack message with the task summary.
func (s *SlackNotifier) buildSlackMessage(summary *TaskSummary) SlackMessage {
	totalWaiting := summary.Pending + summary.Approved + summary.Scheduled + summary.Failed

	color := "good" // green
	if summary.Failed > 0 {
		color = "danger" // red
	} else if summary.Pending > 0 {
		color = "warning" // yellow
	}

	return SlackMessage{
		Attachments: []Attachment{
			{
				Color: color,
				Title: ":clipboard: Waiting Room Task Summary",
				Text:  fmt.Sprintf("You have *%d* tasks requiring attention.", totalWaiting),
				Fields: []Field{
					{
						Title: ":hourglass_flowing_sand: Pending Approval",
						Value: fmt.Sprintf("*%d*", summary.Pending),
						Short: true,
					},
					{
						Title: ":calendar: Scheduled",
						Value: fmt.Sprintf("*%d*", summary.Scheduled),
						Short: true,
					},
					{
						Title: ":white_check_mark: Approved",
						Value: fmt.Sprintf("*%d*", summary.Approved),
						Short: true,
					},
					{
						Title: ":x: Failed",
						Value: fmt.Sprintf("*%d*", summary.Failed),
						Short: true,
					},
					{
						Title: ":rocket: Running",
						Value: fmt.Sprintf("*%d*", summary.Running),
						Short: true,
					},
				},
			},
		},
	}
}
