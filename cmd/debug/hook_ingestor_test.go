package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"rig/internal/core"
)

type stubTaskStore struct {
	getTaskFn   func(id string) (*core.Task, error)
	listTasksFn func() ([]*core.Task, error)
}

func (s stubTaskStore) CreateTask(_ context.Context, _ *core.Task) error { return nil }
func (s stubTaskStore) UpdateTask(_ context.Context, _ *core.Task) error { return nil }
func (s stubTaskStore) GetTask(_ context.Context, id string) (*core.Task, error) {
	if s.getTaskFn == nil {
		return nil, nil
	}
	return s.getTaskFn(id)
}
func (s stubTaskStore) ListTasks(_ context.Context) ([]*core.Task, error) {
	if s.listTasksFn == nil {
		return nil, nil
	}
	return s.listTasksFn()
}

func TestDebugHookIngestor_RequiresTaskID(t *testing.T) {
	ingestor := newDebugHookIngestor(stubTaskStore{})

	_, err := ingestor.IngestHookEvent(t.Context(), core.HookEventInput{
		EventName:  "UserPromptSubmit",
		OccurredAt: time.Now().UTC(),
	})
	if !errors.Is(err, core.ErrUnmanagedHookEvent) {
		t.Fatalf("expected ErrUnmanagedHookEvent, got %v", err)
	}
}

func TestDebugHookIngestor_MapsRawInputToHookSummary(t *testing.T) {
	ingestor := newDebugHookIngestor(stubTaskStore{})
	now := time.Date(2026, time.April, 19, 12, 0, 0, 0, time.UTC)

	summary, err := ingestor.IngestHookEvent(t.Context(), core.HookEventInput{
		TaskID:         "task-1",
		EventName:      "SessionStart",
		Provider:       string(core.AgentProviderCodex),
		SessionID:      "session-1",
		TurnID:         "turn-1",
		Cwd:            "/tmp/repo",
		Model:          "gpt-5.4",
		OccurredAt:     now,
		PromptText:     "do the task",
		CommandText:    "echo hi",
		StartSource:    "startup",
		TranscriptPath: "/tmp/transcript.jsonl",
	})
	if err != nil {
		t.Fatalf("ingest hook event: %v", err)
	}

	if summary.TaskID != "task-1" || summary.LastEventName != "SessionStart" || !summary.StartedAt.Equal(now) {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if !summary.LastActivityAt.Equal(now) {
		t.Fatalf("expected LastActivityAt=%s, got %s", now, summary.LastActivityAt)
	}
}

func TestDebugHookIngestor_ResolvesTaskIDFromWorktreePath(t *testing.T) {
	ingestor := newDebugHookIngestor(stubTaskStore{
		listTasksFn: func() ([]*core.Task, error) {
			return []*core.Task{{
				ID:           "task-1",
				WorktreePath: "/tmp/repo-one",
			}}, nil
		},
	})
	now := time.Date(2026, time.April, 19, 12, 0, 0, 0, time.UTC)

	summary, err := ingestor.IngestHookEvent(t.Context(), core.HookEventInput{
		EventName:  "UserPromptSubmit",
		OccurredAt: now,
		Cwd:        "/tmp/repo-one",
	})
	if err != nil {
		t.Fatalf("ingest hook event: %v", err)
	}
	if summary.TaskID != "task-1" {
		t.Fatalf("expected task-1, got %#v", summary)
	}
}
