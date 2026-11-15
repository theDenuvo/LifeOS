package goals

import (
	"context"
	"fmt"
	"strings"

	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
)

// ContextSnapshot renders the durable goal picture for the assistant's
// automatic context. It is appended to the system prompt like the memory
// snapshot: a hint about long-term direction, not a source of truth.
func (s *Service) ContextSnapshot(ctx context.Context) (string, error) {
	stats, err := s.Stats(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Цели: предложено %d (ждут утверждения), в работе %d, на паузе %d, достигнуто %d.",
		stats.Proposed, stats.Active, stats.Paused, stats.Achieved)

	for _, status := range []domaingoal.Status{domaingoal.StatusProposed, domaingoal.StatusActive} {
		views, _, err := s.List(ctx, Filter{Status: &status, Limit: 10})
		if err != nil {
			// A failure to list goals must not hide the rest of the snapshot.
			break
		}
		for _, view := range views {
			if line := goalLine(view); line != "" {
				b.WriteString("\n")
				b.WriteString(line)
			}
		}
	}
	if stalled, err := s.Stalled(ctx); err == nil && len(stalled) > 0 {
		fmt.Fprintf(&b, "\nОстановившиеся цели: %d — спроси пользователя, что происходит, и предложи скорректировать план.", len(stalled))
	}
	return b.String(), nil
}

// goalLine renders one goal in a single bounded line.
func goalLine(view *View) string {
	if view == nil || view.Goal == nil || view.Review == nil {
		return ""
	}
	g := view.Goal
	var b strings.Builder
	fmt.Fprintf(&b, "Цель #%d «%s» (%s)", g.ID, truncateRunes(g.Title, 80), g.Status)
	if view.Review.Progress.Percent != nil {
		fmt.Fprintf(&b, ", прогресс %d%%", *view.Review.Progress.Percent)
	}
	if g.TargetDate != nil {
		fmt.Fprintf(&b, ", срок до %s", g.TargetDate.Format("2006-01-02"))
	}
	signals := view.Review.Signals
	if signals.Stalled {
		fmt.Fprintf(&b, "; стоит: %s", signals.StallReason)
	}
	if signals.Overdue {
		b.WriteString("; срок истёк")
	}
	if signals.PendingProposal {
		b.WriteString("; есть предложение по плану")
	}
	return b.String()
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
