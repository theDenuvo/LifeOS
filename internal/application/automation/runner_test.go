package automation_test

import (
	"context"
	"testing"
	"time"

	appautomation "github.com/theDenuvo/LifeOS/internal/application/automation"
	domainautomation "github.com/theDenuvo/LifeOS/internal/domain/automation"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// appRunner builds a runner over the fake-backed service with a one-minute
// tick: tests drive the event path and recovery directly.
func appRunner(service *appautomation.Service) *appautomation.Runner {
	return appautomation.NewRunner(service, nil, time.Minute)
}

// TestRunnerDispatchesEventRuns checks the live path end to end over a real
// broker: an event on the stream starts the run without any tick.
func TestRunnerDispatchesEventRuns(t *testing.T) {
	broker := events.NewBroker()
	clock := testClock()
	repo := newFakeRepo()
	release := make(chan struct{})
	tools := &blockingTools{release: release}
	service := newTestService(clock, repo, tools, broker)
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind:  domainautomation.TriggerEvent,
		Event: &domainautomation.EventTrigger{Name: "task.completed"},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// The broker is both the publisher and the event source, as in main.
	runner := appautomation.NewRunner(service, broker, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Start(ctx)
	// The subscription is asynchronous: give the runner a moment to attach
	// before publishing, otherwise the event would be dropped by the broker.
	time.Sleep(200 * time.Millisecond)

	if err := broker.Publish("task.completed", map[string]any{}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The run starts asynchronously; poll until the reservation appears.
	deadline := time.Now().Add(5 * time.Second)
	for {
		running := false
		for _, run := range repo.snapshotRuns() {
			if run.Status == domainautomation.RunRunning {
				running = true
			}
		}
		if running || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	running := false
	for _, run := range repo.snapshotRuns() {
		if run.Status == domainautomation.RunRunning {
			running = true
		}
	}
	if !running {
		t.Fatalf("event did not start a run")
	}
	close(release)
}

// chanSource is a test event source the test publishes into with the same
// non-blocking drop semantics as the broker's subscriber buffer.
type chanSource chan events.Event

func (c chanSource) Subscribe(context.Context) <-chan events.Event { return c }

// publish mimics events.Broker.Publish: a full subscriber buffer drops the
// event silently, and the test counts the drops.
func (c chanSource) publish(eventType string, payload map[string]any, at time.Time) bool {
	select {
	case c <- events.Event{Type: eventType, Data: payload, At: at}:
		return true
	default:
		return false
	}
}

// TestSkipJournalKeepsReceiverLive pins the receive-path guarantee under an
// overflow: journaling refused launches happens off the receive loop, so the
// receiver keeps draining the source even while the skip journal is backed
// up on database writes — no event is dropped by the buffer. Eight long runs
// hold the dispatch queue; a hundred events match their triggers; every
// overflow lands in the bounded asynchronous journal.
func TestSkipJournalKeepsReceiverLive(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	repo.skipDelay = 10 * time.Millisecond
	release := make(chan struct{})
	tools := &blockingTools{release: release}
	service := newTestService(clock, repo, tools, &recordedPublisher{})

	const automations = 8
	const events = 100
	ids := make([]int64, 0, automations)
	for i := 0; i < automations; i++ {
		view := mustCreate(t, service, domainautomation.Trigger{
			Kind:  domainautomation.TriggerEvent,
			Event: &domainautomation.EventTrigger{Name: "task.completed"},
		})
		if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
			t.Fatalf("approve: %v", err)
		}
		ids = append(ids, view.Automation.ID)
	}

	source := make(chanSource, 64)
	runner := appautomation.NewRunner(service, source, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Start(ctx)

	now := clock.Now()
	// Publish in batches slower than one receive-loop pass but faster than
	// the skip journal drains: a synchronous implementation would stall the
	// receiver here and start dropping events.
	for i := 0; i < events; i++ {
		if !source.publish("task.completed", map[string]any{}, now) {
			t.Fatalf("receiver fell behind: the event buffer dropped event %d", i)
		}
		if i%20 == 19 {
			time.Sleep(60 * time.Millisecond)
		}
	}

	// Give the receiver time to process the burst before releasing the runs.
	time.Sleep(200 * time.Millisecond)

	close(release)
	// All eight dispatched runs must reach a terminal status, and the journal
	// must record at least its full capacity of refused launches.
	deadline := time.Now().Add(5 * time.Second)
	for {
		finished, skips := 0, 0
		for _, run := range repo.snapshotRuns() {
			switch run.Status {
			case domainautomation.RunSucceeded:
				finished++
			case domainautomation.RunSkipped:
				skips++
			}
		}
		if finished == automations && skips >= 64 {
			break
		}
		if time.Now().After(deadline) {
			statuses := map[string]int{}
			perAutomation := map[int64]int{}
			reasons := map[string]int{}
			for _, run := range repo.snapshotRuns() {
				statuses[string(run.Status)]++
				perAutomation[run.AutomationID]++
				if run.Error != "" {
					reasons[run.Error]++
				}
			}
			t.Fatalf("journal did not drain: %d finished runs, %d recorded skips | statuses %v | per automation %v | reasons %v",
				finished, skips, statuses, perAutomation, reasons)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSkipJournalFlushesOnShutdown pins the shutdown guarantee: refusals
// already queued are flushed even though the runner context is cancelled —
// the journal never exits with a half-drained queue and never records a
// fictitious zero-entry from a closed channel.
func TestSkipJournalFlushesOnShutdown(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	gate := make(chan struct{})
	repo.skipGate = gate
	release := make(chan struct{})
	defer close(release)
	tools := &blockingTools{release: release}
	service := newTestService(clock, repo, tools, &recordedPublisher{})

	const automations = 8
	const events = 8 // 8 dispatched + 7*8 = 56 queued refusals, inside the journal bound
	for i := 0; i < automations; i++ {
		view := mustCreate(t, service, domainautomation.Trigger{
			Kind:  domainautomation.TriggerEvent,
			Event: &domainautomation.EventTrigger{Name: "task.completed"},
		})
		if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
			t.Fatalf("approve: %v", err)
		}
	}

	source := make(chanSource, 64)
	runner := appautomation.NewRunner(service, source, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	go runner.Start(ctx)

	now := clock.Now()
	for i := 0; i < events; i++ {
		if !source.publish("task.completed", map[string]any{}, now) {
			t.Fatalf("receiver fell behind: the event buffer dropped event %d", i)
		}
	}
	// The receive loop has enqueued the refusals; the journal is frozen on
	// the gate, holding the first write.
	time.Sleep(150 * time.Millisecond)
	for _, run := range repo.snapshotRuns() {
		if run.Status == domainautomation.RunSkipped {
			t.Fatalf("a skip was recorded while the journal was frozen: %+v", run)
		}
	}

	// Shutdown: cancel the context. The journal must not abandon its queue.
	cancel()
	time.Sleep(100 * time.Millisecond)

	// Let the frozen write through and the drain finish.
	close(gate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		skips := 0
		for _, run := range repo.snapshotRuns() {
			if run.Status == domainautomation.RunSkipped {
				if run.AutomationID == 0 {
					t.Fatalf("a fictitious zero-automation skip was recorded: %+v", run)
				}
				skips++
			}
		}
		if skips == (events-1)*automations {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued refusals were not flushed on shutdown: %d recorded, want %d",
				skips, (events-1)*automations)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunnerStartWaitsForEventReceiver pins the application-level shutdown
// guarantee: Start returns only after the event receiver has finished its
// orderly shutdown, including the refusal-journal drain — a caller that
// closes the database after Start returns cannot cut the flush short.
func TestRunnerStartWaitsForEventReceiver(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	gate := make(chan struct{})
	repo.skipGate = gate
	release := make(chan struct{})
	tools := &blockingTools{release: release}
	service := newTestService(clock, repo, tools, &recordedPublisher{})

	const automations = 8
	const events = 8 // 8 dispatched + 56 queued refusals
	for i := 0; i < automations; i++ {
		view := mustCreate(t, service, domainautomation.Trigger{
			Kind:  domainautomation.TriggerEvent,
			Event: &domainautomation.EventTrigger{Name: "task.completed"},
		})
		if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
			t.Fatalf("approve: %v", err)
		}
	}

	source := make(chanSource, 64)
	runner := appautomation.NewRunner(service, source, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() {
		runner.Start(ctx)
		close(started)
	}()

	now := clock.Now()
	for i := 0; i < events; i++ {
		if !source.publish("task.completed", map[string]any{}, now) {
			t.Fatalf("receiver fell behind: the event buffer dropped event %d", i)
		}
	}
	// The queue holds refusals; the journal is frozen on the gate.
	time.Sleep(150 * time.Millisecond)

	// Shutdown: Start must not return while the drain is still pending.
	cancel()
	select {
	case <-started:
		t.Fatal("Start returned while the refusal journal was still draining")
	case <-time.After(300 * time.Millisecond):
	}

	// Release the in-flight runs and let the drain finish; now Start returns.
	close(release)
	close(gate)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the journal drained")
	}
	skips := 0
	for _, run := range repo.snapshotRuns() {
		if run.Status == domainautomation.RunSkipped {
			if run.AutomationID == 0 {
				t.Fatalf("a fictitious zero-automation skip was recorded: %+v", run)
			}
			skips++
		}
	}
	if skips != (events-1)*automations {
		t.Fatalf("queued refusals were not flushed before Start returned: %d recorded, want %d",
			skips, (events-1)*automations)
	}
}

// TestSkipJournalDrainIsBounded pins the shutdown arithmetic: after the
// receive loop stops, the whole drain is bounded by maxDrainTimeout plus
// one in-flight write — slow writes cannot stretch it one entry at a time
// beyond the bound the application wait in main is sized against.
func TestSkipJournalDrainIsBounded(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	repo.skipDelay = 300 * time.Millisecond // 56 queued writes would take ~17s unbounded
	release := make(chan struct{})
	tools := &blockingTools{release: release}
	service := newTestService(clock, repo, tools, &recordedPublisher{})

	const automations = 8
	const events = 8 // 8 dispatched + 56 queued refusals
	for i := 0; i < automations; i++ {
		view := mustCreate(t, service, domainautomation.Trigger{
			Kind:  domainautomation.TriggerEvent,
			Event: &domainautomation.EventTrigger{Name: "task.completed"},
		})
		if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
			t.Fatalf("approve: %v", err)
		}
	}

	source := make(chanSource, 64)
	runner := appautomation.NewRunner(service, source, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() {
		runner.Start(ctx)
		close(started)
	}()

	now := clock.Now()
	for i := 0; i < events; i++ {
		if !source.publish("task.completed", map[string]any{}, now) {
			t.Fatalf("receiver fell behind: the event buffer dropped event %d", i)
		}
	}
	time.Sleep(300 * time.Millisecond) // some writes land, most stay queued

	// The runs are released first, so the measurement covers the journal
	// drain and not the bounded wait for in-flight work.
	close(release)
	shutdownAt := time.Now()
	cancel()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return within the drain bound")
	}
	elapsed := time.Since(shutdownAt)
	// Unbounded, the queue would drain at 300ms per entry for ~17 seconds.
	// Bounded, the drain stops after maxDrainTimeout plus one in-flight
	// write; allow the test scheduler some slack.
	if elapsed > 9*time.Second {
		t.Fatalf("drain was not bounded: Start returned after %s", elapsed)
	}
	for _, run := range repo.snapshotRuns() {
		if run.AutomationID == 0 {
			t.Fatalf("a fictitious zero-automation skip was recorded: %+v", run)
		}
	}
}

// TestRunnerStartWaitsForEventWorkers pins the guarantee the shutdown relies
// on: a scenario already executing keeps the runner alive until it finishes
// its steps and journals the run — Start cannot return, and therefore the
// database cannot close, while a run is still writing.
func TestRunnerStartWaitsForEventWorkers(t *testing.T) {
	clock := testClock()
	repo := newFakeRepo()
	release := make(chan struct{})
	tools := &blockingTools{release: release}
	service := newTestService(clock, repo, tools, &recordedPublisher{})
	view := mustCreate(t, service, domainautomation.Trigger{
		Kind:  domainautomation.TriggerEvent,
		Event: &domainautomation.EventTrigger{Name: "task.completed"},
	})
	if _, err := service.Approve(context.Background(), view.Automation.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	source := make(chanSource, 8)
	runner := appautomation.NewRunner(service, source, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() {
		runner.Start(ctx)
		close(started)
	}()

	if !source.publish("task.completed", map[string]any{}, clock.Now()) {
		t.Fatal("publish refused")
	}
	// Wait for the run to actually be executing, then shut down under it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		running := false
		for _, run := range repo.snapshotRuns() {
			if run.Status == domainautomation.RunRunning {
				running = true
			}
		}
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the event run never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-started:
		t.Fatal("Start returned while an event run was still executing")
	case <-time.After(500 * time.Millisecond):
	}

	close(release)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the in-flight run finished")
	}

	// The run reached a terminal state and the reservation is released: no
	// write is left pending behind the runner's back.
	runs, total, err := service.Runs(context.Background(), view.Automation.ID, 10, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if total != 1 || !runs[0].Status.Terminal() {
		t.Fatalf("in-flight run was not finished before Start returned: %+v", runs)
	}
}
