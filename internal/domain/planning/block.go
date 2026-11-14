// Package planning owns the time-management rules of LifeOS: when work is
// scheduled, how long it takes, and when the user should be reminded.
//
// It is deliberately separate from tasks and calendar. A task has a deadline, an
// event is an appointment, and a block is the user's own working time. The
// planner combines all three; nothing is copied between them.
package planning

import (
	"errors"
	"time"
)

// Block validation errors.
var (
	// ErrInvalidBlock is returned when a block is malformed.
	ErrInvalidBlock = errors.New("block must reference a task and end after it starts")
	// ErrBlockTooShort is returned when a block is shorter than the minimum slot.
	ErrBlockTooShort = errors.New("block must be at least 5 minutes long")
	// ErrBlockTooLong is returned when a block is longer than a day.
	ErrBlockTooLong = errors.New("block must not be longer than 24 hours")
)

// Block duration bounds.
const (
	MinBlockMinutes = 5
	MaxBlockMinutes = 24 * 60
)

// BlockStatus is the lifecycle state of a planned work block.
type BlockStatus string

// Block lifecycle states.
const (
	BlockPlanned BlockStatus = "planned"
	BlockActive  BlockStatus = "active"
	BlockDone    BlockStatus = "done"
	BlockSkipped BlockStatus = "skipped"
)

// IsValid reports whether the status is known.
func (s BlockStatus) IsValid() bool {
	switch s {
	case BlockPlanned, BlockActive, BlockDone, BlockSkipped:
		return true
	default:
		return false
	}
}

// BlockSource records who created the block.
type BlockSource string

// Block sources.
const (
	// SourceAuto is a block produced by the planner and safe to move.
	SourceAuto BlockSource = "auto"
	// SourceManual is a block the user placed and the planner must not move.
	SourceManual BlockSource = "manual"
	// SourceAssistant is a block the assistant proposed and the user confirmed.
	SourceAssistant BlockSource = "assistant"
)

// IsValid reports whether the source is known.
func (s BlockSource) IsValid() bool {
	switch s {
	case SourceAuto, SourceManual, SourceAssistant:
		return true
	default:
		return false
	}
}

// Movable reports whether the planner may replace this block when replanning.
func (s BlockSource) Movable() bool { return s == SourceAuto }

// Block is a scheduled interval of work on a task.
//
// Blocks live apart from calendar events: an event is an appointment with other
// people and is never moved by LifeOS on its own, while an automatically planned
// block is the user's own time and may be rearranged by the planner.
type Block struct {
	ID        int64
	TaskID    int64
	StartsAt  time.Time // UTC
	EndsAt    time.Time // UTC
	Status    BlockStatus
	Source    BlockSource
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewBlock validates and builds a work block.
func NewBlock(taskID int64, startsAt, endsAt time.Time, status BlockStatus, source BlockSource, now time.Time) (Block, error) {
	if taskID <= 0 || startsAt.IsZero() || endsAt.IsZero() || !endsAt.After(startsAt) {
		return Block{}, ErrInvalidBlock
	}
	duration := endsAt.Sub(startsAt)
	if duration < MinBlockMinutes*time.Minute {
		return Block{}, ErrBlockTooShort
	}
	if duration > MaxBlockMinutes*time.Minute {
		return Block{}, ErrBlockTooLong
	}
	if status == "" {
		status = BlockPlanned
	}
	if !status.IsValid() {
		return Block{}, ErrInvalidBlock
	}
	if source == "" {
		source = SourceAuto
	}
	if !source.IsValid() {
		return Block{}, ErrInvalidBlock
	}
	stamp := now.UTC()
	return Block{
		TaskID:    taskID,
		StartsAt:  startsAt.UTC(),
		EndsAt:    endsAt.UTC(),
		Status:    status,
		Source:    source,
		CreatedAt: stamp,
		UpdatedAt: stamp,
	}, nil
}

// Duration returns the block length.
func (b Block) Duration() time.Duration { return b.EndsAt.Sub(b.StartsAt) }

// Overlaps reports whether two blocks share any instant.
func (b Block) Overlaps(other Block) bool {
	return b.StartsAt.Before(other.EndsAt) && other.StartsAt.Before(b.EndsAt)
}
