package task

import (
	"testing"
)

func TestPriority_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		priority Priority
		expected bool
	}{
		{"valid low", PriorityLow, true},
		{"valid medium", PriorityMedium, true},
		{"valid high", PriorityHigh, true},
		{"invalid", Priority("invalid"), false},
		{"empty", Priority(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.priority.IsValid(); got != tt.expected {
				t.Errorf("Priority.IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPriority_String(t *testing.T) {
	if PriorityLow.String() != "low" {
		t.Errorf("PriorityLow.String() = %q, want \"low\"", PriorityLow.String())
	}
	if PriorityMedium.String() != "medium" {
		t.Errorf("PriorityMedium.String() = %q, want \"medium\"", PriorityMedium.String())
	}
	if PriorityHigh.String() != "high" {
		t.Errorf("PriorityHigh.String() = %q, want \"high\"", PriorityHigh.String())
	}
}

func TestValidPriorities(t *testing.T) {
	priorities := ValidPriorities()
	if len(priorities) != 3 {
		t.Errorf("ValidPriorities() returned %d priorities, want 3", len(priorities))
	}
}
