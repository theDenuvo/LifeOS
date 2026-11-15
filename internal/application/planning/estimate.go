package planning

import (
	"context"
	"sort"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// minHistorySamples is how many similar finished tasks are needed before the
// median is trusted. One sample is an anecdote, two are a pattern.
const minHistorySamples = 2

// maxHistorySamples bounds how much history one planning call reads.
const maxHistorySamples = 500

// estimateSample is one finished piece of work.
type estimateSample struct {
	TaskID  int64
	Title   string
	Minutes int
	Tags    []string
}

// historyIndex remembers how long similar completed work took, so a task without
// an explicit estimate is not silently assigned an invented duration.
type historyIndex struct {
	byWord map[string][]estimateSample
}

// loadHistory builds the index from the user's completed tasks.
func (s *Service) loadHistory(ctx context.Context, tx Transaction) (*historyIndex, error) {
	result, err := tx.Tasks().List(ctx, tasks.TaskFilter{
		Statuses: []task.Status{task.StatusCompleted},
		Limit:    tasks.Some(maxHistorySamples),
	})
	if err != nil {
		return nil, err
	}
	index := &historyIndex{byWord: make(map[string][]estimateSample, 64)}
	for _, item := range result.Tasks {
		if item.Task.EstimateMinutes == nil {
			continue
		}
		sample := estimateSample{
			TaskID:  item.Task.ID,
			Title:   item.Task.Title,
			Minutes: *item.Task.EstimateMinutes,
			Tags:    append([]string(nil), item.Task.Tags...),
		}
		for word := range significantWords(item.Task.Title) {
			index.byWord[word] = append(index.byWord[word], sample)
		}
	}
	return index, nil
}

// samples returns the finished work that resembles the given title, deduplicated
// so a task matching on two words is still counted once.
func (h *historyIndex) samples(title string, tags []string) []estimateSample {
	if h == nil {
		return nil
	}
	seen := make(map[int64]bool, 8)
	out := make([]estimateSample, 0, 8)
	for word := range significantWords(title) {
		for _, sample := range h.byWord[word] {
			if seen[sample.TaskID] {
				continue
			}
			if len(tags) > 0 && !sharesTags(tags, sample.Tags) {
				continue
			}
			seen[sample.TaskID] = true
			out = append(out, sample)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].TaskID < out[b].TaskID })
	return out
}

func sharesTags(wanted, have []string) bool {
	for _, left := range wanted {
		for _, right := range have {
			if left == right {
				return true
			}
		}
	}
	return false
}

// medianMinutes is the middle sample, so one outlier cannot skew the plan.
func medianMinutes(samples []estimateSample) int {
	if len(samples) == 0 {
		return 0
	}
	values := make([]int, 0, len(samples))
	for _, sample := range samples {
		values = append(values, sample.Minutes)
	}
	sort.Ints(values)
	return values[len(values)/2]
}

// resolvedEstimate is the duration the planner uses for one task, together with
// the reason it chose it.
type resolvedEstimate struct {
	Value   time.Duration
	Source  EstimateSource
	Samples int
}

// resolveEstimate is the single rule for unknown durations:
//
//  1. the estimate the user stated on the task always wins;
//  2. otherwise the median of similar completed work;
//  3. otherwise the configured planning.default_estimate_minutes.
//
// The chosen source travels with the plan, so the interface can say "по истории"
// or "по умолчанию" instead of presenting a guess as a known duration.
func (s *Service) resolveEstimate(value *task.Task, prefs Preferences, history *historyIndex) resolvedEstimate {
	if value == nil {
		return resolvedEstimate{Value: prefs.DefaultEstimate, Source: EstimateDefault}
	}
	if value.EstimateMinutes != nil {
		return resolvedEstimate{
			Value:  time.Duration(*value.EstimateMinutes) * time.Minute,
			Source: EstimateExplicit,
		}
	}
	if samples := history.samples(value.Title, value.Tags); len(samples) >= minHistorySamples {
		return resolvedEstimate{
			Value:   time.Duration(medianMinutes(samples)) * time.Minute,
			Source:  EstimateHistory,
			Samples: len(samples),
		}
	}
	return resolvedEstimate{Value: prefs.DefaultEstimate, Source: EstimateDefault}
}
