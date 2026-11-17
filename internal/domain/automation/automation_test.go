package automation

import "testing"

// TestEventTriggerRejectsRunLifecycleEvents pins the structural cycle guard:
// the service publishes run-lifecycle events for every run — including a run
// refused for exceeding the chain depth — so a user trigger listening to
// them would feed the refusal back into the same automation forever.
func TestEventTriggerRejectsRunLifecycleEvents(t *testing.T) {
	for _, name := range []string{"automation.run.started", "automation.run.finished"} {
		if err := (EventTrigger{Name: name}).Validate(); err == nil {
			t.Fatalf("run-lifecycle event %q must not be a valid trigger", name)
		}
		if !IsReservedRunEvent(name) {
			t.Fatalf("%q must be reported as reserved", name)
		}
	}
	for _, name := range []string{"task.completed", "goal.updated", "note.created"} {
		if err := (EventTrigger{Name: name}).Validate(); err != nil {
			t.Fatalf("event %q must stay a valid trigger: %v", name, err)
		}
	}
}
