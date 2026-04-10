package sqlite

import (
	"time"

	"agent/internal/adapters/repository/sqlite/generated"
	"agent/internal/core"
)

func taskFromRow(row generated.GetTaskByIDOrSlugRow) *core.Task {
	return &core.Task{
		ID:                 row.ID,
		Prompt:             row.Prompt,
		DisplayName:        row.DisplayName,
		Slug:               row.Slug,
		RepoRoot:           row.RepoRoot,
		RepoName:           row.RepoName,
		BaseBranch:         row.BaseBranch,
		BranchName:         row.BranchName,
		WorktreePath:       row.WorktreePath,
		TmuxSession:        row.TmuxSession,
		AgentWindowName:    row.AgentWindowName,
		EditorWindowName:   row.EditorWindowName,
		Provider:           row.Provider,
		Status:             core.TaskStatus(row.Status),
		WorktreeExists:     row.WorktreeExists == 1,
		BranchExists:       row.BranchExists == 1,
		SessionExists:      row.SessionExists == 1,
		AgentWindowExists:  row.AgentWindowExists == 1,
		EditorWindowExists: row.EditorWindowExists == 1,
		LastError:          row.LastError,
		CreatedAt:          parseTime(row.CreatedAt),
		UpdatedAt:          parseTime(row.UpdatedAt),
		LastReconciledAt:   parseTime(row.LastReconciledAt),
	}
}

func tasksFromRows(rows []generated.ListTasksRow) []*core.Task {
	tasks := make([]*core.Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, &core.Task{
			ID:                 row.ID,
			Prompt:             row.Prompt,
			DisplayName:        row.DisplayName,
			Slug:               row.Slug,
			RepoRoot:           row.RepoRoot,
			RepoName:           row.RepoName,
			BaseBranch:         row.BaseBranch,
			BranchName:         row.BranchName,
			WorktreePath:       row.WorktreePath,
			TmuxSession:        row.TmuxSession,
			AgentWindowName:    row.AgentWindowName,
			EditorWindowName:   row.EditorWindowName,
			Provider:           row.Provider,
			Status:             core.TaskStatus(row.Status),
			WorktreeExists:     row.WorktreeExists == 1,
			BranchExists:       row.BranchExists == 1,
			SessionExists:      row.SessionExists == 1,
			AgentWindowExists:  row.AgentWindowExists == 1,
			EditorWindowExists: row.EditorWindowExists == 1,
			LastError:          row.LastError,
			CreatedAt:          parseTime(row.CreatedAt),
			UpdatedAt:          parseTime(row.UpdatedAt),
			LastReconciledAt:   parseTime(row.LastReconciledAt),
		})
	}
	return tasks
}

func createTaskParams(task *core.Task) generated.CreateTaskParams {
	return generated.CreateTaskParams{
		ID:                 task.ID,
		Prompt:             task.Prompt,
		DisplayName:        task.DisplayName,
		Slug:               task.Slug,
		RepoRoot:           task.RepoRoot,
		RepoName:           task.RepoName,
		BaseBranch:         task.BaseBranch,
		BranchName:         task.BranchName,
		WorktreePath:       task.WorktreePath,
		TmuxSession:        task.TmuxSession,
		AgentWindowName:    task.AgentWindowName,
		EditorWindowName:   task.EditorWindowName,
		Provider:           task.Provider,
		Status:             string(task.Status),
		WorktreeExists:     int64(boolToInt(task.WorktreeExists)),
		BranchExists:       int64(boolToInt(task.BranchExists)),
		SessionExists:      int64(boolToInt(task.SessionExists)),
		AgentWindowExists:  int64(boolToInt(task.AgentWindowExists)),
		EditorWindowExists: int64(boolToInt(task.EditorWindowExists)),
		LastError:          task.LastError,
		CreatedAt:          formatTime(task.CreatedAt),
		UpdatedAt:          formatTime(task.UpdatedAt),
		LastReconciledAt:   formatTime(task.LastReconciledAt),
	}
}

func updateTaskParams(task *core.Task) generated.UpdateTaskParams {
	params := createTaskParams(task)
	return generated.UpdateTaskParams{
		Prompt:             params.Prompt,
		DisplayName:        params.DisplayName,
		Slug:               params.Slug,
		RepoRoot:           params.RepoRoot,
		RepoName:           params.RepoName,
		BaseBranch:         params.BaseBranch,
		BranchName:         params.BranchName,
		WorktreePath:       params.WorktreePath,
		TmuxSession:        params.TmuxSession,
		AgentWindowName:    params.AgentWindowName,
		EditorWindowName:   params.EditorWindowName,
		Provider:           params.Provider,
		Status:             params.Status,
		WorktreeExists:     params.WorktreeExists,
		BranchExists:       params.BranchExists,
		SessionExists:      params.SessionExists,
		AgentWindowExists:  params.AgentWindowExists,
		EditorWindowExists: params.EditorWindowExists,
		LastError:          params.LastError,
		CreatedAt:          params.CreatedAt,
		UpdatedAt:          params.UpdatedAt,
		LastReconciledAt:   params.LastReconciledAt,
		ID:                 task.ID,
	}
}

func appendEventParams(taskID, eventType, payload string) generated.AppendEventParams {
	return generated.AppendEventParams{
		TaskID:    taskID,
		EventType: eventType,
		Payload:   payload,
		CreatedAt: nowRFC3339Nano(time.Now()),
	}
}

func nowRFC3339Nano(ts time.Time) string {
	return ts.UTC().Format(time.RFC3339Nano)
}
