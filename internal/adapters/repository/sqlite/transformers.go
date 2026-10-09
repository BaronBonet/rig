package sqlite

import (
	"time"

	"github.com/BaronBonet/rig/internal/adapters/repository/sqlite/generated"
	"github.com/BaronBonet/rig/internal/core"
)

func createTaskParams(task *core.Task) generated.CreateTaskParams {
	return generated.CreateTaskParams{
		ID:             task.ID,
		Slug:           task.Slug,
		Prompt:         task.Prompt,
		DisplayName:    task.DisplayName,
		RepoRoot:       task.RepoRoot,
		RepoName:       task.RepoName,
		BranchName:     task.BranchName,
		WorktreePath:   task.WorktreePath,
		TmuxSession:    task.TmuxSession,
		Provider:       string(task.Provider),
		CreationStatus: string(normalizeTaskCreationStatus(task.CreationStatus)),
		CreationStep:   string(task.CreationStep),
		CreationError:  task.CreationError,
		CreatedAt:      formatTime(task.CreatedAt),
		UpdatedAt:      formatTime(task.UpdatedAt),
	}
}

func updateTaskParams(task *core.Task) generated.UpdateTaskParams {
	return generated.UpdateTaskParams{
		Slug:           task.Slug,
		Prompt:         task.Prompt,
		DisplayName:    task.DisplayName,
		RepoRoot:       task.RepoRoot,
		RepoName:       task.RepoName,
		BranchName:     task.BranchName,
		WorktreePath:   task.WorktreePath,
		TmuxSession:    task.TmuxSession,
		Provider:       string(task.Provider),
		CreationStatus: string(normalizeTaskCreationStatus(task.CreationStatus)),
		CreationStep:   string(task.CreationStep),
		CreationError:  task.CreationError,
		CreatedAt:      formatTime(task.CreatedAt),
		UpdatedAt:      formatTime(task.UpdatedAt),
		ID:             task.ID,
	}
}

func insertTaskActivityParams(event core.TaskActivityEvent) generated.InsertTaskActivityParams {
	return generated.InsertTaskActivityParams{
		TaskID:         event.TaskID,
		AgentSessionID: event.AgentSessionID,
		TurnID:         event.TurnID,
		EventName:      event.EventName,
		Role:           string(event.Role),
		Text:           event.Text,
		ObservedAt:     formatTime(event.ObservedAt),
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

func taskFromRow(row generated.ListTasksRow) *core.Task {
	return &core.Task{
		ID:             row.ID,
		Slug:           row.Slug,
		Prompt:         row.Prompt,
		DisplayName:    row.DisplayName,
		RepoRoot:       row.RepoRoot,
		RepoName:       row.RepoName,
		BranchName:     row.BranchName,
		WorktreePath:   row.WorktreePath,
		TmuxSession:    row.TmuxSession,
		Provider:       core.Provider(row.Provider),
		CreationStatus: normalizeTaskCreationStatus(core.TaskCreationStatus(row.CreationStatus)),
		CreationStep:   core.TaskCreateProgressStep(row.CreationStep),
		CreationError:  row.CreationError,
		CreatedAt:      parseTime(row.CreatedAt),
		UpdatedAt:      parseTime(row.UpdatedAt),
	}
}

func normalizeTaskCreationStatus(status core.TaskCreationStatus) core.TaskCreationStatus {
	if status == "" {
		return core.TaskCreationStatusReady
	}
	return status
}

func tasksFromRows(rows []generated.ListTasksRow) []*core.Task {
	tasks := make([]*core.Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, taskFromRow(row))
	}
	return tasks
}

func taskActivityEventFromRow(row generated.TaskActivity) core.TaskActivityEvent {
	return core.TaskActivityEvent{
		TaskID:         row.TaskID,
		AgentSessionID: row.AgentSessionID,
		TurnID:         row.TurnID,
		EventName:      row.EventName,
		Role:           core.TaskActivityRole(row.Role),
		Text:           row.Text,
		ObservedAt:     parseTime(row.ObservedAt),
	}
}

func taskActivityEventsFromRows(rows []generated.TaskActivity) []core.TaskActivityEvent {
	events := make([]core.TaskActivityEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, taskActivityEventFromRow(row))
	}
	return events
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

func createAgentSessionParams(session core.AgentSession) generated.CreateAgentSessionParams {
	return generated.CreateAgentSessionParams{
		ID:                  session.ID,
		TaskID:              session.TaskID,
		Provider:            string(session.Provider),
		TmuxSocketPath:      session.TmuxServer.SocketPath,
		TmuxServerPid:       int64(session.TmuxServer.PID),
		TmuxPane:            session.TmuxPane,
		PaneTrusted:         boolToInt64(session.PaneTrusted),
		ProviderSessionID:   session.ProviderSessionID,
		StartedAt:           formatTime(session.StartedAt),
		EndedAt:             formatTime(session.EndedAt),
		LaunchedAt:          formatTime(session.LaunchedAt),
		StatusPhase:         string(session.Status.Phase),
		StatusRawEventName:  session.Status.RawEventName,
		StatusObservedAt:    formatTime(session.Status.ObservedAt),
		BackgroundSubagents: int64(session.Status.BackgroundWork.Subagents),
		BackgroundShells:    int64(session.Status.BackgroundWork.Shells),
		BackgroundMonitors:  int64(session.Status.BackgroundWork.Monitors),
		BackgroundWorkflows: int64(session.Status.BackgroundWork.Workflows),
		BackgroundOther:     int64(session.Status.BackgroundWork.Other),
	}
}

func updateAgentSessionParams(session core.AgentSession) generated.UpdateAgentSessionParams {
	return generated.UpdateAgentSessionParams{
		TmuxSocketPath:      session.TmuxServer.SocketPath,
		TmuxServerPid:       int64(session.TmuxServer.PID),
		TmuxPane:            session.TmuxPane,
		LaunchedAt:          formatTime(session.LaunchedAt),
		PaneTrusted:         boolToInt64(session.PaneTrusted),
		ProviderSessionID:   session.ProviderSessionID,
		StatusPhase:         string(session.Status.Phase),
		StatusRawEventName:  session.Status.RawEventName,
		StatusObservedAt:    formatTime(session.Status.ObservedAt),
		BackgroundSubagents: int64(session.Status.BackgroundWork.Subagents),
		BackgroundShells:    int64(session.Status.BackgroundWork.Shells),
		BackgroundMonitors:  int64(session.Status.BackgroundWork.Monitors),
		BackgroundWorkflows: int64(session.Status.BackgroundWork.Workflows),
		BackgroundOther:     int64(session.Status.BackgroundWork.Other),
		ID:                  session.ID,
	}
}

func agentSessionFromRow(row generated.AgentSession) core.AgentSession {
	return core.AgentSession{
		ID:       row.ID,
		TaskID:   row.TaskID,
		Provider: core.Provider(row.Provider),
		TmuxServer: core.TmuxServer{
			SocketPath: row.TmuxSocketPath,
			PID:        int(row.TmuxServerPid),
		},
		TmuxPane:          row.TmuxPane,
		PaneTrusted:       row.PaneTrusted != 0,
		ProviderSessionID: row.ProviderSessionID,
		StartedAt:         parseTime(row.StartedAt),
		EndedAt:           parseTime(row.EndedAt),
		LaunchedAt:        parseTime(row.LaunchedAt),
		Status: core.AgentSessionStatus{
			ObservedAt:   parseTime(row.StatusObservedAt),
			RawEventName: row.StatusRawEventName,
			Phase:        core.TaskStatusPhase(row.StatusPhase),
			BackgroundWork: core.TaskBackgroundWork{
				Subagents: int(row.BackgroundSubagents),
				Shells:    int(row.BackgroundShells),
				Monitors:  int(row.BackgroundMonitors),
				Workflows: int(row.BackgroundWorkflows),
				Other:     int(row.BackgroundOther),
			},
		},
	}
}

func agentSessionsFromRows(rows []generated.AgentSession) []core.AgentSession {
	sessions := make([]core.AgentSession, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, agentSessionFromRow(row))
	}
	return sessions
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
