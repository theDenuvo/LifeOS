package goals

import (
	"context"

	"github.com/theDenuvo/LifeOS/internal/application/tasks"
)

// TaskEngineAdapter narrows the task engine to the TaskProvider the goal
// service needs.
//
// It lives here rather than in cmd/lifeos so production and the HTTP tests
// exercise the same adapter, including the linked-task progress path that used
// to be missing from the test wiring.
type TaskEngineAdapter struct {
	Service *tasks.Service
}

// GetTask returns the task fields the goal progress calculation reads.
func (a TaskEngineAdapter) GetTask(ctx context.Context, id int64) (TaskView, error) {
	item, err := a.Service.GetTask(ctx, id)
	if err != nil {
		return TaskView{}, err
	}
	return TaskView{
		ID:          item.Task.ID,
		Title:       item.Task.Title,
		Status:      string(item.Task.Status),
		CompletedAt: item.Task.CompletedAt,
	}, nil
}

var _ TaskProvider = TaskEngineAdapter{}
