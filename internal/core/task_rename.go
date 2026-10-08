package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *service) RenameTask(ctx context.Context, taskID string, name string) error {
	return s.operations.Run(ctx, taskID, taskOperationRename, false, func(ctx context.Context) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("a task needs a name")
		}
		task, err := taskByID(ctx, s.tasks, taskID)
		if err != nil {
			return err
		}
		if task.CreationStatus == TaskCreationStatusCreating {
			return fmt.Errorf("%q is still being created; rename it once it is ready", task.DisplayName)
		}
		if !task.Rename(name) {
			return nil
		}
		if err := s.tasks.UpdateTask(ctx, task); err != nil {
			return fmt.Errorf("update task %s: %w", task.ID, err)
		}
		return nil
	})
}

// adoptSessionTitle renames the Task after the title the user gave its
// provider session, when that title is new. Reading it is best effort: a
// transcript that cannot be read leaves the Task as it is.
func (o *taskObservation) adoptSessionTitle(
	ctx context.Context,
	providerClient ProviderClient,
	input HookEventInput,
) error {
	switch input.EventName {
	case HookEventSessionStart, HookEventUserPromptSubmit, HookEventStop:
	default:
		return nil
	}
	transcriptPath := strings.TrimSpace(input.TranscriptPath)
	if transcriptPath == "" {
		return nil
	}
	title, _ := providerClient.ReadSessionTitle(ctx, transcriptPath)
	if title == "" {
		return nil
	}
	task, err := taskByID(ctx, o.tasks, input.TaskID)
	if err != nil {
		return err
	}
	if title == task.SessionTitle || task.CreationStatus == TaskCreationStatusCreating {
		return nil
	}

	task.SessionTitle = title
	// A session already named like its Task, such as an imported one, keeps
	// the Task's ask: nothing was repurposed.
	task.Rename(title)
	if err := o.tasks.UpdateTask(ctx, task); err != nil {
		return fmt.Errorf("update task %s: %w", task.ID, err)
	}
	return nil
}
