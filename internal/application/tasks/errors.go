package tasks

import (
	"errors"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Application-level errors.
//
// Domain errors are passed through unchanged so callers can keep using
// errors.Is/errors.As against the domain sentinels; these are added only for
// situations the domain has no concept of (missing row, stale version, bad
// request parameters).
var (
	// ErrTaskNotFound is returned when no task exists with the requested ID.
	ErrTaskNotFound = errors.New("task not found")

	// ErrVersionConflict is returned when a mutation targets a stale version.
	// The caller must re-read the task and retry.
	ErrVersionConflict = errors.New("task version conflict")

	// ErrInvalidLimit is returned when a page size falls outside 1..MaxPageLimit.
	ErrInvalidLimit = errors.New("limit must be between 1 and 100")

	// ErrInvalidOffset is returned when a negative offset is supplied.
	ErrInvalidOffset = errors.New("offset must not be negative")

	// ErrInvalidSort is returned when the sort field is not supported or the
	// direction prefix is malformed.
	ErrInvalidSort = errors.New("invalid sort parameter")
)

// Page limits from API_CONTRACT.md.
const (
	// DefaultPageLimit is used when the request omits limit.
	DefaultPageLimit = 50

	// MaxPageLimit is the largest accepted page size.
	MaxPageLimit = 100
)

// TaskWithVersion pairs a task aggregate with its concurrency metadata.
//
// The version is deliberately not part of domain task.Task: optimistic locking
// is a persistence concern and belongs to the application/repository boundary.
type TaskWithVersion struct {
	Task    *task.Task
	Version int
}

// NotFound reports whether err means "no such task".
func NotFound(err error) bool { return errors.Is(err, ErrTaskNotFound) }

// VersionConflict reports whether err means "stale version".
func VersionConflict(err error) bool { return errors.Is(err, ErrVersionConflict) }
