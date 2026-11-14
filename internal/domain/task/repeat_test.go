package task

import (
	"encoding/json"
	"testing"
	"time"
)

// testNow is the fixed instant used by the domain tests.
func testNow() time.Time { return time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC) }

func mustDate(t *testing.T, value string) CalendarDate {
	t.Helper()
	date, err := ParseCalendarDate(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return date
}

func TestRepeatNext(t *testing.T) {
	anchor := mustDate(t, "2026-11-02") // Monday

	cases := []struct {
		name   string
		repeat Repeat
		want   string
	}{
		{"daily", Repeat{Freq: RepeatDaily, Interval: 1}, "2026-11-03"},
		{"every third day", Repeat{Freq: RepeatDaily, Interval: 3}, "2026-11-05"},
		{"weekly same weekday", Repeat{Freq: RepeatWeekly, Interval: 1}, "2026-11-09"},
		{"every second week", Repeat{Freq: RepeatWeekly, Interval: 2}, "2026-11-16"},
		{"weekly weekdays", Repeat{Freq: RepeatWeekly, Interval: 1, Weekdays: []int{3, 5}}, "2026-11-04"},
		{"monthly", Repeat{Freq: RepeatMonthly, Interval: 1}, "2026-12-02"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			next, err := test.repeat.Next(anchor)
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if got := next.String(); got != test.want {
				t.Errorf("next = %s, want %s", got, test.want)
			}
		})
	}
}

func TestRepeatMonthlyClampsToLastDay(t *testing.T) {
	repeat := Repeat{Freq: RepeatMonthly, Interval: 1}
	next, err := repeat.Next(mustDate(t, "2026-01-31"))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got := next.String(); got != "2026-02-28" {
		t.Errorf("31 January + 1 month = %s, want 2026-02-28", got)
	}

	next, err = repeat.Next(mustDate(t, "2028-01-31"))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got := next.String(); got != "2028-02-29" {
		t.Errorf("31 January + 1 month in a leap year = %s, want 2028-02-29", got)
	}
}

func TestRepeatStopsAfterUntil(t *testing.T) {
	until := mustDate(t, "2026-11-05")
	repeat := Repeat{Freq: RepeatDaily, Interval: 1, Until: &until}

	next, err := repeat.Next(mustDate(t, "2026-11-04"))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got := next.String(); got != "2026-11-05" {
		t.Errorf("next = %s, want the last allowed date 2026-11-05", got)
	}

	next, err = repeat.Next(mustDate(t, "2026-11-05"))
	if err != nil {
		t.Fatalf("Next past the end: %v", err)
	}
	if !next.IsZero() {
		t.Errorf("series past until produced %s, want no occurrence", next.String())
	}
}

func TestRepeatValidation(t *testing.T) {
	cases := []struct {
		name   string
		repeat Repeat
		ok     bool
	}{
		{"valid daily", Repeat{Freq: RepeatDaily, Interval: 1}, true},
		{"unknown freq", Repeat{Freq: "hourly", Interval: 1}, false},
		{"zero interval", Repeat{Freq: RepeatDaily, Interval: 0}, false},
		{"too large interval", Repeat{Freq: RepeatDaily, Interval: 24}, false},
		{"weekdays on daily", Repeat{Freq: RepeatDaily, Interval: 1, Weekdays: []int{1}}, false},
		{"weekday out of range", Repeat{Freq: RepeatWeekly, Interval: 1, Weekdays: []int{8}}, false},
		{"repeated weekday", Repeat{Freq: RepeatWeekly, Interval: 1, Weekdays: []int{1, 1}}, false},
		{"weekdays with interval 2", Repeat{Freq: RepeatWeekly, Interval: 2, Weekdays: []int{1}}, false},
		{"window too large", Repeat{Freq: RepeatDaily, Interval: 1, WindowDays: 9}, false},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := test.repeat.Validate()
			if test.ok && err != nil {
				t.Fatalf("valid rule rejected: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
}

func TestRepeatJSONRoundTrip(t *testing.T) {
	until := mustDate(t, "2026-12-31")
	original := Repeat{Freq: RepeatWeekly, Interval: 1, Weekdays: []int{1, 3}, Until: &until, WindowDays: 2}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Repeat
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Freq != original.Freq || decoded.Interval != original.Interval || decoded.WindowDays != original.WindowDays {
		t.Fatalf("round trip changed the rule: %+v", decoded)
	}
	if decoded.Until == nil || decoded.Until.String() != "2026-12-31" {
		t.Fatalf("until lost: %+v", decoded.Until)
	}
	if len(decoded.Weekdays) != 2 || decoded.Weekdays[0] != 1 || decoded.Weekdays[1] != 3 {
		t.Fatalf("weekdays lost: %+v", decoded.Weekdays)
	}
}

func TestEstimateValidation(t *testing.T) {
	value, err := NewTask("Позвонить в банк", PriorityMedium, "Europe/Moscow", testNow())
	if err != nil {
		t.Fatalf("new task: %v", err)
	}

	if err := value.Update(testNow(), WithEstimate(45)); err != nil {
		t.Fatalf("valid estimate rejected: %v", err)
	}
	if value.EstimateMinutes == nil || *value.EstimateMinutes != 45 {
		t.Fatalf("estimate not stored: %+v", value.EstimateMinutes)
	}

	for _, invalid := range []int{1, 2000} {
		if err := value.Update(testNow(), WithEstimate(invalid)); err == nil {
			t.Errorf("estimate %d accepted, want rejection", invalid)
		}
	}

	// Zero clears the estimate: an unknown duration is a normal state.
	if err := value.Update(testNow(), WithEstimate(0)); err != nil {
		t.Fatalf("clearing estimate failed: %v", err)
	}
	if value.EstimateMinutes != nil {
		t.Fatalf("estimate not cleared: %+v", *value.EstimateMinutes)
	}
}

func TestRepeatDescribe(t *testing.T) {
	cases := map[string]Repeat{
		"каждый день":    {Freq: RepeatDaily, Interval: 1},
		"каждые 3 дн.":   {Freq: RepeatDaily, Interval: 3},
		"каждую неделю":  {Freq: RepeatWeekly, Interval: 1},
		"по пн, ср и пт": {Freq: RepeatWeekly, Interval: 1, Weekdays: []int{1, 3, 5}},
		"каждый месяц":   {Freq: RepeatMonthly, Interval: 1},
	}

	for want, repeat := range cases {
		if got := repeat.Describe(); got != want {
			t.Errorf("Describe() = %q, want %q", got, want)
		}
	}
}
