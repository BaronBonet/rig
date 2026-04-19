package main

import (
	"context"
	"fmt"
	"strings"

	"rig/internal/core"
)

type debugHookIngestor struct {
	tasks core.TaskStore
}

func newDebugHookIngestor(tasks core.TaskStore) *debugHookIngestor {
	return &debugHookIngestor{tasks: tasks}
}

func (d *debugHookIngestor) IngestHookEvent(ctx context.Context, raw core.HookEventInput) (*core.HookSessionSummary, error) {
	taskID := strings.TrimSpace(raw.TaskID)
	if taskID == "" {
		resolvedTaskID, err := d.resolveTaskID(ctx, strings.TrimSpace(raw.Cwd))
		if err != nil {
			return nil, err
		}
		taskID = resolvedTaskID
	}

	summary := &core.HookSessionSummary{
		TaskID:                taskID,
		SessionID:             strings.TrimSpace(raw.SessionID),
		Provider:              strings.TrimSpace(raw.Provider),
		Model:                 strings.TrimSpace(raw.Model),
		Cwd:                   strings.TrimSpace(raw.Cwd),
		TranscriptPath:        strings.TrimSpace(raw.TranscriptPath),
		StartSource:           strings.TrimSpace(raw.StartSource),
		CurrentTurnID:         strings.TrimSpace(raw.TurnID),
		LastEventName:         strings.TrimSpace(raw.EventName),
		LastActivityAt:        raw.OccurredAt,
		LastPromptSubmittedAt: raw.OccurredAt,
		LastPromptText:        strings.TrimSpace(raw.PromptText),
		LastCommandText:       strings.TrimSpace(raw.CommandText),
		LastCommandResultText: strings.TrimSpace(raw.CommandResultText),
		LastAssistantMessage:  strings.TrimSpace(raw.LastAssistantMessage),
	}
	if summary.LastEventName == "SessionStart" {
		summary.StartedAt = raw.OccurredAt
	}
	if summary.LastEventName == "Stop" {
		summary.LastStopAt = raw.OccurredAt
	}

	return summary, nil
}

func (d *debugHookIngestor) resolveTaskID(ctx context.Context, cwd string) (string, error) {
	if d.tasks == nil {
		return "", core.ErrUnmanagedHookEvent
	}
	if cwd == "" {
		return "", core.ErrUnmanagedHookEvent
	}

	tasks, err := d.tasks.ListTasks(ctx)
	if err != nil {
		return "", fmt.Errorf("list tasks for debug hook resolution: %w", err)
	}
	for _, task := range tasks {
		if task != nil && strings.TrimSpace(task.WorktreePath) == cwd {
			return strings.TrimSpace(task.ID), nil
		}
	}

	return "", core.ErrUnmanagedHookEvent
}
