package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRepositorySeedWorkspaceCopiesFilesAndDirectories(t *testing.T) {
	repoRoot := t.TempDir()
	worktreePath := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("API_KEY=1\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "local", "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "local", "config.json"), []byte(`{"name":"rig"}`), 0o644))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(repoRoot, "local", "scripts", "setup.sh"), []byte("#!/bin/sh\necho setup\n"), 0o755),
	)

	var copied []string

	err := seedWorkspace(context.Background(), seedWorkspaceInput{
		RepoRoot:      repoRoot,
		WorktreePath:  worktreePath,
		RelativePaths: []string{".env", "local/"},
	}, func(path string) {
		copied = append(copied, path)
	})
	require.NoError(t, err)
	require.Equal(t, []string{".env", "local/"}, copied)

	envBody, err := os.ReadFile(filepath.Join(worktreePath, ".env"))
	require.NoError(t, err)
	require.Equal(t, "API_KEY=1\n", string(envBody))
}

func TestRepositorySetupAndBootstrapTaskWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	worktreePath := t.TempDir()

	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(repoRoot, ".rig.yaml"),
			[]byte("seed:\n  copy:\n    - .env\n  setup_script: setup.sh\n"),
			0o644,
		),
	)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("API_KEY=1\n"), 0o600))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(repoRoot, "setup.sh"),
			[]byte("#!/bin/sh\nset -eu\nprintf setup-ran > \"$PWD/setup-ran.txt\"\n"),
			0o755,
		),
	)

	manager := New()
	err := manager.SetupTaskWorkspace(context.Background(), &core.Task{
		WorktreePath: worktreePath,
		RepoRoot:     repoRoot,
	}, repoRoot)
	require.NoError(t, err)

	err = manager.BootstrapTaskWorkspace(context.Background(), &core.Task{
		WorktreePath: worktreePath,
		RepoRoot:     repoRoot,
	}, core.WorkspaceBootstrapSpec{Files: []core.WorkspaceBootstrapFile{
		{
			Path:     ".codex/hooks/hooks.json",
			Content:  []byte("{\"hooks\":{}}\n"),
			FileMode: 0o640,
		},
	}})
	require.NoError(t, err)

	envBody, err := os.ReadFile(filepath.Join(worktreePath, ".env"))
	require.NoError(t, err)
	require.Equal(t, "API_KEY=1\n", string(envBody))

	setupBody, err := os.ReadFile(filepath.Join(worktreePath, "setup-ran.txt"))
	require.NoError(t, err)
	require.Equal(t, "setup-ran", string(setupBody))

	hooksInfo, err := os.Stat(filepath.Join(worktreePath, ".codex", "hooks", "hooks.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), hooksInfo.Mode().Perm())
}

func TestBootstrapTaskWorkspace_MergesIntoExistingFile(t *testing.T) {
	worktree := t.TempDir()
	manager := New()

	existingPath := filepath.Join(worktree, ".claude", "settings.local.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(existingPath), 0o755))
	require.NoError(t, os.WriteFile(existingPath, []byte(`{"permissions":{}}`), 0o600))

	err := manager.BootstrapTaskWorkspace(context.Background(), &core.Task{WorktreePath: worktree},
		core.WorkspaceBootstrapSpec{Files: []core.WorkspaceBootstrapFile{{
			Path:     ".claude/settings.local.json",
			Content:  []byte(`{"hooks":{}}`),
			FileMode: 0o600,
			Merge: func(existing []byte) ([]byte, error) {
				require.JSONEq(t, `{"permissions":{}}`, string(existing))
				return []byte(`{"permissions":{},"hooks":{}}`), nil
			},
		}}})

	require.NoError(t, err)
	written, err := os.ReadFile(existingPath)
	require.NoError(t, err)
	require.JSONEq(t, `{"permissions":{},"hooks":{}}`, string(written))
}

func TestBootstrapTaskWorkspace_MergeSkippedForNewFile(t *testing.T) {
	worktree := t.TempDir()
	manager := New()

	err := manager.BootstrapTaskWorkspace(context.Background(), &core.Task{WorktreePath: worktree},
		core.WorkspaceBootstrapSpec{Files: []core.WorkspaceBootstrapFile{{
			Path:     ".claude/settings.local.json",
			Content:  []byte(`{"hooks":{}}`),
			FileMode: 0o600,
			Merge: func([]byte) ([]byte, error) {
				t.Fatal("merge must not run when the destination does not exist")
				return nil, nil
			},
		}}})

	require.NoError(t, err)
	written, err := os.ReadFile(filepath.Join(worktree, ".claude", "settings.local.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"hooks":{}}`, string(written))
}

func TestBootstrapTaskWorkspace_RejectsMissingWorktree(t *testing.T) {
	manager := New()

	err := manager.BootstrapTaskWorkspace(context.Background(),
		&core.Task{WorktreePath: filepath.Join(t.TempDir(), "gone")},
		core.WorkspaceBootstrapSpec{Files: []core.WorkspaceBootstrapFile{{
			Path:    "file.txt",
			Content: []byte("x"),
		}}})

	require.ErrorContains(t, err, "task worktree unavailable")
}
