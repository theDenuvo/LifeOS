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
		for _, run := range repo.runs {
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
	for _, run := range repo.runs {
		if run.Status == domainautomation.RunRunning {
			running = true
		}
	}
	if !running {
		t.Fatalf("event did not start a run: %+v", repo.runs)
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
		for _, run := range repo.runs {
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
			for _, run := range repo.runs {
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
