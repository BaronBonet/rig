package sqlite

import (
	"time"

	"rig/internal/adapters/repository/sqlite/generated"
	"rig/internal/core"
)

func createTaskParams(task *core.Task) generated.CreateTaskParams {
	return generated.CreateTaskParams{
		ID:           task.ID,
		Slug:         task.Slug,
		Prompt:       task.Prompt,
		DisplayName:  task.DisplayName,
		RepoRoot:     task.RepoRoot,
		RepoName:     task.RepoName,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
		TmuxSession:  task.TmuxSession,
		Provider:     string(task.Provider),
		CreatedAt:    formatTime(task.CreatedAt),
		UpdatedAt:    formatTime(task.UpdatedAt),
	}
}

func updateTaskParams(task *core.Task) generated.UpdateTaskParams {
	return generated.UpdateTaskParams{
		Slug:         task.Slug,
		Prompt:       task.Prompt,
		DisplayName:  task.DisplayName,
		RepoRoot:     task.RepoRoot,
		RepoName:     task.RepoName,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
		TmuxSession:  task.TmuxSession,
		Provider:     string(task.Provider),
		CreatedAt:    formatTime(task.CreatedAt),
		UpdatedAt:    formatTime(task.UpdatedAt),
		ID:           task.ID,
	}
}

func upsertTaskStatusParams(update core.TaskStatusUpdate) generated.UpsertTaskStatusParams {
	return generated.UpsertTaskStatusParams{
		TaskID:       update.TaskID,
		Provider:     string(update.Provider),
		Phase:        string(update.Phase),
		RawEventName: update.RawEventName,
		ObservedAt:   formatTime(update.ObservedAt),
	}
}

func insertTaskActivityParams(event core.TaskActivityEvent) generated.InsertTaskActivityParams {
	return generated.InsertTaskActivityParams{
		TaskID:     event.TaskID,
		TurnID:     event.TurnID,
		EventName:  event.EventName,
		Role:       string(event.Role),
		Text:       event.Text,
		ObservedAt: formatTime(event.ObservedAt),
	}
}

func upsertTaskResumeMetadataParams(metadata core.TaskResumeMetadata) generated.UpsertTaskResumeMetadataParams {
	return generated.UpsertTaskResumeMetadataParams{
		TaskID:     metadata.TaskID,
		Provider:   string(metadata.Provider),
		SessionID:  metadata.SessionID,
		ObservedAt: formatTime(metadata.ObservedAt),
	}
}

func upsertTaskProviderSessionParams(session core.TaskProviderSession) generated.UpsertTaskProviderSessionParams {
	return generated.UpsertTaskProviderSessionParams{
		TaskID:            session.TaskID,
		Provider:          string(session.Provider),
		ProviderSessionID: session.ProviderSessionID,
		TranscriptPath:    session.TranscriptPath,
		StartSource:       session.StartSource,
		Model:             session.Model,
		Cwd:               session.Cwd,
		FirstObservedAt:   formatTime(session.FirstObservedAt),
		LastObservedAt:    formatTime(session.LastObservedAt),
		LastEventName:     session.LastEventName,
	}
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}

	return ts.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}

	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}

	return parsed
}

func taskFromRow(row generated.Task) *core.Task {
	return &core.Task{
		ID:           row.ID,
		Slug:         row.Slug,
		Prompt:       row.Prompt,
		DisplayName:  row.DisplayName,
		RepoRoot:     row.RepoRoot,
		RepoName:     row.RepoName,
		BranchName:   row.BranchName,
		WorktreePath: row.WorktreePath,
		TmuxSession:  row.TmuxSession,
		Provider:     core.Provider(row.Provider),
		CreatedAt:    parseTime(row.CreatedAt),
		UpdatedAt:    parseTime(row.UpdatedAt),
	}
}

func tasksFromRows(rows []generated.Task) []*core.Task {
	tasks := make([]*core.Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, taskFromRow(row))
	}
	return tasks
}

func taskStatusUpdateFromRow(row generated.TaskStatus) *core.TaskStatusUpdate {
	return &core.TaskStatusUpdate{
		TaskID:       row.TaskID,
		Provider:     core.Provider(row.Provider),
		Phase:        core.TaskStatusPhase(row.Phase),
		RawEventName: row.RawEventName,
		ObservedAt:   parseTime(row.ObservedAt),
	}
}

func taskActivityEventFromRow(row generated.TaskActivity) core.TaskActivityEvent {
	return core.TaskActivityEvent{
		TaskID:     row.TaskID,
		TurnID:     row.TurnID,
		EventName:  row.EventName,
		Role:       core.TaskActivityRole(row.Role),
		Text:       row.Text,
		ObservedAt: parseTime(row.ObservedAt),
	}
}

func taskActivityEventsFromRows(rows []generated.TaskActivity) []core.TaskActivityEvent {
	events := make([]core.TaskActivityEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, taskActivityEventFromRow(row))
	}
	return events
}

func taskResumeMetadataFromRow(row generated.TaskResumeMetadatum) *core.TaskResumeMetadata {
	return &core.TaskResumeMetadata{
		TaskID:     row.TaskID,
		Provider:   core.Provider(row.Provider),
		SessionID:  row.SessionID,
		ObservedAt: parseTime(row.ObservedAt),
	}
}

func taskProviderSessionFromRow(row generated.ListTaskProviderSessionsRow) core.TaskProviderSession {
	return core.TaskProviderSession{
		TaskID:            row.TaskID,
		Provider:          core.Provider(row.Provider),
		ProviderSessionID: row.ProviderSessionID,
		TranscriptPath:    row.TranscriptPath,
		StartSource:       row.StartSource,
		LastEventName:     row.LastEventName,
		Model:             row.Model,
		Cwd:               row.Cwd,
		FirstObservedAt:   parseTime(row.FirstObservedAt),
		LastObservedAt:    parseTime(row.LastObservedAt),
	}
}

func taskProviderSessionsFromRows(rows []generated.ListTaskProviderSessionsRow) []core.TaskProviderSession {
	sessions := make([]core.TaskProviderSession, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, taskProviderSessionFromRow(row))
	}
	return sessions
}
