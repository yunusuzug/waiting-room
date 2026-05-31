package waitingroom

import "time"

// strPtr returns a pointer to a string.
func strPtr(s string) *string {
	return &s
}

// timePtr returns a pointer to a time.Time.
func timePtr(t time.Time) *time.Time {
	return &t
}
