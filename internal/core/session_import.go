package core

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// importableSessionLimit caps how many recent sessions each provider offers
// for import; older sessions are rarely worth resuming.
const importableSessionLimit = 30

func (s *service) ListImportableSessions(ctx context.Context, folder string) ([]ProviderSessionSummary, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return nil, nil
	}
	setup, err := s.GetProviderSetup(ctx)
	if err != nil {
		return nil, err
	}
	if setup == nil {
		return nil, ErrProviderSetupRequired
	}

	owned, err := s.ownedProviderSessionIDs(ctx)
	if err != nil {
		return nil, err
	}

	var sessions []ProviderSessionSummary
	for _, provider := range configuredProvidersInOrder(*setup) {
		providerClient, err := supportedProviderClient(s.providers, provider)
		if err != nil {
			continue
		}
		found, err := providerClient.ListFolderSessions(ctx, folder, importableSessionLimit)
		if err != nil {
			return nil, fmt.Errorf("list %s sessions: %w", provider, err)
		}
		for _, session := range found {
			if !owned[providerSessionKey(session.Provider, session.SessionID)] {
				sessions = append(sessions, session)
			}
		}
	}

	slices.SortStableFunc(sessions, func(a, b ProviderSessionSummary) int {
		return b.LastActiveAt.Compare(a.LastActiveAt)
	})
	return sessions, nil
}

func (s *service) ImportSession(ctx context.Context, session ProviderSessionSummary) (*Task, error) {
	session.SessionID = strings.TrimSpace(session.SessionID)
	if session.SessionID == "" {
		return nil, fmt.Errorf("import session: session ID is required")
	}
	folder := strings.TrimSpace(session.Cwd)
	if !filepath.IsAbs(folder) {
		return nil, fmt.Errorf("import session: absolute session folder required, got %q", folder)
	}
	folder = filepath.Clean(folder)
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("import session: folder %q is not available", folder)
	}
	if _, _, err := s.launcher.resolveProvider(ctx, session.Provider); err != nil {
		return nil, err
	}

	owned, err := s.ownedProviderSessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	if owned[providerSessionKey(session.Provider, session.SessionID)] {
		return nil, fmt.Errorf("import session: %s session %s already belongs to a task", session.Provider,
			session.SessionID)
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	label := filepath.Base(folder)
	title := cmp.Or(strings.TrimSpace(session.Title), "imported "+string(session.Provider)+" session")
	task := newFolderTaskRecord(folder, label, session.Provider, title,
		uniqueFolderTaskSlug(folder, label, title, existingTasks))
	task.CreationStatus = TaskCreationStatusReady
	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if err := s.tasks.UpsertTaskProviderSession(ctx, TaskProviderSession{
		FirstObservedAt:   cmp.Or(session.LastActiveAt, now),
		LastObservedAt:    now,
		TaskID:            task.ID,
		Provider:          session.Provider,
		ProviderSessionID: session.SessionID,
		TranscriptPath:    session.TranscriptPath,
		StartSource:       "import",
		Cwd:               folder,
	}); err != nil {
		return task, fmt.Errorf("record imported session: %w", err)
	}
	if err := s.tasks.UpsertTaskResumeMetadata(ctx, TaskResumeMetadata{
		ObservedAt: now,
		TaskID:     task.ID,
		SessionID:  session.SessionID,
		Provider:   session.Provider,
	}); err != nil {
		return task, fmt.Errorf("record imported session: %w", err)
	}

	// The imported session resumes exactly like a task whose tmux session was
	// lost: same bootstrap, same provider resume command.
	if err := s.reconnectTask(ctx, task); err != nil {
		return task, fmt.Errorf("imported, but its session did not start (enter retries): %w", err)
	}
	return task, nil
}

// ownedProviderSessionIDs returns the provider sessions that already belong to
// a task, keyed by providerSessionKey.
func (s *service) ownedProviderSessionIDs(ctx context.Context) (map[string]bool, error) {
	tasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	owned := make(map[string]bool)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		sessions, err := s.tasks.ListTaskProviderSessions(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			owned[providerSessionKey(session.Provider, session.ProviderSessionID)] = true
		}
	}
	return owned, nil
}

func providerSessionKey(provider Provider, sessionID string) string {
	return string(provider) + "/" + strings.TrimSpace(sessionID)
}
