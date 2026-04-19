package tasksqlite

import (
	"time"

	"rig/internal/adapters/repository/tasksqlite/generated"
	"rig/internal/core"
)

func createTaskParams(task *core.Task) generated.CreateTaskParams {
	return generated.CreateTaskParams{
		ID:           task.ID,
		Prompt:       task.Prompt,
		DisplayName:  task.DisplayName,
		RepoRoot:     task.RepoRoot,
		RepoName:     task.RepoName,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
		TmuxSession:  task.TmuxSession,
		Provider:     string(task.Provider),
		Status:       string(task.Status),
		CreatedAt:    formatTime(task.CreatedAt),
		UpdatedAt:    formatTime(task.UpdatedAt),
	}
}

func updateTaskParams(task *core.Task) generated.UpdateTaskParams {
	return generated.UpdateTaskParams{
		Prompt:       task.Prompt,
		DisplayName:  task.DisplayName,
		RepoRoot:     task.RepoRoot,
		RepoName:     task.RepoName,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
		TmuxSession:  task.TmuxSession,
		Provider:     string(task.Provider),
		Status:       string(task.Status),
		CreatedAt:    formatTime(task.CreatedAt),
		UpdatedAt:    formatTime(task.UpdatedAt),
		ID:           task.ID,
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
		Prompt:       row.Prompt,
		DisplayName:  row.DisplayName,
		RepoRoot:     row.RepoRoot,
		RepoName:     row.RepoName,
		BranchName:   row.BranchName,
		WorktreePath: row.WorktreePath,
		TmuxSession:  row.TmuxSession,
		Provider:     core.AgentProvider(row.Provider),
		Status:       core.TaskStatus(row.Status),
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
