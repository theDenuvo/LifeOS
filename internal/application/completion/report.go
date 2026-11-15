// Package completion checks whether the expected result of a task can be seen in
// the data LifeOS actually has access to.
//
// The rule is deliberately conservative: a conclusion is drawn only from sources
// that really exist, the evidence is kept with the conclusion, and the task status
// is never changed on a guess. A source that is not configured is reported as
// unavailable instead of being silently treated as "no evidence" or, worse, as
// confirmation.
package completion

import (
	"context"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// Verdict is the conclusion of one check.
type Verdict string

// Verdicts.
const (
	// VerdictConfirmed means sufficient evidence was found in an available source.
	VerdictConfirmed Verdict = "confirmed"
	// VerdictInsufficient means nothing convincing was found.
	VerdictInsufficient Verdict = "insufficient"
	// VerdictContradictory means sources disagree with each other.
	VerdictContradictory Verdict = "contradictory"
	// VerdictAlreadyCompleted means the task was already finished before the check.
	VerdictAlreadyCompleted Verdict = "already_completed"
)

// IsValid reports whether the verdict is known.
func (v Verdict) IsValid() bool {
	switch v {
	case VerdictConfirmed, VerdictInsufficient, VerdictContradictory, VerdictAlreadyCompleted:
		return true
	default:
		return false
	}
}

// Evidence is one observation that supports or contradicts completion.
type Evidence struct {
	Source     string    `json:"source"`
	Reference  string    `json:"reference"`
	Snippet    string    `json:"snippet,omitempty"`
	At         time.Time `json:"at"`
	Confidence float64   `json:"confidence"`
	Against    bool      `json:"against,omitempty"`
}

// SourceStatus reports whether a source could be consulted at all.
type SourceStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Report is the recorded result of a check, including the grounds for it.
type Report struct {
	ID         int64          `json:"id,omitempty"`
	TaskID     int64          `json:"task_id"`
	Title      string         `json:"title"`
	Expected   string         `json:"expected_result,omitempty"`
	Verdict    Verdict        `json:"verdict"`
	Confidence float64        `json:"confidence"`
	Reason     string         `json:"reason"`
	Evidence   []Evidence     `json:"evidence"`
	Sources    []SourceStatus `json:"sources"`
	Decision   string         `json:"decision"`
	CheckedAt  time.Time      `json:"checked_at"`
}

// Confirms reports whether the evidence is strong enough to act on.
func (r Report) Confirms() bool {
	return r.Verdict == VerdictConfirmed && r.Confidence >= ConfirmedConfidence
}

// ConfirmedConfidence is the threshold above which completion may be confirmed.
const ConfirmedConfidence = 0.7

// Source is one place where completion can be observed.
type Source interface {
	// Name identifies the source in the report.
	Name() string
	// Status reports whether the source can be consulted at all.
	Status(ctx context.Context) SourceStatus
	// Evidence collects observations about a task.
	Evidence(ctx context.Context, value *task.Task, expected string) ([]Evidence, error)
}
