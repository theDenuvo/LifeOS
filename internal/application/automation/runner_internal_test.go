package automation

import (
	"testing"
	"time"
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
