// Package search combines task and inbox lookups into one query.
package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/theDenuvo/LifeOS/internal/application/inbox"
	"github.com/theDenuvo/LifeOS/internal/application/tasks"
	domaininbox "github.com/theDenuvo/LifeOS/internal/domain/inbox"
)

// DefaultLimit and MaxLimit bound a search response.
const (
	DefaultLimit = 20
	MaxLimit     = 50
)

// TaskSearcher is the slice of the task service used here.
type TaskSearcher interface {
	ListTasks(ctx context.Context, filter tasks.TaskFilter) (tasks.ListTasksResult, error)
}

// InboxSearcher is the slice of the inbox service used here.
type InboxSearcher interface {
	Search(ctx context.Context, query string, limit int) (inbox.ListResult, error)
}

// Result is a combined search response.
type Result struct {
	Tasks []tasks.TaskWithVersion
	Inbox []*domaininbox.Item
	Total int
}

// Service executes searches.
type Service struct {
	tasks TaskSearcher
	inbox InboxSearcher
}

// NewService builds the search service.
func NewService(taskSearcher TaskSearcher, inboxSearcher InboxSearcher) *Service {
	return &Service{tasks: taskSearcher, inbox: inboxSearcher}
}

// Query searches tasks and inbox items.
func (s *Service) Query(ctx context.Context, query string, limit int) (Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	taskResult, err := s.tasks.ListTasks(ctx, tasks.TaskFilter{
		Search: tasks.Some(query),
		Limit:  tasks.Some(limit),
	})
	if err != nil {
		return Result{}, err
	}

	inboxResult, err := s.inbox.Search(ctx, query, limit)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Tasks: taskResult.Tasks,
		Inbox: inboxResult.Items,
		Total: taskResult.Total + inboxResult.Total,
	}, nil
}
