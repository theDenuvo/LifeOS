package assistant

import (
	"context"
	"fmt"
	"strings"
	"time"

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/application/proactive"
	"github.com/theDenuvo/LifeOS/internal/application/summary"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
)

// TodayProvider is the slice of the summary service the assistant context needs.
type TodayProvider interface {
	Today(ctx context.Context) (summary.Summary, error)
}

// InsightProvider is the slice of the proactive service the assistant context needs.
type InsightProvider interface {
	Compute(ctx context.Context) ([]proactive.Insight, error)
}

// EventProvider is the slice of the calendar service the context needs.
type EventProvider interface {
	ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]appevents.EventWithVersion, error)
}

// MemoryProvider renders known personal facts and pending contradictions.
type MemoryProvider interface {
	ContextSnapshot(ctx context.Context) (string, error)
}

// GoalsProvider renders the durable goal picture: what the user is moving
// toward, what progress is factual and which goals stopped moving.
type GoalsProvider interface {
	ContextSnapshot(ctx context.Context) (string, error)
}

// ContextProvider renders a compact snapshot of the user's current LifeOS state.
//
// The snapshot is injected into the system prompt on every turn. It is a hint,
// not a source of truth: the model must still use tools for anything it is
// about to change or quote precisely.
type ContextProvider interface {
	Snapshot(ctx context.Context) (string, error)
}

// LifeOSContext builds the assistant's automatic context from read models.
type LifeOSContext struct {
	today    TodayProvider
	insights InsightProvider
	events   EventProvider
	memory   MemoryProvider
	goals    GoalsProvider
	timezone string
	clock    Clock
}

// ContextOption customises the automatic context.
type ContextOption func(*LifeOSContext)

// WithEvents adds today's calendar events to the snapshot.
func WithEvents(provider EventProvider) ContextOption {
	return func(c *LifeOSContext) { c.events = provider }
}

// WithMemory adds the durable personal memory to the snapshot. Without it the
// assistant would only know the current day, which is what stage 2 fixes.
func WithMemory(provider MemoryProvider) ContextOption {
	return func(c *LifeOSContext) { c.memory = provider }
}

// WithGoals adds the long-term goal picture to the snapshot. Without it the
// assistant would not know where the user is headed, which is what stage 4
// fixes.
func WithGoals(provider GoalsProvider) ContextOption {
	return func(c *LifeOSContext) { c.goals = provider }
}

// NewLifeOSContext builds a context provider.
func NewLifeOSContext(today TodayProvider, insights InsightProvider, timezone string, clock Clock, opts ...ContextOption) *LifeOSContext {
	if clock == nil {
		clock = SystemClock()
	}
	if timezone == "" {
		timezone = "Europe/Moscow"
	}
	c := &LifeOSContext{today: today, insights: insights, timezone: timezone, clock: clock}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Snapshot renders the current state in a bounded, human-readable form.
func (c *LifeOSContext) Snapshot(ctx context.Context) (string, error) {
	now := c.clock.Now()
	loc, err := time.LoadLocation(c.timezone)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)

	today, err := c.today.Today(ctx)
	if err != nil {
		return "", err
	}
	insights, err := c.insights.Compute(ctx)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Локальное время: %s (%s, %s).\n",
		local.Format("2006-01-02 15:04"), c.timezone, weekdayRU(local.Weekday()))
	fmt.Fprintf(&b, "Задачи: активных %d, на сегодня %d, просрочено %d, высокий приоритет %d, выполнено сегодня %d.\n",
		today.Stats.ActiveTotal,
		len(today.Today),
		today.Stats.OverdueCount,
		today.Stats.HighPriorityCount,
		today.Stats.CompletedToday,
	)
	fmt.Fprintf(&b, "Входящие: %d необработанных.\n", today.Stats.InboxCount)

	if c.events != nil {
		dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		dayEnd := dayStart.AddDate(0, 0, 1)
		if events, err := c.events.ListRange(ctx, dayStart, dayEnd, nil); err == nil && len(events) > 0 {
			fmt.Fprintf(&b, "События сегодня: %s.\n", joinEvents(events, 5, loc))
		}
	}

	if today.FocusTask != nil {
		fmt.Fprintf(&b, "Фокус: «%s» (%s, %s).\n",
			truncateRunes(today.FocusTask.Title, 80),
			priorityRU(today.FocusTask.Priority),
			timeOrDash(today.FocusTask.Time),
		)
	}
	if len(today.Today) > 0 {
		fmt.Fprintf(&b, "Сегодня: %s.\n", joinToday(today.Today, 5))
	}
	if len(today.Overdue) > 0 {
		fmt.Fprintf(&b, "Просрочено: %s.\n", joinOverdue(today.Overdue, 5))
	}
	if len(insights) > 0 {
		fmt.Fprintf(&b, "Наблюдения: %s.\n", joinInsights(insights, 4))
	}
	// Memory is appended last: it is durable context, not today's state, and a
	// failure to render it must not hide the day's snapshot.
	if c.memory != nil {
		if value, err := c.memory.ContextSnapshot(ctx); err == nil && strings.TrimSpace(value) != "" {
			b.WriteString("\n")
			b.WriteString(value)
		}
	}
	// Goals come after memory: both are durable context. A goal failure must
	// not hide the rest of the snapshot either.
	if c.goals != nil {
		if value, err := c.goals.ContextSnapshot(ctx); err == nil && strings.TrimSpace(value) != "" {
			b.WriteString("\n\n")
			b.WriteString(value)
		}
	}
	return b.String(), nil
}

func joinToday(items []summary.TodayItem, limit int) string {
	parts := make([]string, 0, min(limit, len(items)))
	for i, item := range items {
		if i >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("«%s» (%s, %s)",
			truncateRunes(item.Title, 60), priorityRU(item.Priority), timeOrDash(item.Time)))
	}
	if len(items) > limit {
		parts = append(parts, fmt.Sprintf("ещё %d", len(items)-limit))
	}
	return strings.Join(parts, "; ")
}

func joinOverdue(items []summary.OverdueItem, limit int) string {
	parts := make([]string, 0, min(limit, len(items)))
	for i, item := range items {
		if i >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("«%s» (%d дн.)",
			truncateRunes(item.Title, 60), item.DaysOverdue))
	}
	if len(items) > limit {
		parts = append(parts, fmt.Sprintf("ещё %d", len(items)-limit))
	}
	return strings.Join(parts, "; ")
}

func joinEvents(items []appevents.EventWithVersion, limit int, loc *time.Location) string {
	parts := make([]string, 0, min(limit, len(items)))
	for i, item := range items {
		if i >= limit {
			break
		}
		value := item.Event
		parts = append(parts, fmt.Sprintf("«%s» (%s–%s)",
			truncateRunes(value.Title, 60),
			value.StartsAt.In(loc).Format("15:04"),
			value.EndsAt.In(loc).Format("15:04"),
		))
	}
	if len(items) > limit {
		parts = append(parts, fmt.Sprintf("ещё %d", len(items)-limit))
	}
	return strings.Join(parts, "; ")
}

func joinInsights(items []proactive.Insight, limit int) string {
	parts := make([]string, 0, min(limit, len(items)))
	for i, item := range items {
		if i >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("[%s] %s — %s",
			item.Severity, truncateRunes(item.Title, 60), truncateRunes(item.Detail, 100)))
	}
	if len(items) > limit {
		parts = append(parts, fmt.Sprintf("ещё %d", len(items)-limit))
	}
	return strings.Join(parts, "; ")
}

func timeOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "без времени"
	}
	return value
}

func priorityRU(value string) string {
	switch value {
	case "high":
		return "высокий приоритет"
	case "low":
		return "низкий приоритет"
	default:
		return "средний приоритет"
	}
}

func weekdayRU(day time.Weekday) string {
	switch day {
	case time.Monday:
		return "понедельник"
	case time.Tuesday:
		return "вторник"
	case time.Wednesday:
		return "среда"
	case time.Thursday:
		return "четверг"
	case time.Friday:
		return "пятница"
	case time.Saturday:
		return "суббота"
	default:
		return "воскресенье"
	}
}
