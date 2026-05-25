package waitingroom

import (
	"context"
	"encoding/json"
)

// ApprovalDecision contains the result of evaluating whether a task requires approval.
type ApprovalDecision struct {
	// RequiresApproval is true if the task needs to be approved before execution.
	RequiresApproval bool

	// Reason provides an optional explanation for the decision.
	// This can be useful for logging or displaying to users.
	Reason string
}

// ApprovalDecisionFunc is a function that determines whether a task requires approval.
// Applications provide this function when initializing the TaskManager.
// It receives the task type and payload, and returns an ApprovalDecision.
type ApprovalDecisionFunc func(ctx context.Context, taskType string, payload json.RawMessage) ApprovalDecision

// DefaultApprovalDecision returns an approval decision that auto-approves all tasks.
// This can be used as a simple default when no custom approval logic is needed.
func DefaultApprovalDecision(ctx context.Context, taskType string, payload json.RawMessage) ApprovalDecision {
	return ApprovalDecision{
		RequiresApproval: false,
		Reason:           "Auto-approved by default",
	}
}

// AlwaysRequireApproval returns an approval decision that requires approval for all tasks.
// This can be used when all tasks must go through manual approval.
func AlwaysRequireApproval(ctx context.Context, taskType string, payload json.RawMessage) ApprovalDecision {
	return ApprovalDecision{
		RequiresApproval: true,
		Reason:           "All tasks require approval",
	}
}

// ConditionalApproval creates an approval decision function that requires approval
// only for tasks matching the specified types.
func ConditionalApproval(requireApprovalTypes ...string) ApprovalDecisionFunc {
	approvalMap := make(map[string]bool)
	for _, t := range requireApprovalTypes {
		approvalMap[t] = true
	}

	return func(ctx context.Context, taskType string, payload json.RawMessage) ApprovalDecision {
		if approvalMap[taskType] {
			return ApprovalDecision{
				RequiresApproval: true,
				Reason:           "Task type requires approval",
			}
		}
		return ApprovalDecision{
			RequiresApproval: false,
			Reason:           "Auto-approved",
		}
	}
}
