package task

// Priority represents the priority level of a task.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
)

// ValidPriorities returns all valid priority values.
func ValidPriorities() []Priority {
	return []Priority{PriorityLow, PriorityMedium, PriorityHigh}
}

// IsValid checks if the priority is a valid value.
func (p Priority) IsValid() bool {
	switch p {
	case PriorityLow, PriorityMedium, PriorityHigh:
		return true
	default:
		return false
	}
}

// String returns the string representation of the priority.
func (p Priority) String() string {
	return string(p)
}
