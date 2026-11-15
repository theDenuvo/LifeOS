package planning

import (
	"context"
	"sort"
	"strconv"
	"time"

	domainplanning "github.com/theDenuvo/LifeOS/internal/domain/planning"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// dayState is one day of the replanning horizon: its window and what is left of
// it after commitments and already planned work were subtracted.
type dayState struct {
	date    task.CalendarDate
	window  Interval
	working bool
	free    []Interval
}

// replanTx proposes where overdue and deadline-bound work can go.
//
// Two rules make the proposal trustworthy: a task that already has time in the
// horizon is never proposed again (it would get a second block for the same work),
// and a day is only chosen when it really contains one continuous free interval of
// the required length — a day full of small gaps cannot hold a two-hour task just
// because the gaps add up.
func (s *Service) replanTx(
	ctx context.Context,
	tx Transaction,
	prefs Preferences,
	loc *time.Location,
	now time.Time,
	horizonDays int,
) (Proposal, error) {
	today, err := dateOf(now, loc)
	if err != nil {
		return Proposal{}, err
	}
	tasksByID, err := s.openTasks(ctx, tx)
	if err != nil {
		return Proposal{}, err
	}
	history, err := s.loadHistory(ctx, tx)
	if err != nil {
		return Proposal{}, err
	}

	horizonEnd := addCalendarDays(today, horizonDays).Time(loc)
	horizonBlocks, err := tx.Blocks().ListRange(ctx, now.UTC(), horizonEnd.UTC())
	if err != nil {
		return Proposal{}, err
	}
	// Work that already has a block in the horizon is scheduled already: proposing
	// it again would reserve the same work twice.
	blocked := make(map[int64]domainplanning.Block, len(horizonBlocks))
	for _, block := range horizonBlocks {
		if existing, ok := blocked[block.TaskID]; !ok || block.StartsAt.Before(existing.StartsAt) {
			blocked[block.TaskID] = block
		}
	}

	days := make([]dayState, 0, horizonDays)
	for offset := 0; offset < horizonDays; offset++ {
		date := addCalendarDays(today, offset)
		window, working := prefs.WindowOn(date, loc)
		state := dayState{date: date, window: window, working: working}
		if working {
			start := date.Time(loc)
			end := start.AddDate(0, 0, 1)
			events, err := tx.Events().ListRange(ctx, start.UTC(), end.UTC(), nil)
			if err != nil {
				return Proposal{}, err
			}
			busy := make([]Busy, 0, len(events)+len(horizonBlocks))
			for _, item := range events {
				interval := Interval{Start: item.Event.StartsAt, End: item.Event.EndsAt}
				if item.Event.AllDay {
					interval = Interval{Start: start, End: end}
				}
				busy = append(busy, Busy{Interval: interval, Kind: "event", Label: item.Event.Title})
			}
			for _, block := range horizonBlocks {
				if !block.StartsAt.Before(end.UTC()) && !block.EndsAt.After(start.UTC()) {
					continue
				}
				if !intervalsOverlap(block.StartsAt, block.EndsAt, start.UTC(), end.UTC()) {
					continue
				}
				busy = append(busy, Busy{
					Interval: Interval{Start: block.StartsAt, End: block.EndsAt},
					Kind:     "block",
					BlockID:  block.ID,
					TaskID:   block.TaskID,
				})
			}
			state.free = FreeSlots(window, busy, prefs.Buffer, prefs.MinSlot)
			if offset == 0 {
				// The current day only has the time that is left in it.
				state.free = dropShortSlots(ClipFrom(state.free, nextMinute(now)), prefs.MinSlot)
			}
		}
		days = append(days, state)
	}

	type pending struct {
		candidate Candidate
		deadline  *task.CalendarDate
	}
	items := make([]pending, 0, len(tasksByID))
	kept := make([]KeptTaskView, 0, 4)
	for _, value := range tasksByID {
		item := value.Task
		if block, ok := blocked[item.ID]; ok {
			reason := "уже запланировано на " + block.StartsAt.In(loc).Format("02.01 15:04")
			if !block.Source.Movable() {
				reason = "время уже занято вручную: " + block.StartsAt.In(loc).Format("02.01 15:04")
			}
			kept = append(kept, KeptTaskView{
				TaskID: item.ID,
				Title:  item.Title,
				Reason: reason,
				Start:  block.StartsAt,
				End:    block.EndsAt,
				Source: string(block.Source),
			})
			continue
		}

		resolved := s.resolveEstimate(item, prefs, history)
		candidate := Candidate{
			TaskID:         item.ID,
			Title:          item.Title,
			Priority:       item.Priority,
			Estimate:       resolved.Value,
			EstimateSource: resolved.Source,
			DueAt:          item.DueAt,
			CreatedAt:      item.CreatedAt,
			Recurring:      item.Repeat != nil,
			Urgency:        2,
		}
		var deadline *task.CalendarDate
		if item.DueDate != nil && !item.DueDate.IsZero() {
			due := *item.DueDate
			candidate.DueDate = &due
			deadline = &due
		}
		if item.DueAt != nil {
			if item.DueAt.After(now) {
				hard := item.DueAt.UTC()
				candidate.HardDeadline = &hard
				candidate.DeadlineLabel = "дедлайна " + hard.In(loc).Format("02.01 15:04")
			}
			if dueDate, err := dateOf(*item.DueAt, loc); err == nil {
				deadline = &dueDate
			}
		}
		if item.IsOverdue(now) {
			candidate.Urgency = 0
		} else if deadline != nil && deadline.String() <= today.String() {
			candidate.Urgency = 1
		}
		items = append(items, pending{candidate: candidate, deadline: deadline})
	}
	sort.SliceStable(items, func(a, b int) bool {
		left, right := items[a].candidate, items[b].candidate
		if left.Urgency != right.Urgency {
			return left.Urgency < right.Urgency
		}
		if left.Priority != right.Priority {
			return priorityRank(left.Priority) < priorityRank(right.Priority)
		}
		if leftDeadline, rightDeadline := deadlineKey(items[a].deadline), deadlineKey(items[b].deadline); leftDeadline != rightDeadline {
			return leftDeadline < rightDeadline
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})

	proposal := Proposal{
		GeneratedAt: now,
		HorizonDays: horizonDays,
		Moves:       []Move{},
		Placements:  []ProposedPlacement{},
		Unplaced:    []UnplacedView{},
		Kept:        kept,
		Days:        make([]DayLoad, 0, len(days)),
	}

	placed := 0
	for _, item := range items {
		// A deadline that already passed cannot constrain the plan any more: the
		// task has to be done as early as possible, not "before yesterday".
		deadline := item.deadline
		if deadline != nil && deadline.String() < today.String() {
			deadline = nil
		}

		targetDay, targetSlot := -1, -1
		for i := range days {
			if !days[i].working {
				continue
			}
			if deadline != nil && days[i].date.String() > deadline.String() {
				continue
			}
			if slot := fitSlot(days[i].free, item.candidate); slot >= 0 {
				targetDay, targetSlot = i, slot
				break
			}
		}
		if targetDay < 0 {
			proposal.Unplaced = append(proposal.Unplaced, UnplacedView{
				TaskID:      item.candidate.TaskID,
				Title:       item.candidate.Title,
				EstimateMin: int(item.candidate.Estimate.Minutes()),
				Source:      item.candidate.EstimateSource,
				Reason:      shortageReason(item.candidate, deadline, days),
			})
			continue
		}

		state := &days[targetDay]
		start := state.free[targetSlot].Start
		end := start.Add(item.candidate.Estimate)
		remaining := Interval{Start: end.Add(prefs.Buffer), End: state.free[targetSlot].End}
		if remaining.Duration() > 0 {
			state.free[targetSlot] = remaining
		} else {
			state.free = append(state.free[:targetSlot], state.free[targetSlot+1:]...)
		}

		proposal.Placements = append(proposal.Placements, ProposedPlacement{
			TaskID: item.candidate.TaskID,
			Date:   state.date.String(),
			Start:  start.UTC(),
			End:    end.UTC(),
		})
		reason := "освободилось время"
		if item.candidate.Urgency == 0 {
			reason = "просрочено"
		} else if item.candidate.Urgency == 1 {
			reason = "срок сегодня"
		}
		proposal.Moves = append(proposal.Moves, Move{
			TaskID:   item.candidate.TaskID,
			Title:    item.candidate.Title,
			To:       state.date.String(),
			Reason:   reason,
			Estimate: int(item.candidate.Estimate.Minutes()),
		})
		placed++
	}

	for _, state := range days {
		free := time.Duration(0)
		for _, slot := range state.free {
			free += slot.Duration()
		}
		planned := state.window.Duration() - free
		if planned < 0 {
			planned = 0
		}
		proposal.Days = append(proposal.Days, DayLoad{
			Date:        state.date.String(),
			Working:     state.working,
			FreeMinutes: int(free.Minutes()),
			PlannedMin:  int(planned.Minutes()),
		})
	}
	if placed == 0 {
		proposal.Summary = "Переносить нечего: всё уже помещается."
	} else {
		proposal.Summary = "Предложено мест: " + strconv.Itoa(placed) + ". Ничего не изменится без вашего согласия."
	}
	return proposal, nil
}

// shortageReason explains why the work fits nowhere in the horizon.
//
// The reason is only produced after every allowed day was checked, so a task is
// never reported as unplaceable while another day still has room for it.
func shortageReason(candidate Candidate, deadline *task.CalendarDate, days []dayState) string {
	total := time.Duration(0)
	for _, day := range days {
		if !day.working {
			continue
		}
		if deadline != nil && day.date.String() > deadline.String() {
			continue
		}
		for _, slot := range day.free {
			total += slot.Duration()
		}
	}
	label := candidate.DeadlineLabel
	if label == "" {
		label = "сроку"
	}
	switch {
	case total == 0:
		if deadline != nil {
			return "не помещается до " + deadline.String()
		}
		return "на горизонте нет свободного времени"
	case candidate.HardDeadline != nil:
		return "не хватает времени до " + label
	case total >= candidate.Estimate:
		return "нужно непрерывное окно " + formatDuration(candidate.Estimate) + ", а свободное время разбито на части"
	}
	if deadline != nil {
		return "не помещается до " + deadline.String()
	}
	return "свободного времени меньше, чем нужно (" + formatDuration(total) + " из " + formatDuration(candidate.Estimate) + ")"
}
