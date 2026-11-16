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
