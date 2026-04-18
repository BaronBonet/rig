package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRepositorySeedWorkspaceCopiesFilesAndDirectories(t *testing.T) {
	repoRoot := t.TempDir()
	worktreePath := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("API_KEY=1\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "local", "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "local", "config.json"), []byte(`{"name":"agent"}`), 0o644))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(repoRoot, "local", "scripts", "setup.sh"), []byte("#!/bin/sh\necho setup\n"), 0o755),
	)

	repo := NewSeeder()
	var copied []string

	err := repo.SeedWorkspace(context.Background(), core.SeedWorkspaceInput{
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

func TestRepositoryPrepareTaskWorkspaceWritesBootstrapFilesAndRunsSetup(t *testing.T) {
	repoRoot := t.TempDir()
	worktreePath := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".rig.yaml"), []byte("seed:\n  copy:\n    - .env\n  setup_script: setup.sh\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("API_KEY=1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "setup.sh"), []byte("#!/bin/sh\nset -eu\nprintf setup-ran > \"$PWD/setup-ran.txt\"\n"), 0o755))

	preparer := New()
	err := preparer.PrepareTaskWorkspace(context.Background(), &core.Task{
		WorktreePath: worktreePath,
		RepoRoot:     repoRoot,
	}, repoRoot, core.WorkspaceBootstrapSpec{Files: []core.WorkspaceBootstrapFile{
		{
			Path:     ".codex/hooks/hooks.json",
			Content:  []byte("{\"hooks\":{}}\n"),
			FileMode: 0o640,
		},
		{
			Path:     ".claude/settings.local.json",
			Content:  []byte("{\"hooks\":{}}\n"),
			FileMode: 0o600,
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

	settingsInfo, err := os.Stat(filepath.Join(worktreePath, ".claude", "settings.local.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), settingsInfo.Mode().Perm())
}
