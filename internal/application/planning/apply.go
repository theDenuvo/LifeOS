package planning

import (
	"context"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domainplanning "github.com/theDenuvo/LifeOS/internal/domain/planning"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// applyTx writes an accepted plan.
//
// The whole request is validated before anything is written, and nothing is
// deleted up front: only the blocks the new placement actually supersedes are
// removed. Accepting the proposal for a new task therefore leaves the blocks that
// were already in the day untouched, instead of silently wiping a plan the user
// had accepted earlier.
func (s *Service) applyTx(
	ctx context.Context,
	tx Transaction,
	in ApplyInput,
	prefs Preferences,
	loc *time.Location,
	now time.Time,
) ([]PlanItem, []string, error) {
	dates, err := parseDates(in.ReplaceDates)
	if err != nil {
		return nil, nil, err
	}
	for _, item := range in.Items {
		if item.TaskID <= 0 || item.Start.IsZero() || !item.End.After(item.Start) {
			return nil, nil, ErrPlacementInvalid
		}
		day, err := dateOf(item.Start, loc)
		if err != nil {
			return nil, nil, err
		}
		dates = append(dates, day)
	}
	dates = uniqueDates(dates)

	// Everything that already occupies the affected days, loaded once.
	existing := make([]domainplanning.Block, 0, 8)
	for _, date := range dates {
		start := date.Time(loc)
		blocks, err := tx.Blocks().ListRange(ctx, start.UTC(), start.AddDate(0, 0, 1).UTC())
		if err != nil {
			return nil, nil, err
		}
		existing = append(existing, blocks...)
	}

	type prepared struct {
		input   PlacementInput
		task    tasks.TaskWithVersion
		date    task.CalendarDate
		reuseID int64
		report  PlanItem
	}
	batch := make([]prepared, 0, len(in.Items))
	superseded := make(map[int64]bool, 4)

	for _, item := range in.Items {
		date, err := dateOf(item.Start, loc)
		if err != nil {
			return nil, nil, err
		}
		window, working := prefs.WindowOn(date, loc)
		if !working {
			return nil, nil, fmt.Errorf("%w: %s is not a working day", ErrPlacementOutsideWindow, date)
		}
		if item.Start.Before(window.Start) || item.End.After(window.End) {
			return nil, nil, fmt.Errorf("%w: %s is outside %s-%s", ErrPlacementOutsideWindow, date, formatClock(prefs.Start), formatClock(prefs.End))
		}
		// Passed time is not reservable, and a plan for a finished day makes no
		// sense: the user would get a block they cannot work in.
		if dateIsPast(date, now, loc) {
			return nil, nil, fmt.Errorf("%w: %s", ErrPlacementInPast, date)
		}
		if item.Start.Before(now) {
			return nil, nil, fmt.Errorf("%w: %s", ErrPlacementInPast, item.Start.In(loc).Format("15:04"))
		}

		found, err := tx.Tasks().FindByID(ctx, item.TaskID)
		if err != nil {
			return nil, nil, ErrNoTask
		}
		if found.Task.Status != task.StatusOpen {
			return nil, nil, ErrTaskNotOpen
		}
		if err := checkDeadline(found.Task, item, now, loc); err != nil {
			return nil, nil, err
		}

		events, err := tx.Events().ListRange(ctx, item.Start.UTC(), item.End.UTC(), nil)
		if err != nil {
			return nil, nil, err
		}
		for _, event := range events {
			if event.Event.StartsAt.Before(item.End.UTC()) && item.Start.UTC().Before(event.Event.EndsAt) {
				return nil, nil, fmt.Errorf("%w: %s", ErrPlacementConflict, event.Event.Title)
			}
		}

		reuseID := int64(0)
		for _, block := range existing {
			if !intervalsOverlap(block.StartsAt, block.EndsAt, item.Start.UTC(), item.End.UTC()) {
				continue
			}
			if !block.Source.Movable() {
				return nil, nil, fmt.Errorf("%w: %s", ErrPlacementConflict, "уже стоит блок на это время")
			}
			// Automatic blocks belong to the planner: the same range means the
			// request is repeated and must be a no-op, anything else means the
			// work is being moved.
			if block.TaskID == item.TaskID && sameRange(block, item) {
				reuseID = block.ID
				continue
			}
			superseded[block.ID] = true
		}

		for _, other := range batch {
			if intervalsOverlap(other.input.Start.UTC(), other.input.End.UTC(), item.Start.UTC(), item.End.UTC()) {
				return nil, nil, fmt.Errorf("%w: %s", ErrPlacementConflict, "два блока в этом запросе пересекаются")
			}
		}

		batch = append(batch, prepared{input: item, task: found, date: date, reuseID: reuseID})
	}

	for id := range superseded {
		if err := tx.Blocks().Delete(ctx, id); err != nil {
			return nil, nil, err
		}
	}

	applied := make([]PlanItem, 0, len(batch))
	skipped := make([]string, 0)
	for _, item := range batch {
		if item.reuseID != 0 {
			if err := s.setPlannedDate(ctx, tx, item.task, item.date, now); err != nil {
				return nil, nil, err
			}
			for _, block := range existing {
				if block.ID != item.reuseID {
					continue
				}
				applied = append(applied, PlanItem{
					BlockID:  block.ID,
					TaskID:   block.TaskID,
					Title:    item.task.Task.Title,
					Priority: item.task.Task.Priority,
					Start:    block.StartsAt,
					End:      block.EndsAt,
					Source:   block.Source,
					Status:   block.Status,
				})
			}
			continue
		}

		block, err := domainplanning.NewBlock(item.input.TaskID, item.input.Start, item.input.End, domainplanning.BlockPlanned, domainplanning.SourceAuto, now)
		if err != nil {
			return nil, nil, err
		}
		stored, err := tx.Blocks().Insert(ctx, block)
		if err != nil {
			return nil, nil, err
		}
		if err := s.setPlannedDate(ctx, tx, item.task, item.date, now); err != nil {
			return nil, nil, err
		}
		applied = append(applied, PlanItem{
			BlockID:  stored.ID,
			TaskID:   stored.TaskID,
			Title:    item.task.Task.Title,
			Priority: item.task.Task.Priority,
			Start:    stored.StartsAt,
			End:      stored.EndsAt,
			Source:   stored.Source,
			Status:   stored.Status,
		})
	}
	return applied, skipped, nil
}

// checkDeadline refuses a placement that would not be finished in time.
//
// The server enforces this itself instead of trusting the proposal: a client can
// send any start and end, and an accepted block that ends after the deadline is a
// promise the user cannot keep.
func checkDeadline(value *task.Task, item PlacementInput, now time.Time, loc *time.Location) error {
	if value.DueAt != nil && value.DueAt.After(now) && item.End.After(*value.DueAt) {
		return fmt.Errorf("%w: %s", ErrPlacementAfterDue, value.DueAt.In(loc).Format("02.01 15:04"))
	}
	if value.DueDate != nil && !value.DueDate.IsZero() {
		date, err := dateOf(item.Start, loc)
		if err == nil && date.String() > value.DueDate.String() {
			return fmt.Errorf("%w: %s", ErrPlacementAfterDue, value.DueDate.String())
		}
	}
	return nil
}

func intervalsOverlap(startA, endA, startB, endB time.Time) bool {
	return startA.Before(endB) && startB.Before(endA)
}

func sameRange(block domainplanning.Block, item PlacementInput) bool {
	return block.StartsAt.Equal(item.Start.UTC()) && block.EndsAt.Equal(item.End.UTC())
}
