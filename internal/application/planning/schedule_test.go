package planning

import (
	"testing"
	"time"

	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func TestFreeSlotsRespectsBufferAndMinimum(t *testing.T) {
	window := Interval{Start: at(t, "2026-11-10T06:00:00Z"), End: at(t, "2026-11-10T16:00:00Z")} // 09:00-19:00 MSK
	busy := []Busy{
		{Interval: Interval{Start: at(t, "2026-11-10T07:00:00Z"), End: at(t, "2026-11-10T08:00:00Z")}, Label: "встреча"},
		{Interval: Interval{Start: at(t, "2026-11-10T07:30:00Z"), End: at(t, "2026-11-10T08:30:00Z")}, Label: "перекрытие"},
	}

	slots := FreeSlots(window, busy, 10*time.Minute, 15*time.Minute)

	// 09:00-10:30 busy merges into one range; buffer trims both free sides.
	if len(slots) != 2 {
		t.Fatalf("got %d free slots, want 2: %+v", len(slots), slots)
	}
	if want := at(t, "2026-11-10T06:00:00Z").Add(10 * time.Minute); !slots[0].Start.Equal(want) {
		t.Errorf("first slot starts at %s, want %s", slots[0].Start, want)
	}
	if want := at(t, "2026-11-10T07:00:00Z").Add(-10 * time.Minute); !slots[0].End.Equal(want) {
		t.Errorf("first slot ends at %s, want %s", slots[0].End, want)
	}
	if want := at(t, "2026-11-10T08:30:00Z").Add(10 * time.Minute); !slots[1].Start.Equal(want) {
		t.Errorf("second slot starts at %s, want %s", slots[1].Start, want)
	}
}

func TestFreeSlotsDropsFragments(t *testing.T) {
	window := Interval{Start: at(t, "2026-11-10T06:00:00Z"), End: at(t, "2026-11-10T16:00:00Z")}
	busy := []Busy{
		{Interval: Interval{Start: at(t, "2026-11-10T06:00:00Z"), End: at(t, "2026-11-10T09:50:00Z")}},
		{Interval: Interval{Start: at(t, "2026-11-10T10:00:00Z"), End: at(t, "2026-11-10T16:00:00Z")}},
	}

	slots := FreeSlots(window, busy, 10*time.Minute, 15*time.Minute)
	if len(slots) != 0 {
		t.Fatalf("a ten-minute gap between two meetings produced %+v, want nothing", slots)
	}
}

func TestScheduleOrderAndBuffer(t *testing.T) {
	free := []Interval{{Start: at(t, "2026-11-10T06:00:00Z"), End: at(t, "2026-11-10T09:00:00Z")}}
	candidates := []Candidate{
		{TaskID: 1, Title: "позже", Priority: task.PriorityLow, Estimate: 30 * time.Minute, Urgency: 4},
		{TaskID: 2, Title: "просрочено", Priority: task.PriorityMedium, Estimate: 45 * time.Minute, Urgency: 0},
	}

	placements, unplaced := Schedule(free, candidates, 10*time.Minute)
	if len(unplaced) != 0 {
		t.Fatalf("unexpected unplaced: %+v", unplaced)
	}
	if len(placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(placements))
	}
	if placements[0].TaskID != 2 {
		t.Errorf("first placement is task %d, want the overdue one", placements[0].TaskID)
	}
	if want := placements[0].End.Add(10 * time.Minute); !placements[1].Start.Equal(want) {
		t.Errorf("second placement starts at %s, want a buffer of 10 minutes after %s", placements[1].Start, placements[0].End)
	}
}

func TestScheduleReportsReasonWhenNothingFits(t *testing.T) {
	free := []Interval{{Start: at(t, "2026-11-10T06:00:00Z"), End: at(t, "2026-11-10T06:30:00Z")}}
	candidates := []Candidate{{TaskID: 3, Title: "большая", Estimate: 90 * time.Minute, Urgency: 1}}

	placements, unplaced := Schedule(free, candidates, 0)
	if len(placements) != 0 {
		t.Fatalf("a 90-minute task was placed into a 30-minute slot: %+v", placements)
	}
	if len(unplaced) != 1 || unplaced[0].Reason == "" {
		t.Fatalf("unplaced = %+v, want one explained entry", unplaced)
	}
}

func TestPreferencesWindowRespectsWorkingDays(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	prefs := Preferences{Start: 9 * time.Hour, End: 19 * time.Hour, Days: []int{1, 2, 3, 4, 5}}

	monday, _ := task.ParseCalendarDate("2026-11-09")
	window, ok := prefs.WindowOn(monday, loc)
	if !ok {
		t.Fatal("Monday rejected as a working day")
	}
	if got := window.Duration(); got != 10*time.Hour {
		t.Errorf("window = %s, want 10h", got)
	}

	saturday, _ := task.ParseCalendarDate("2026-11-14")
	if _, ok := prefs.WindowOn(saturday, loc); ok {
		t.Fatal("Saturday accepted as a working day")
	}
}

func TestClipFromDropsPassedTime(t *testing.T) {
	base := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	slots := []Interval{
		{Start: base, End: base.Add(time.Hour)},
		{Start: base.Add(2 * time.Hour), End: base.Add(3 * time.Hour)},
	}
	clipped := ClipFrom(slots, base.Add(30*time.Minute))
	if len(clipped) != 2 {
		t.Fatalf("clipped = %+v, want two intervals", clipped)
	}
	if !clipped[0].Start.Equal(base.Add(30 * time.Minute)) {
		t.Errorf("first interval starts at %s, want the boundary", clipped[0].Start)
	}
	if !clipped[0].End.Equal(base.Add(time.Hour)) {
		t.Errorf("first interval ends at %s, want it unchanged", clipped[0].End)
	}

	// Everything that already passed disappears instead of becoming a zero-length
	// slot the planner could mistake for free time.
	past := ClipFrom(slots, base.Add(4*time.Hour))
	if len(past) != 0 {
		t.Fatalf("clipped = %+v, want nothing left", past)
	}
}

func TestScheduleStopsAtAnExactDeadline(t *testing.T) {
	base := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	slot := []Interval{{Start: base, End: base.Add(8 * time.Hour)}}
	deadline := base.Add(2 * time.Hour) // 11:00

	// Three hours of work cannot end by 11:00, so nothing may be placed.
	placements, unplaced := Schedule(slot, []Candidate{{
		TaskID:        1,
		Title:         "Долгая работа",
		Estimate:      3 * time.Hour,
		HardDeadline:  &deadline,
		DeadlineLabel: "дедлайна 11:00",
	}}, 10*time.Minute)
	if len(placements) != 0 {
		t.Fatalf("placed %+v, want nothing before the deadline", placements)
	}
	if len(unplaced) != 1 {
		t.Fatalf("unplaced = %+v, want an explanation", unplaced)
	}
	if unplaced[0].Reason == "" {
		t.Error("the reason is empty")
	}

	// One hour fits: 10:00 is not a candidate (it would end at 11:00 sharp only if
	// it started at 10:00, which does fit), so the earliest start is used.
	placements, unplaced = Schedule(slot, []Candidate{{
		TaskID:       2,
		Title:        "Короткая работа",
		Estimate:     time.Hour,
		HardDeadline: &deadline,
	}}, 10*time.Minute)
	if len(placements) != 1 || len(unplaced) != 0 {
		t.Fatalf("placements = %+v, unplaced = %+v, want one placement", placements, unplaced)
	}
	if !placements[0].End.Equal(base.Add(time.Hour)) {
		t.Errorf("placed %s-%s, want it to end before the deadline", placements[0].Start, placements[0].End)
	}
}
