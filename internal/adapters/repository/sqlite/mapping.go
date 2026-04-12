package sqlite

import (
	"time"

	"rig/internal/adapters/repository/sqlite/generated"
	"rig/internal/core"
)

func taskFromRow(row generated.Task) *core.Task {
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

func tasksFromRows(rows []generated.Task) []*core.Task {
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

func hookSessionSummaryFromGetRow(row generated.GetHookSessionSummaryByTaskIDRow) *core.HookSessionSummary {
	return hookSessionSummaryFromValues(
		row.TaskID,
		row.SessionID,
		row.Model,
		row.Cwd,
		row.TranscriptPath,
		row.StartSource,
		row.CurrentTurnID,
		row.LastEventName,
		row.RuntimePhase,
		row.StartedAt,
		row.LastActivityAt,
		row.LastStopAt,
		row.LastPromptPreview,
		row.LastCommandPreview,
		row.LastCommandResultPreview,
		row.LastAssistantMessage,
		row.CommandCount,
	)
}

func hookSessionSummaryFromListAllRow(row generated.ListAllHookSessionSummariesRow) *core.HookSessionSummary {
	return hookSessionSummaryFromValues(
		row.TaskID,
		row.SessionID,
		row.Model,
		row.Cwd,
		row.TranscriptPath,
		row.StartSource,
		row.CurrentTurnID,
		row.LastEventName,
		row.RuntimePhase,
		row.StartedAt,
		row.LastActivityAt,
		row.LastStopAt,
		row.LastPromptPreview,
		row.LastCommandPreview,
		row.LastCommandResultPreview,
		row.LastAssistantMessage,
		row.CommandCount,
	)
}

func hookSessionSummaryFromListByTaskIDsRow(
	row generated.ListHookSessionSummariesByTaskIDsRow,
) *core.HookSessionSummary {
	return hookSessionSummaryFromValues(
		row.TaskID,
		row.SessionID,
		row.Model,
		row.Cwd,
		row.TranscriptPath,
		row.StartSource,
		row.CurrentTurnID,
		row.LastEventName,
		row.RuntimePhase,
		row.StartedAt,
		row.LastActivityAt,
		row.LastStopAt,
		row.LastPromptPreview,
		row.LastCommandPreview,
		row.LastCommandResultPreview,
		row.LastAssistantMessage,
		row.CommandCount,
	)
}

func hookSessionSummaryFromValues(
	taskID string,
	sessionID string,
	model string,
	cwd string,
	transcriptPath string,
	startSource string,
	currentTurnID string,
	lastEventName string,
	runtimePhase string,
	startedAt string,
	lastActivityAt string,
	lastStopAt string,
	lastPromptPreview string,
	lastCommandPreview string,
	lastCommandResultPreview string,
	lastAssistantMessage string,
	commandCount int64,
) *core.HookSessionSummary {
	return &core.HookSessionSummary{
		TaskID:                taskID,
		SessionID:             sessionID,
		Model:                 model,
		Cwd:                   cwd,
		TranscriptPath:        transcriptPath,
		StartSource:           startSource,
		CurrentTurnID:         currentTurnID,
		LastEventName:         lastEventName,
		RuntimePhase:          core.HookRuntimePhase(runtimePhase),
		StartedAt:             parseTime(startedAt),
		LastActivityAt:        parseTime(lastActivityAt),
		LastStopAt:            parseTime(lastStopAt),
		LastPromptText:        lastPromptPreview,
		LastCommandText:       lastCommandPreview,
		LastCommandResultText: lastCommandResultPreview,
		LastAssistantMessage:  lastAssistantMessage,
		CommandCount:          int(commandCount),
	}
}

func hookEventFromRow(row generated.TaskHookEvent) core.HookEvent {
	return core.HookEvent{
		OccurredAt:           parseTime(row.OccurredAt),
		ID:                   row.ID,
		TaskID:               row.TaskID,
		SessionID:            row.SessionID,
		TurnID:               row.TurnID,
		EventName:            row.EventName,
		RawPayloadJSON:       row.RawPayloadJson,
		LastAssistantMessage: row.LastAssistantMessage,
		PromptText:           row.PromptPreview,
		CommandText:          row.CommandPreview,
		CommandResultText:    row.CommandResultPreview,
		ToolUseID:            row.ToolUseID,
	}
}

func observerSummaryFromGetRow(row generated.GetObserverSummaryByTaskIDRow) *core.ObserverSummary {
	return observerSummaryFromValues(
		row.TaskID,
		row.DisplayStatus,
		row.DisplayActivity,
		row.ProcessAlive,
		row.LastRuntimeObservedAt,
	)
}

func observerSummaryFromListAllRow(row generated.ListAllObserverSummariesRow) *core.ObserverSummary {
	return observerSummaryFromValues(
		row.TaskID,
		row.DisplayStatus,
		row.DisplayActivity,
		row.ProcessAlive,
		row.LastRuntimeObservedAt,
	)
}

func observerSummaryFromListByTaskIDsRow(row generated.ListObserverSummariesByTaskIDsRow) *core.ObserverSummary {
	return observerSummaryFromValues(
		row.TaskID,
		row.DisplayStatus,
		row.DisplayActivity,
		row.ProcessAlive,
		row.LastRuntimeObservedAt,
	)
}

func observerSummaryFromValues(
	taskID string,
	displayStatus string,
	displayActivity string,
	processAlive int64,
	lastRuntimeObservedAt string,
) *core.ObserverSummary {
	return &core.ObserverSummary{
		TaskID:                taskID,
		DisplayStatus:         core.DisplayStatus(displayStatus),
		DisplayActivity:       core.DisplayActivity(displayActivity),
		ProcessAlive:          processAlive == 1,
		LastRuntimeObservedAt: parseTime(lastRuntimeObservedAt),
	}
}

func hookEventParamsFromRecord(record hookRecord) generated.InsertHookEventParams {
	return generated.InsertHookEventParams{
		TaskID:               record.TaskID,
		SessionID:            record.SessionID,
		TurnID:               record.TurnID,
		EventName:            record.EventName,
		OccurredAt:           formatTime(record.OccurredAt),
		RawPayloadJson:       record.RawPayloadJSON,
		LastAssistantMessage: trimPreview(record.LastAssistantMessage),
		PromptPreview:        trimPreview(record.PromptText),
		CommandPreview:       trimPreview(record.CommandText),
		CommandResultPreview: trimPreview(record.CommandResultText),
		ToolUseID:            record.ToolUseID,
	}
}

func hookSessionSummaryParams(summary *core.HookSessionSummary) generated.UpsertHookSessionSummaryParams {
	return generated.UpsertHookSessionSummaryParams{
		TaskID:                   summary.TaskID,
		SessionID:                summary.SessionID,
		Model:                    summary.Model,
		Cwd:                      summary.Cwd,
		TranscriptPath:           summary.TranscriptPath,
		StartSource:              summary.StartSource,
		CurrentTurnID:            summary.CurrentTurnID,
		LastEventName:            summary.LastEventName,
		RuntimePhase:             string(summary.RuntimePhase),
		StartedAt:                formatTime(summary.StartedAt),
		LastActivityAt:           formatTime(summary.LastActivityAt),
		LastStopAt:               formatTime(summary.LastStopAt),
		LastPromptPreview:        summary.LastPromptText,
		LastCommandPreview:       summary.LastCommandText,
		LastCommandResultPreview: summary.LastCommandResultText,
		LastAssistantMessage:     summary.LastAssistantMessage,
		CommandCount:             int64(summary.CommandCount),
		UpdatedAt:                formatTime(summary.LastActivityAt),
	}
}

func observerSummaryParams(summary *core.ObserverSummary, updatedAt time.Time) generated.UpsertObserverSummaryParams {
	return generated.UpsertObserverSummaryParams{
		TaskID:                summary.TaskID,
		DisplayStatus:         string(summary.DisplayStatus),
		DisplayActivity:       string(summary.DisplayActivity),
		ProcessAlive:          int64(boolToInt(summary.ProcessAlive)),
		LastRuntimeObservedAt: formatTime(summary.LastRuntimeObservedAt),
		UpdatedAt:             formatTime(updatedAt),
	}
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
