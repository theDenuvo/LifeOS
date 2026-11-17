package automation

import (
	"testing"
	"time"

	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
)

// TestRunnerDepthAttribution pins the chain guard: an event published while a
// run executes is one hop deeper than that run; events with no covering
// window are direct causes; overlapping runs take the maximum.
func TestRunnerDepthAttribution(t *testing.T) {
	runner := NewRunner(nil, nil, time.Minute)
	base := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

	runner.remember(runWindow{start: base, depth: 1})
	runner.remember(runWindow{start: base.Add(-time.Hour), depth: 0, finish: base.Add(-30 * time.Minute)})

	if got := runner.depthFor(base.Add(time.Second)); got != 2 {
		t.Fatalf("event inside a depth-1 run window: depth = %d, want 2", got)
	}
	if got := runner.depthFor(base.Add(-time.Hour)); got != 1 {
		t.Fatalf("event inside a finished depth-0 window: depth = %d, want 1", got)
	}
	if got := runner.depthFor(base.Add(-2 * time.Hour)); got != 0 {
		t.Fatalf("event outside every window: depth = %d, want 0", got)
	}
}

// TestEventCacheMatchesAndRefreshes pins the receive-path design: matching is
// in-memory (a burst cannot outrun it with a database query per event), an
// empty cache matches nothing, and a replacement atomically swaps the
// subscriptions.
func TestEventCacheMatchesAndRefreshes(t *testing.T) {
	var cache eventCache
	if got := cache.match("task.completed", map[string]any{}); len(got) != 0 {
		t.Fatalf("an unloaded cache must match nothing, got %v", got)
	}

	automation := &domainautomation.Automation{
		ID: 7, Status: domainautomation.StatusActive,
		Trigger: domainautomation.Trigger{
			Kind: domainautomation.TriggerEvent,
			Event: &domainautomation.EventTrigger{
				Name:  "task.completed",
				Match: map[string]string{"source": "user"},
			},
		},
	}
	cache.replace([]*domainautomation.Automation{automation})

	if got := cache.match("task.completed", map[string]any{"source": "user"}); len(got) != 1 || got[0] != 7 {
		t.Fatalf("matching payload: got %v, want [7]", got)
	}
	if got := cache.match("task.completed", map[string]any{"source": "assistant"}); len(got) != 0 {
		t.Fatalf("non-matching payload: got %v, want none", got)
	}
	if got := cache.match("task.created", map[string]any{}); len(got) != 0 {
		t.Fatalf("other event type: got %v, want none", got)
	}

	// A paused automation leaves the cache.
	automation.Status = domainautomation.StatusPaused
	cache.replace([]*domainautomation.Automation{automation})
	if got := cache.match("task.completed", map[string]any{"source": "user"}); len(got) != 0 {
		t.Fatalf("paused automation must not match, got %v", got)
	}
}

// TestEventCacheIgnoresReservedRunEvents is the runtime braces to the trigger
// validation: even a hand-written row with a run-lifecycle trigger never
// fires an automation, so the refusal chain cannot loop.
func TestEventCacheIgnoresReservedRunEvents(t *testing.T) {
	var cache eventCache
	reserved := &domainautomation.Automation{
		ID: 9, Status: domainautomation.StatusActive,
		Trigger: domainautomation.Trigger{
			Kind:  domainautomation.TriggerEvent,
			Event: &domainautomation.EventTrigger{Name: "automation.run.finished"},
		},
	}
	cache.replace([]*domainautomation.Automation{reserved})
	if got := cache.match("automation.run.finished", map[string]any{}); len(got) != 0 {
		t.Fatalf("reserved run event must never match, got %v", got)
	}
}
