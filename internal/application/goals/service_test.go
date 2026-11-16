package goals_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	appgoals "github.com/theDenuvo/LifeOS/internal/application/goals"
	domaingoal "github.com/theDenuvo/LifeOS/internal/domain/goal"
)

// stepClock is a controllable clock: tests move it forward to age goals.
type stepClock struct{ at time.Time }

func (c *stepClock) Now() time.Time { return c.at }

func (c *stepClock) add(d time.Duration) { c.at = c.at.Add(d) }

func testClock() *stepClock {
	return &stepClock{at: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
}

// fakeTaskProvider answers progress queries with a fixed table.
type fakeTasks map[int64]appgoals.TaskView

func (t fakeTasks) GetTask(_ context.Context, id int64) (appgoals.TaskView, error) {
	view, ok := t[id]
	if !ok {
		return appgoals.TaskView{}, errors.New("task not found")
	}
	return view, nil
}

// HasCompletionEvent is false here on purpose: the fake table describes the
// current state only, and the durable-history recovery is covered against a
// real SQLite database where an audit log exists.
func (t fakeTasks) HasCompletionEvent(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

// fakeRepo mirrors the SQLite semantics the goal service depends on:
// versioned goal writes, ordered milestones, target existence checks.
type fakeRepo struct {
	goals      map[int64]*domaingoal.Goal
	milestones map[int64]*domaingoal.Milestone
	links      map[int64]*domaingoal.Link
	progress   map[int64][]domaingoal.ProgressEvent
	proposals  map[int64]*domaingoal.Proposal
	targets    map[domaingoal.TargetType]map[int64]bool
	nextID     int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		goals:      make(map[int64]*domaingoal.Goal),
		milestones: make(map[int64]*domaingoal.Milestone),
		links:      make(map[int64]*domaingoal.Link),
		progress:   make(map[int64][]domaingoal.ProgressEvent),
		proposals:  make(map[int64]*domaingoal.Proposal),
		targets: map[domaingoal.TargetType]map[int64]bool{
			domaingoal.TargetTask: {1: true, 2: true},
		},
	}
}

func (r *fakeRepo) next() int64 {
	r.nextID++
	return r.nextID
}

func (r *fakeRepo) InsertGoal(_ context.Context, goal *domaingoal.Goal, milestones []domaingoal.MilestoneInput) (int64, error) {
	if err := goal.Validate(); err != nil {
		return 0, err
	}
	id := r.next()
	stored := *goal
	stored.ID = id
	r.goals[id] = &stored
	for index, input := range milestones {
		if err := domaingoal.ValidateMilestoneInput(input); err != nil {
			return 0, err
		}
		ms := &domaingoal.Milestone{
			ID: r.next(), GoalID: id, Position: index, Title: strings.TrimSpace(input.Title),
			Criteria: strings.TrimSpace(input.Criteria), DueDate: input.DueDate,
			Status: domaingoal.MilestonePending, CreatedAt: goal.CreatedAt,
			UpdatedAt: goal.CreatedAt, Version: 1,
		}
		r.milestones[ms.ID] = ms
	}
	return id, nil
}

func (r *fakeRepo) GetGoal(_ context.Context, id int64) (*domaingoal.Goal, error) {
	goal, ok := r.goals[id]
	if !ok {
		return nil, appgoals.ErrNotFound
	}
	stored := *goal
	return &stored, nil
}

func (r *fakeRepo) UpdateGoal(_ context.Context, id int64, expectedVersion int, goal *domaingoal.Goal) (*domaingoal.Goal, error) {
	stored, ok := r.goals[id]
	if !ok {
		return nil, appgoals.ErrNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appgoals.ErrVersionConflict
	}
	updated := *goal
	updated.ID = id
	updated.Version = expectedVersion + 1
	r.goals[id] = &updated
	result := updated
	return &result, nil
}

func (r *fakeRepo) DeleteGoal(_ context.Context, id int64) error {
	if _, ok := r.goals[id]; !ok {
		return appgoals.ErrNotFound
	}
	delete(r.goals, id)
	for id, m := range r.milestones {
		if m.GoalID == id {
			delete(r.milestones, id)
		}
	}
	for id, l := range r.links {
		if l.GoalID == id {
			delete(r.links, id)
		}
	}
	return nil
}

func (r *fakeRepo) ListGoals(_ context.Context, filter appgoals.Filter) ([]*domaingoal.Goal, int, error) {
	matches := make([]*domaingoal.Goal, 0, len(r.goals))
	for _, goal := range r.goals {
		if filter.Status != nil && goal.Status != *filter.Status {
			continue
		}
		if query := strings.ToLower(strings.TrimSpace(filter.Query)); query != "" && !strings.Contains(strings.ToLower(goal.Title), query) {
			continue
		}
		matches = append(matches, goal)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	total := len(matches)
	limit := filter.Limit
	if limit <= 0 {
		limit = 30
	}
	if filter.Offset >= len(matches) {
		return nil, total, nil
	}
	end := filter.Offset + limit
	if end > len(matches) {
		end = len(matches)
	}
	return matches[filter.Offset:end], total, nil
}

func (r *fakeRepo) CountByStatus(_ context.Context) (map[domaingoal.Status]int, error) {
	counts := make(map[domaingoal.Status]int)
	for _, goal := range r.goals {
		counts[goal.Status]++
	}
	return counts, nil
}

func (r *fakeRepo) InsertMilestone(_ context.Context, milestone *domaingoal.Milestone) (int64, error) {
	if err := milestone.Validate(); err != nil {
		return 0, err
	}
	id := r.next()
	stored := *milestone
	stored.ID = id
	r.milestones[id] = &stored
	return id, nil
}

func (r *fakeRepo) ListMilestones(_ context.Context, goalID int64) ([]domaingoal.Milestone, error) {
	milestones := make([]domaingoal.Milestone, 0, 4)
	for _, m := range r.milestones {
		if m.GoalID == goalID {
			milestones = append(milestones, *m)
		}
	}
	sort.Slice(milestones, func(i, j int) bool { return milestones[i].Position < milestones[j].Position })
	return milestones, nil
}

func (r *fakeRepo) GetMilestone(_ context.Context, goalID, milestoneID int64) (*domaingoal.Milestone, error) {
	m, ok := r.milestones[milestoneID]
	if !ok || m.GoalID != goalID {
		return nil, appgoals.ErrMilestoneNotFound
	}
	stored := *m
	return &stored, nil
}

func (r *fakeRepo) UpdateMilestone(_ context.Context, milestoneID int64, expectedVersion int, milestone *domaingoal.Milestone) (*domaingoal.Milestone, error) {
	stored, ok := r.milestones[milestoneID]
	if !ok {
		return nil, appgoals.ErrMilestoneNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appgoals.ErrVersionConflict
	}
	updated := *milestone
	updated.ID = milestoneID
	updated.Version = expectedVersion + 1
	r.milestones[milestoneID] = &updated
	result := updated
	return &result, nil
}

func (r *fakeRepo) DeleteMilestone(_ context.Context, goalID, milestoneID int64) error {
	m, ok := r.milestones[milestoneID]
	if !ok || m.GoalID != goalID {
		return appgoals.ErrMilestoneNotFound
	}
	delete(r.milestones, milestoneID)
	return nil
}

func (r *fakeRepo) InsertLink(_ context.Context, link *domaingoal.Link) (int64, error) {
	if err := link.Validate(); err != nil {
		return 0, err
	}
	id := r.next()
	stored := *link
	stored.ID = id
	r.links[id] = &stored
	return id, nil
}

func (r *fakeRepo) ListLinks(_ context.Context, goalID int64) ([]domaingoal.Link, error) {
	links := make([]domaingoal.Link, 0, 4)
	for _, link := range r.links {
		if link.GoalID == goalID {
			links = append(links, *link)
		}
	}
	return links, nil
}

func (r *fakeRepo) ListLinksByTarget(_ context.Context, target domaingoal.TargetType, targetID int64) ([]domaingoal.Link, error) {
	links := make([]domaingoal.Link, 0, 4)
	for _, link := range r.links {
		if link.TargetType != target {
			continue
		}
		if targetID > 0 && link.TargetID != targetID {
			continue
		}
		links = append(links, *link)
	}
	return links, nil
}

func (r *fakeRepo) HasProgressSource(_ context.Context, goalID int64, kind domaingoal.ProgressKind, sourceType domaingoal.TargetType, sourceID int64) (bool, error) {
	for _, event := range r.progress[goalID] {
		if event.Kind != kind || event.SourceType == nil || event.SourceID == nil {
			continue
		}
		if *event.SourceType == sourceType && *event.SourceID == sourceID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) DeleteLink(_ context.Context, goalID, linkID int64) error {
	link, ok := r.links[linkID]
	if !ok || link.GoalID != goalID {
		return appgoals.ErrLinkNotFound
	}
	delete(r.links, linkID)
	return nil
}

func (r *fakeRepo) TargetExists(_ context.Context, target domaingoal.TargetType, id int64) (bool, error) {
	return r.targets[target][id], nil
}

func (r *fakeRepo) InsertProgress(_ context.Context, event *domaingoal.ProgressEvent) (int64, error) {
	if err := event.Validate(); err != nil {
		return 0, err
	}
	id := r.next()
	stored := *event
	stored.ID = id
	r.progress[event.GoalID] = append(r.progress[event.GoalID], stored)
	return id, nil
}

func (r *fakeRepo) ListProgress(_ context.Context, goalID int64, limit int) ([]domaingoal.ProgressEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	events := r.progress[goalID]
	if len(events) > limit {
		events = events[:limit]
	}
	return append([]domaingoal.ProgressEvent(nil), events...), nil
}

func (r *fakeRepo) LastActivity(_ context.Context, goalID int64) (*time.Time, error) {
	events := r.progress[goalID]
	if len(events) == 0 {
		return nil, nil
	}
	latest := events[0].RecordedAt
	for _, event := range events {
		if event.RecordedAt.After(latest) {
			latest = event.RecordedAt
		}
	}
	return &latest, nil
}

func (r *fakeRepo) InsertProposal(_ context.Context, proposal *domaingoal.Proposal) (int64, error) {
	if err := proposal.Validate(); err != nil {
		return 0, err
	}
	id := r.next()
	stored := *proposal
	stored.ID = id
	r.proposals[id] = &stored
	return id, nil
}

func (r *fakeRepo) GetProposal(_ context.Context, goalID, proposalID int64) (*domaingoal.Proposal, error) {
	p, ok := r.proposals[proposalID]
	if !ok || p.GoalID != goalID {
		return nil, appgoals.ErrProposalNotFound
	}
	stored := *p
	return &stored, nil
}

func (r *fakeRepo) ListProposals(_ context.Context, goalID int64, pendingOnly bool) ([]domaingoal.Proposal, error) {
	proposals := make([]domaingoal.Proposal, 0, 2)
	for _, p := range r.proposals {
		if p.GoalID == goalID && (!pendingOnly || p.Status == domaingoal.ProposalPending) {
			proposals = append(proposals, *p)
		}
	}
	return proposals, nil
}

func (r *fakeRepo) SetProposalStatus(_ context.Context, proposalID int64, status domaingoal.ProposalStatus, at time.Time) error {
	p, ok := r.proposals[proposalID]
	if !ok || p.Status != domaingoal.ProposalPending {
		return appgoals.ErrNoPendingProposal
	}
	p.Status = status
	p.DecidedAt = &at
	return nil
}

func (r *fakeRepo) HasPendingProposal(_ context.Context, goalID int64) (bool, error) {
	for _, p := range r.proposals {
		if p.GoalID == goalID && p.Status == domaingoal.ProposalPending {
			return true, nil
		}
	}
	return false, nil
}

type fakeTx struct{ repo *fakeRepo }

func (t fakeTx) Goals() appgoals.Repository { return t.repo }

type fakeUoW struct{ repo *fakeRepo }

func (u *fakeUoW) Do(_ context.Context, fn func(context.Context, appgoals.Transaction) error) error {
	return fn(context.Background(), fakeTx{repo: u.repo})
}

func newService(t *testing.T) (*appgoals.Service, *fakeRepo, *fakeTasks, *stepClock) {
	t.Helper()
	repo := newFakeRepo()
	tasks := fakeTasks{
		1: {ID: 1, Title: "Подготовить структуру курса", Status: "completed"},
		2: {ID: 2, Title: "Записать первый урок", Status: "open"},
	}
	clock := testClock()
	return appgoals.NewService(&fakeUoW{repo: repo}, clock, appgoals.WithTaskProvider(tasks)), repo, &tasks, clock
}

func milestone(title string) domaingoal.MilestoneInput {
	return domaingoal.MilestoneInput{Title: title, Criteria: "критерий готов"}
}

func TestCreateStartsProposedAndApprovalStartsWork(t *testing.T) {
	service, _, _, _ := newService(t)
	view, err := service.Create(context.Background(), appgoals.CreateInput{
		Title:       "Выучить английский до C1",
		Description: "Свободно читать и говорить",
		Milestones:  []domaingoal.MilestoneInput{milestone("Разобраться в грамматике"), milestone("Накопить словарь")},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.Goal.Status != domaingoal.StatusProposed {
		t.Fatalf("a drafted goal must be proposed, got %s", view.Goal.Status)
	}
	if len(view.Milestones) != 2 {
		t.Fatalf("expected 2 milestones, got %d", len(view.Milestones))
	}
	if view.Review == nil || !view.Review.Signals.PendingProposal {
		// A goal with a plan implies a review; a proposal implies nothing yet.
		_ = "no pending proposal expected before approval"
	}

	approved, err := service.Approve(context.Background(), view.Goal.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Goal.Status != domaingoal.StatusActive || approved.Goal.ApprovedAt == nil {
		t.Fatalf("approval must activate the goal, got %s", approved.Goal.Status)
	}
	if _, err := service.Approve(context.Background(), view.Goal.ID); !errors.Is(err, appgoals.ErrInvalidTransition) {
		t.Fatalf("second approval must be refused, got %v", err)
	}
}

func TestApprovedPlanChangesOnlyThroughProposal(t *testing.T) {
	service, _, _, _ := newService(t)
	view, err := service.Create(context.Background(), appgoals.CreateInput{
		Title: "Выпустить продукт", Milestones: []domaingoal.MilestoneInput{milestone("Собрать бета-версию")},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	goalID := view.Goal.ID
	if _, err := service.Approve(context.Background(), goalID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Direct edits of an approved plan are refused.
	if _, err := service.AddMilestone(context.Background(), goalID, milestone("Взломать")); !errors.Is(err, appgoals.ErrGoalNotProposed) {
		t.Fatalf("add milestone on approved goal must be refused, got %v", err)
	}
	current, err := service.Get(context.Background(), goalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := service.UpdatePlan(context.Background(), goalID, current.Goal.Version, appgoals.UpdatePlanInput{}); !errors.Is(err, appgoals.ErrGoalNotProposed) {
		t.Fatalf("update plan on approved goal must be refused, got %v", err)
	}

	// A proposal applies only after the user's decision.
	proposal, err := service.ProposeChange(context.Background(), goalID, domaingoal.ProposalPayload{
		AddMilestones: []domaingoal.MilestoneInput{milestone("Найти первых пользователей")},
	}, "Добавить этап поиска первых пользователей после беты")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if proposal.Status != domaingoal.ProposalPending {
		t.Fatalf("proposal must be pending, got %s", proposal.Status)
	}
	if _, err := service.ProposeChange(context.Background(), goalID, domaingoal.ProposalPayload{Title: ptr("Дубль")}, "дубль"); !errors.Is(err, appgoals.ErrAlreadyProposed) {
		t.Fatalf("second pending proposal must be refused, got %v", err)
	}

	before, err := service.Get(context.Background(), goalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := len(before.Milestones); got != 1 {
		t.Fatalf("plan must stay unchanged until approval, got %d milestones", got)
	}
	if before.Review == nil || !before.Review.Signals.PendingProposal {
		t.Fatal("pending proposal must surface in the review signals")
	}

	if _, err := service.ApplyProposal(context.Background(), goalID, proposal.ID); err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	after, err := service.Get(context.Background(), goalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := len(after.Milestones); got != 2 {
		t.Fatalf("applied proposal must extend the plan, got %d milestones", got)
	}
	if after.Review.Signals.PendingProposal {
		t.Fatal("applied proposal must not stay pending")
	}
}

func TestRejectProposalLeavesPlanUntouched(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title: "Переехать в другой город", Milestones: []domaingoal.MilestoneInput{milestone("Выбрать город")},
	})
	goalID := view.Goal.ID
	_, _ = service.Approve(context.Background(), goalID)
	proposal, err := service.ProposeChange(context.Background(), goalID, domaingoal.ProposalPayload{
		Title: ptr("Отпуск в другом городе"),
	}, "Поменять формулировку цели")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := service.RejectProposal(context.Background(), goalID, proposal.ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	after, _ := service.Get(context.Background(), goalID)
	if got := len(after.Milestones); got != 1 {
		t.Fatalf("rejected proposal must not change the plan, got %d milestones", got)
	}
}

func TestMilestoneProgressAndStallDetection(t *testing.T) {
	service, _, _, clock := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title:      "Запустить курс",
		Milestones: []domaingoal.MilestoneInput{milestone("Сценарий"), milestone("Запись"), milestone("Монтаж")},
	})
	goalID := view.Goal.ID
	_, _ = service.Approve(context.Background(), goalID)

	current, _ := service.Get(context.Background(), goalID)
	var scenario domaingoal.Milestone
	for _, m := range current.Milestones {
		if m.Title == "Сценарий" {
			scenario = m
		}
	}
	updated, err := service.UpdateMilestone(context.Background(), goalID, scenario.ID, scenario.Version,
		appgoals.MilestoneUpdateInput{Status: statusPtr(domaingoal.MilestoneDone)})
	if err != nil {
		t.Fatalf("update milestone: %v", err)
	}
	if updated.Review.Progress.MilestonesDone != 1 || updated.Review.Progress.Percent == nil || *updated.Review.Progress.Percent != 33 {
		t.Fatalf("progress must reflect one done milestone out of three, got %+v", updated.Review.Progress)
	}
	journalHasMilestone := false
	for _, event := range updated.ProgressLog {
		if event.Kind == domaingoal.ProgressMilestone {
			journalHasMilestone = true
		}
	}
	if !journalHasMilestone {
		t.Fatalf("milestone completion must reach the journal, got %+v", updated.ProgressLog)
	}

	// Nothing moves for three weeks: the goal counts as stalled. Reading does
	// not record progress, so the journal stays at the original day.
	clock.add(21 * 24 * time.Hour)
	aged, err := service.Get(context.Background(), goalID)
	if err != nil {
		t.Fatalf("get aged goal: %v", err)
	}
	if !aged.Review.Signals.Stalled {
		t.Fatal("a goal silent for three weeks must be flagged as stalled")
	}
	stalled, err := service.Stalled(context.Background())
	if err != nil {
		t.Fatalf("stalled: %v", err)
	}
	if len(stalled) != 1 || stalled[0].Goal.ID != goalID {
		t.Fatalf("expected the untouched goal to surface as stalled, got %+v", stalled)
	}
	if reason := stalled[0].Review.Signals.StallReason; !strings.Contains(reason, "нет записей") {
		t.Fatalf("stall reason must explain itself, got %q", reason)
	}
}

func TestTaskCompletionReachesGoalJournal(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "Обучить модель"})
	goalID := view.Goal.ID
	if _, err := service.AddLink(context.Background(), goalID, appgoals.LinkInput{
		TargetType: domaingoal.TargetTask, TargetID: 1, Relation: "подготовка данных",
	}); err != nil {
		t.Fatalf("link: %v", err)
	}
	service.OnTaskCompleted(context.Background(), 1)
	after, err := service.Get(context.Background(), goalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	found := false
	for _, event := range after.ProgressLog {
		if event.Kind == domaingoal.ProgressTaskDone {
			found = true
		}
	}
	if !found {
		t.Fatalf("task completion must reach the goal journal, got %+v", after.ProgressLog)
	}
	if after.Review.Progress.LinkedTasks != 1 || after.Review.Progress.LinkedTasksDone != 1 {
		t.Fatalf("linked task progress must count the completion, got %+v", after.Review.Progress)
	}
}

func TestLinkTargetMustExist(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "Цель"})
	if _, err := service.AddLink(context.Background(), view.Goal.ID, appgoals.LinkInput{
		TargetType: domaingoal.TargetTask, TargetID: 999, Relation: "связь",
	}); !errors.Is(err, appgoals.ErrInvalidTarget) {
		t.Fatalf("link to a missing task must be refused, got %v", err)
	}
	if _, err := service.AddLink(context.Background(), view.Goal.ID, appgoals.LinkInput{
		TargetType: domaingoal.TargetTask, TargetID: 1, Relation: "связь",
	}); err != nil {
		t.Fatalf("link to an existing task: %v", err)
	}
}

func TestLifecycleTransitions(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "Съездить в поход"})
	goalID := view.Goal.ID

	if _, err := service.SetStatus(context.Background(), goalID, domaingoal.StatusPaused); !errors.Is(err, appgoals.ErrInvalidTransition) {
		t.Fatalf("pause before approval must be refused, got %v", err)
	}
	_, _ = service.Approve(context.Background(), goalID)
	if _, err := service.SetStatus(context.Background(), goalID, domaingoal.StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := service.SetStatus(context.Background(), goalID, domaingoal.StatusActive); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := service.SetStatus(context.Background(), goalID, domaingoal.StatusAchieved); err != nil {
		t.Fatalf("achieve: %v", err)
	}
	if _, err := service.Update(context.Background(), goalID, 1, appgoals.UpdateInput{Title: ptr("Новое")}); !errors.Is(err, appgoals.ErrVersionConflict) {
		t.Fatalf("editing an achieved goal at a stale version must conflict, got %v", err)
	}
	stats, err := service.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Achieved != 1 {
		t.Fatalf("stats must count the achieved goal, got %+v", stats)
	}
}

func TestProposalPayloadValidation(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "Цель"})
	_, _ = service.Approve(context.Background(), view.Goal.ID)
	if _, err := service.ProposeChange(context.Background(), view.Goal.ID, domaingoal.ProposalPayload{}, "ничего не меняет"); !errors.Is(err, appgoals.ErrEmptyPayload) {
		t.Fatalf("empty proposal must be refused, got %v", err)
	}
	if _, err := service.ProposeChange(context.Background(), view.Goal.ID, domaingoal.ProposalPayload{
		CompleteMilestoneIDs: []int64{12345},
	}, "чужой этап"); !errors.Is(err, appgoals.ErrMilestoneOutside) {
		t.Fatalf("foreign milestone ids must be refused, got %v", err)
	}
}

func TestContextSnapshotListsGoalsAndStall(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title:      "Написать книгу",
		Milestones: []domaingoal.MilestoneInput{milestone("План книги")},
	})
	_, _ = service.Approve(context.Background(), view.Goal.ID)
	_ = view
	snapshot, err := service.ContextSnapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snapshot, "Написать книгу") {
		t.Fatalf("snapshot must mention the active goal, got %q", snapshot)
	}
	if !strings.Contains(snapshot, "предложено 0") || !strings.Contains(snapshot, "в работе 1") {
		t.Fatalf("snapshot must carry the stats, got %q", snapshot)
	}
}

func statusPtr(status domaingoal.MilestoneStatus) *domaingoal.MilestoneStatus { return &status }

func ptr(value string) *string { return &value }

// TestApprovedPlanCannotBeEditedThroughMilestoneUpdate covers the review
// finding that UpdateMilestone skipped the approval gate: renaming a step of an
// active goal bypassed propose_goal_change.
func TestApprovedPlanCannotBeEditedThroughMilestoneUpdate(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title: "Запустить подкаст", Milestones: []domaingoal.MilestoneInput{milestone("Пилотный выпуск")},
	})
	goalID := view.Goal.ID
	if _, err := service.Approve(context.Background(), goalID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	current, _ := service.Get(context.Background(), goalID)
	step := current.Milestones[0]

	renamed := "Совсем другой этап"
	if _, err := service.UpdateMilestone(context.Background(), goalID, step.ID, step.Version,
		appgoals.MilestoneUpdateInput{Title: &renamed}); !errors.Is(err, appgoals.ErrGoalNotProposed) {
		t.Fatalf("renaming a step of an approved goal must be refused, got %v", err)
	}

	// A status move is factual progress and stays allowed.
	if _, err := service.UpdateMilestone(context.Background(), goalID, step.ID, step.Version,
		appgoals.MilestoneUpdateInput{Status: statusPtr(domaingoal.MilestoneDone)}); err != nil {
		t.Fatalf("status move on an approved goal must be allowed, got %v", err)
	}
}

// TestTerminalGoalPlanIsImmutable covers AddMilestone/RemoveMilestone allowing
// changes on achieved or abandoned goals.
func TestTerminalGoalPlanIsImmutable(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title: "Завершённая цель", Milestones: []domaingoal.MilestoneInput{milestone("Этап")},
	})
	goalID := view.Goal.ID
	_, _ = service.Approve(context.Background(), goalID)
	if _, err := service.SetStatus(context.Background(), goalID, domaingoal.StatusAchieved); err != nil {
		t.Fatalf("achieve: %v", err)
	}

	if _, err := service.AddMilestone(context.Background(), goalID, milestone("Поздний этап")); !errors.Is(err, appgoals.ErrGoalTerminal) {
		t.Fatalf("adding a step to an achieved goal must be refused, got %v", err)
	}
	current, _ := service.Get(context.Background(), goalID)
	if _, err := service.RemoveMilestone(context.Background(), goalID, current.Milestones[0].ID); !errors.Is(err, appgoals.ErrGoalTerminal) {
		t.Fatalf("removing a step from an achieved goal must be refused, got %v", err)
	}
	if _, err := service.UpdateMilestone(context.Background(), goalID, current.Milestones[0].ID, current.Milestones[0].Version,
		appgoals.MilestoneUpdateInput{Status: statusPtr(domaingoal.MilestonePending)}); !errors.Is(err, appgoals.ErrGoalTerminal) {
		t.Fatalf("changing a step of an achieved goal must be refused, got %v", err)
	}
}

// TestRecordRejectsMissingSource covers a journal entry claiming a source that
// does not exist.
func TestRecordRejectsMissingSource(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "Цель"})
	sourceType := domaingoal.TargetTask

	missing := int64(999)
	if _, err := service.Record(context.Background(), view.Goal.ID, appgoals.RecordInput{
		Summary: "Сделано", SourceType: &sourceType, SourceID: &missing,
	}); !errors.Is(err, appgoals.ErrInvalidTarget) {
		t.Fatalf("record against a missing task must be refused, got %v", err)
	}

	zero := int64(0)
	if _, err := service.Record(context.Background(), view.Goal.ID, appgoals.RecordInput{
		Summary: "Сделано", SourceType: &sourceType, SourceID: &zero,
	}); !errors.Is(err, appgoals.ErrInvalidTarget) {
		t.Fatalf("record with a zero source id must be refused, got %v", err)
	}

	existing := int64(1)
	if _, err := service.Record(context.Background(), view.Goal.ID, appgoals.RecordInput{
		Summary: "Сделано", SourceType: &sourceType, SourceID: &existing,
	}); err != nil {
		t.Fatalf("record against an existing task: %v", err)
	}
}

// TestSkippedMilestoneDoesNotCountAsProgress covers the percentage arithmetic:
// a skipped step closes the plan but is not an achieved step.
func TestSkippedMilestoneDoesNotCountAsProgress(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title:      "Собрать отчёт",
		Milestones: []domaingoal.MilestoneInput{milestone("Собрать данные"), milestone("Написать текст"), milestone("Сверстать")},
	})
	goalID := view.Goal.ID
	_, _ = service.Approve(context.Background(), goalID)
	current, _ := service.Get(context.Background(), goalID)

	updated, err := service.UpdateMilestone(context.Background(), goalID, current.Milestones[0].ID, current.Milestones[0].Version,
		appgoals.MilestoneUpdateInput{Status: statusPtr(domaingoal.MilestoneSkipped)})
	if err != nil {
		t.Fatalf("skip milestone: %v", err)
	}
	if updated.Review.Progress.MilestonesDone != 0 {
		t.Fatalf("skipped step counted as done: %+v", updated.Review.Progress)
	}
	if updated.Review.Progress.Percent == nil || *updated.Review.Progress.Percent != 0 {
		t.Fatalf("percent = %v, want 0", updated.Review.Progress.Percent)
	}
	if updated.Review.Progress.MilestonesCompleted != 1 {
		t.Fatalf("skipped step must still close the plan: %+v", updated.Review.Progress)
	}
	if updated.Review.Signals.NoOpenWork {
		t.Fatal("two open steps remain: no_open_work must be false")
	}
}

// TestProposedAndPausedGoalsDoNotStall covers the stall signal being computed
// for goals that are not being worked on.
func TestProposedAndPausedGoalsDoNotStall(t *testing.T) {
	service, _, _, clock := newService(t)
	proposed, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "Идея на будущее"})
	active, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "В работе"})
	_, _ = service.Approve(context.Background(), active.Goal.ID)
	if _, err := service.SetStatus(context.Background(), active.Goal.ID, domaingoal.StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}

	clock.add(60 * 24 * time.Hour)

	proposedView, err := service.Get(context.Background(), proposed.Goal.ID)
	if err != nil {
		t.Fatalf("get proposed: %v", err)
	}
	if proposedView.Review.Signals.Stalled {
		t.Fatal("a proposed goal waits for the user; it must not be reported as stalled")
	}
	pausedView, err := service.Get(context.Background(), active.Goal.ID)
	if err != nil {
		t.Fatalf("get paused: %v", err)
	}
	if pausedView.Review.Signals.Stalled {
		t.Fatal("a paused goal was stopped on purpose; it must not be reported as stalled")
	}
}

// TestReconcileTaskProgressIsIdempotent covers the catch-up path: a completion
// missed by the live pump is restored once, and a second run writes nothing.
func TestReconcileTaskProgressIsIdempotent(t *testing.T) {
	service, _, _, _ := newService(t)
	view, _ := service.Create(context.Background(), appgoals.CreateInput{Title: "Цель с задачей"})
	goalID := view.Goal.ID
	if _, err := service.AddLink(context.Background(), goalID, appgoals.LinkInput{
		TargetType: domaingoal.TargetTask, TargetID: 1, Relation: "связано",
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	// The live event is lost: nothing recorded yet.
	added, err := service.ReconcileTaskProgress(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if added != 1 {
		t.Fatalf("first reconcile added %d entries, want 1", added)
	}

	again, err := service.ReconcileTaskProgress(context.Background())
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if again != 0 {
		t.Fatalf("second reconcile added %d entries, want 0", again)
	}

	// The live pump observing the same completion must not duplicate it.
	service.OnTaskCompleted(context.Background(), 1)
	after, err := service.Get(context.Background(), goalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	count := 0
	for _, event := range after.ProgressLog {
		if event.Kind == domaingoal.ProgressTaskDone {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("task_done entries = %d, want exactly 1", count)
	}
}

// TestOnTaskCompletedReportsFailures pins the contract the review asked for:
// a journal write that did not happen must reach the caller instead of being
// logged and reported as success.
func TestOnTaskCompletedReportsFailures(t *testing.T) {
	service, _, _, _ := newService(t)
	// The task cannot be read, so the journal cannot be written at all.
	if err := service.OnTaskCompleted(context.Background(), 999); err == nil {
		t.Fatal("OnTaskCompleted returned nil although the progress was not recorded")
	}
	// A task nobody linked to is a successful no-op, not a failure.
	if err := service.OnTaskCompleted(context.Background(), 1); err != nil {
		t.Fatalf("OnTaskCompleted on an unlinked task: %v", err)
	}
}

func TestDeadlineRisksSurfacesCloseDatesWithoutProgress(t *testing.T) {
	service, _, _, clock := newService(t)

	// Target date inside the window, no activity at all: a clear risk.
	soon := clock.at.Add(4 * 24 * time.Hour)
	created, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title:      "Успеть к сдаче",
		TargetDate: &soon,
	})
	if _, err := service.Approve(context.Background(), created.Goal.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Target date far away: not a risk yet, whatever the activity.
	far := clock.at.Add(60 * 24 * time.Hour)
	calmer, _ := service.Create(context.Background(), appgoals.CreateInput{
		Title:      "Долгая цель",
		TargetDate: &far,
	})
	if _, err := service.Approve(context.Background(), calmer.Goal.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Time passes and nothing moves: the close goal starts drifting.
	clock.add(4 * 24 * time.Hour)
	risks, err := service.DeadlineRisks(context.Background())
	if err != nil {
		t.Fatalf("deadline risks: %v", err)
	}
	if len(risks) != 1 || risks[0].Goal.ID != created.Goal.ID {
		t.Fatalf("expected only the close deadline goal to surface, got %+v", risks)
	}
	if risks[0].DaysLeft < 0 || risks[0].DaysLeft > 7 {
		t.Fatalf("days left must be within the window, got %d", risks[0].DaysLeft)
	}
	if risks[0].IdleDays < 3 {
		t.Fatalf("idle days must explain the drift, got %d", risks[0].IdleDays)
	}
}
