package core

import (
	"context"
	"fmt"
	"time"
)

func (s *service) ShelveTask(ctx context.Context, taskID string) error {
	return s.operations.Run(ctx, taskID, taskOperationShelve, false, func(ctx context.Context) error {
		return s.shelveTask(ctx, taskID)
	})
}

func (s *service) shelveTask(ctx context.Context, taskID string) error {
	task, err := taskByID(ctx, s.tasks, taskID)
	if err != nil {
		return err
	}
	if task.CreationStatus == TaskCreationStatusCreating {
		return fmt.Errorf("%q is still being created; shelve it once it is ready", task.DisplayName)
	}
	group := append([]*Task{task}, s.childTasks(ctx, task.ID)...)
	for _, member := range group {
		if s.taskIsWorking(ctx, member.ID) {
			return fmt.Errorf(
				"%w: %q is working; shelve it once it is idle",
				ErrProviderSessionActive,
				member.DisplayName,
			)
		}
	}

	// The sessions end with the tmux session, or with a child's own window;
	// their resume metadata stays, so opening the Task later resumes them.
	if err := s.tmuxSession.DeleteTaskSession(ctx, task); err != nil {
		return fmt.Errorf("close task session: %w", err)
	}
	return s.setShelved(ctx, group, time.Now().UTC())
}

func (s *service) UnshelveTask(ctx context.Context, taskID string) error {
	return s.operations.Run(ctx, taskID, taskOperationUnshelve, true, func(ctx context.Context) error {
		task, err := taskByID(ctx, s.tasks, taskID)
		if err != nil {
			return err
		}
		return s.setShelved(ctx, append([]*Task{task}, s.childTasks(ctx, task.ID)...), time.Time{})
	})
}

// setShelved moves the Tasks onto the shelf at the given time, or back onto
// the current list for a zero time. A Task already where it is sent keeps
// its record, so a child shelved on its own keeps when that was.
func (s *service) setShelved(ctx context.Context, tasks []*Task, at time.Time) error {
	for _, task := range tasks {
		if task.IsShelved() == !at.IsZero() {
			continue
		}
		task.ShelvedAt = at
		task.UpdatedAt = time.Now().UTC()
		if err := s.tasks.UpdateTask(ctx, task); err != nil {
			return fmt.Errorf("update task %s: %w", task.ID, err)
		}
	}
	return nil
}
