package task

// Status represents the status of a task.
type Status string

const (
	StatusOpen      Status = "open"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
)

// ValidStatuses returns all valid status values.
func ValidStatuses() []Status {
	return []Status{StatusOpen, StatusCompleted, StatusCancelled}
}

// IsValid checks if the status is a valid value.
func (s Status) IsValid() bool {
	switch s {
	case StatusOpen, StatusCompleted, StatusCancelled:
		return true
	default:
		return false
	}
}

// String returns the string representation of the status.
func (s Status) String() string {
	return string(s)
}

// CanTransitionTo returns true if the status can transition to the target status.
func (s Status) CanTransitionTo(target Status) bool {
	switch s {
	case StatusOpen:
		return target == StatusCompleted || target == StatusCancelled
	case StatusCompleted:
		return target == StatusOpen
	case StatusCancelled:
		return target == StatusOpen
	default:
		return false
	}
}

// TransitionError returns an error describing why the transition is invalid.
func (s Status) TransitionError(target Status) error {
	if !s.CanTransitionTo(target) {
		return &InvalidTransitionError{From: s, To: target}
	}
	return nil
}

// InvalidTransitionError represents an invalid status transition.
type InvalidTransitionError struct {
	From Status
	To   Status
}

func (e *InvalidTransitionError) Error() string {
	return "invalid status transition: " + string(e.From) + " -> " + string(e.To)
}

// IsInvalidTransitionError checks if the error is an InvalidTransitionError.
func IsInvalidTransitionError(err error) bool {
	_, ok := err.(*InvalidTransitionError)
	return ok
}
