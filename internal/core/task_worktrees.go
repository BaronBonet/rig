package core

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// taskWorktrees derives the worktrees a Task is working in from the file edits
// its provider sessions made. Edits come from provider transcripts and branches
// come from the worktrees as they are now; the only durable state is, per
// worktree, the branch it had when the Task's latest edit there was first
// observed. A worktree that has since checked out another branch was most
// likely reused for other work, and the view says so instead of claiming it.
type taskWorktrees struct {
	tasks       TaskRepository
	gitWorktree GitWorktreeClient
	providers   map[Provider]ProviderClient
}

func newTaskWorktrees(
	tasks TaskRepository,
	gitWorktree GitWorktreeClient,
	providers map[Provider]ProviderClient,
) *taskWorktrees {
	return &taskWorktrees{tasks: tasks, gitWorktree: gitWorktree, providers: providers}
}

type editedWorktree struct {
	lastEditAt time.Time
	root       string
	editCount  int
}

func (w *taskWorktrees) ListTaskWorktrees(ctx context.Context, taskID string) ([]TaskWorktree, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}

	sessions, err := w.tasks.ListTaskProviderSessions(ctx, taskID)
	if err != nil {
		return nil, err
	}
	edited, err := w.editedWorktrees(ctx, latestProviderSessionsByID(sessions))
	if err != nil || len(edited) == 0 {
		return nil, err
	}

	records, err := w.tasks.ListTaskWorktreeRecords(ctx, taskID)
	if err != nil {
		return nil, err
	}
	recorded := make(map[string]TaskWorktreeRecord, len(records))
	for _, record := range records {
		recorded[record.WorktreePath] = record
	}

	worktrees := make([]TaskWorktree, 0, len(edited))
	for _, edit := range edited {
		ref, err := w.gitWorktree.InspectWorktree(ctx, edit.root)
		if err != nil {
			return nil, fmt.Errorf("inspect worktree %q: %w", edit.root, err)
		}
		if ref == nil {
			continue
		}

		record, known := recorded[edit.root]
		if !known || edit.lastEditAt.After(record.LastEditAt) || edit.editCount > record.EditCount {
			// A newly observed edit pins the branch the worktree has now.
			record = TaskWorktreeRecord{
				LastEditAt:   edit.lastEditAt,
				TaskID:       taskID,
				WorktreePath: edit.root,
				RepoName:     ref.RepoName,
				Branch:       ref.Branch,
				EditCount:    edit.editCount,
			}
			if err := w.tasks.UpsertTaskWorktreeRecord(ctx, record); err != nil {
				return nil, err
			}
		}

		worktrees = append(worktrees, TaskWorktree{
			LastEditAt:   edit.lastEditAt,
			WorktreePath: edit.root,
			RepoName:     ref.RepoName,
			Branch:       ref.Branch,
			EditedBranch: record.Branch,
			EditCount:    edit.editCount,
		})
	}

	slices.SortFunc(worktrees, func(a, b TaskWorktree) int {
		if byTime := b.LastEditAt.Compare(a.LastEditAt); byTime != 0 {
			return byTime
		}
		return cmp.Compare(a.WorktreePath, b.WorktreePath)
	})
	if len(worktrees) == 0 {
		return nil, nil
	}

	return worktrees, nil
}

// editedWorktrees groups the sessions' file edits by the worktree they landed
// in. Edits outside any existing worktree (scratch files, deleted worktrees) are
// dropped.
func (w *taskWorktrees) editedWorktrees(
	ctx context.Context,
	sessions []TaskProviderSession,
) ([]editedWorktree, error) {
	byRoot := make(map[string]*editedWorktree)
	rootByDir := make(map[string]string)
	for _, session := range sessions {
		providerClient, err := supportedProviderClient(w.providers, session.Provider)
		if err != nil {
			continue
		}
		changes, err := providerClient.ReadSessionFileChanges(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("read session file changes %q: %w", session.TranscriptPath, err)
		}

		for _, change := range changes {
			dir := filepath.Dir(filepath.Clean(change.Path))
			root, seen := rootByDir[dir]
			if !seen {
				root = w.gitWorktree.WorktreeRootOf(dir)
				rootByDir[dir] = root
			}
			if root == "" {
				continue
			}

			edit := byRoot[root]
			if edit == nil {
				edit = &editedWorktree{root: root}
				byRoot[root] = edit
			}
			edit.editCount++
			if change.ObservedAt.After(edit.lastEditAt) {
				edit.lastEditAt = change.ObservedAt
			}
		}
	}

	edited := make([]editedWorktree, 0, len(byRoot))
	for _, edit := range byRoot {
		edited = append(edited, *edit)
	}
	return edited, nil
}
