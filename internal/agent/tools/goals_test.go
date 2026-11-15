package tools

import (
	"testing"
)

// TestParseOptionalGoalDateRejectsGarbage covers the review finding that a
// malformed but non-empty milestone deadline was silently treated as "no
// deadline", quietly dropping a date the user or the model asked for.
func TestParseOptionalGoalDateRejectsGarbage(t *testing.T) {
	for _, value := range []string{"06/2027", "2027-13-01", "завтра", "2027-1-5"} {
		if _, err := parseOptionalGoalDate(value); err == nil {
			t.Fatalf("parseOptionalGoalDate(%q) must fail, got no error", value)
		}
	}
}

// TestParseOptionalGoalDateAcceptsAbsentAndValid keeps the two legitimate
// cases working: no deadline at all, and a real calendar date.
func TestParseOptionalGoalDateAcceptsAbsentAndValid(t *testing.T) {
	due, err := parseOptionalGoalDate("   ")
	if err != nil {
		t.Fatalf("empty date must be accepted as no deadline, got %v", err)
	}
	if due != nil {
		t.Fatalf("empty date produced %v, want nil", due)
	}

	due, err = parseOptionalGoalDate("2027-06-01")
	if err != nil {
		t.Fatalf("valid date: %v", err)
	}
	if due == nil || due.Format("2006-01-02") != "2027-06-01" {
		t.Fatalf("due = %v, want 2027-06-01", due)
	}
}

// TestBuildCreateInputRejectsBadMilestoneDate makes sure the error reaches the
// tool call instead of being swallowed one level up.
func TestBuildCreateInputRejectsBadMilestoneDate(t *testing.T) {
	_, err := buildCreateInput("Цель", "", "", []goalMilestoneArg{
		{Title: "Этап", DueDate: "31.12.2027"},
	})
	if err == nil {
		t.Fatal("buildCreateInput must propagate a malformed milestone date")
	}
	if _, err := buildCreateInput("Цель", "", "", []goalMilestoneArg{{Title: "Этап"}}); err != nil {
		t.Fatalf("milestone without a date must be accepted, got %v", err)
	}
}
