package task

import (
	"testing"
)

func TestStatus_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		exp    bool
	}{
		{"valid open", StatusOpen, true},
		{"valid completed", StatusCompleted, true},
		{"valid cancelled", StatusCancelled, true},
		{"invalid", Status("invalid"), false},
		{"empty", Status(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.exp {
				t.Errorf("Status.IsValid() = %v, want %v", got, tt.exp)
			}
		})
	}
}

func TestStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name     string
		from     Status
		to       Status
		expected bool
	}{
		{"open -> completed", StatusOpen, StatusCompleted, true},
		{"open -> cancelled", StatusOpen, StatusCancelled, true},
		{"open -> open", StatusOpen, StatusOpen, false},
		{"completed -> open", StatusCompleted, StatusOpen, true},
		{"completed -> completed", StatusCompleted, StatusCompleted, false},
		{"completed -> cancelled", StatusCompleted, StatusCancelled, false},
		{"cancelled -> open", StatusCancelled, StatusOpen, true},
		{"cancelled -> cancelled", StatusCancelled, StatusCancelled, false},
		{"cancelled -> completed", StatusCancelled, StatusCompleted, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.from.CanTransitionTo(tt.to); got != tt.expected {
				t.Errorf("CanTransitionTo(%v) = %v, want %v", tt.to, got, tt.expected)
			}
		})
	}
}

func TestStatus_TransitionError(t *testing.T) {
	tests := []struct {
		name      string
		from      Status
		to        Status
		expectErr bool
	}{
		{"valid open -> completed", StatusOpen, StatusCompleted, false},
		{"valid open -> cancelled", StatusOpen, StatusCancelled, false},
		{"valid completed -> open", StatusCompleted, StatusOpen, false},
		{"valid cancelled -> open", StatusCancelled, StatusOpen, false},
		{"invalid open -> open", StatusOpen, StatusOpen, true},
		{"invalid completed -> completed", StatusCompleted, StatusCompleted, true},
		{"invalid completed -> cancelled", StatusCompleted, StatusCancelled, true},
		{"invalid cancelled -> completed", StatusCancelled, StatusCompleted, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.from.TransitionError(tt.to)
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error for transition %v -> %v", tt.from, tt.to)
				}
				if !IsInvalidTransitionError(err) {
					t.Errorf("expected InvalidTransitionError, got %T", err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestStatus_String(t *testing.T) {
	if StatusOpen.String() != "open" {
		t.Errorf("StatusOpen.String() = %q, want \"open\"", StatusOpen.String())
	}
	if StatusCompleted.String() != "completed" {
		t.Errorf("StatusCompleted.String() = %q, want \"completed\"", StatusCompleted.String())
	}
	if StatusCancelled.String() != "cancelled" {
		t.Errorf("StatusCancelled.String() = %q, want \"cancelled\"", StatusCancelled.String())
	}
}

func TestValidStatuses(t *testing.T) {
	statuses := ValidStatuses()
	if len(statuses) != 3 {
		t.Errorf("ValidStatuses() returned %d statuses, want 3", len(statuses))
	}
}
