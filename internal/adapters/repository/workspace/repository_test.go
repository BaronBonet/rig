package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"agent/internal/core"

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

	repo := NewRepository()
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
	requireModeBits(t, filepath.Join(worktreePath, ".env"), 0o600)

	configBody, err := os.ReadFile(filepath.Join(worktreePath, "local", "config.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"agent"}`, string(configBody))
	requireModeBits(t, filepath.Join(worktreePath, "local", "config.json"), 0o644)

	setupBody, err := os.ReadFile(filepath.Join(worktreePath, "local", "scripts", "setup.sh"))
	require.NoError(t, err)
	require.Equal(t, "#!/bin/sh\necho setup\n", string(setupBody))
	requireModeBits(t, filepath.Join(worktreePath, "local", "scripts", "setup.sh"), 0o755)
}

func TestRepositoryValidateSeedPathsDoesNotMutateWorkspace(t *testing.T) {
	repoRoot := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("API_KEY=1\n"), 0o600))
	sentinel := filepath.Join(repoRoot, "created-by-validation")
	require.NoError(t, os.RemoveAll(sentinel))

	repo := NewRepository()

	err := repo.ValidateSeedPaths(context.Background(), repoRoot, []string{".env"})
	require.NoError(t, err)

	_, err = os.Stat(sentinel)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))

	entries, err := os.ReadDir(repoRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, ".env", entries[0].Name())
}

func TestRepositoryValidateSeedPathsRejectsOverlappingPaths(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "local"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "local", "config.json"), []byte(`{"ok":true}`), 0o644))

	repo := NewRepository()

	err := repo.ValidateSeedPaths(context.Background(), repoRoot, []string{"local/", "local/config.json"})
	require.Error(t, err)
	require.ErrorContains(t, err, "overlaps with")
}

func TestRepositorySeedWorkspaceRejectsMissingSource(t *testing.T) {
	repo := NewRepository()

	err := repo.SeedWorkspace(context.Background(), core.SeedWorkspaceInput{
		RepoRoot:      t.TempDir(),
		WorktreePath:  t.TempDir(),
		RelativePaths: []string{"missing.txt"},
	}, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "missing.txt")
}

func TestRepositorySeedWorkspaceRejectsExistingDestination(t *testing.T) {
	repoRoot := t.TempDir()
	worktreePath := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("API_KEY=1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, ".env"), []byte("already here\n"), 0o644))

	repo := NewRepository()

	err := repo.SeedWorkspace(context.Background(), core.SeedWorkspaceInput{
		RepoRoot:      repoRoot,
		WorktreePath:  worktreePath,
		RelativePaths: []string{".env"},
	}, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "already exists")
}

func TestRepositorySeedWorkspaceRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior depends on local privileges on Windows")
	}

	repoRoot := t.TempDir()
	worktreePath := t.TempDir()

	target := filepath.Join(repoRoot, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("secret\n"), 0o600))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(repoRoot, "linked.txt")))

	repo := NewRepository()

	err := repo.SeedWorkspace(context.Background(), core.SeedWorkspaceInput{
		RepoRoot:      repoRoot,
		WorktreePath:  worktreePath,
		RelativePaths: []string{"linked.txt"},
	}, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "symlink")
}

func TestRepositorySeedWorkspaceRejectsSymlinkedSourceParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior depends on local privileges on Windows")
	}

	repoRoot := t.TempDir()
	worktreePath := t.TempDir()
	targetDir := filepath.Join(repoRoot, "real-dir")
	require.NoError(t, os.MkdirAll(targetDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "config.json"), []byte(`{"ok":true}`), 0o644))
	require.NoError(t, os.Symlink("real-dir", filepath.Join(repoRoot, "link-dir")))

	repo := NewRepository()

	err := repo.SeedWorkspace(context.Background(), core.SeedWorkspaceInput{
		RepoRoot:      repoRoot,
		WorktreePath:  worktreePath,
		RelativePaths: []string{"link-dir/config.json"},
	}, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "symlink")
}

func TestRepositorySeedWorkspaceRejectsSymlinkedDestinationParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior depends on local privileges on Windows")
	}

	repoRoot := t.TempDir()
	worktreePath := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "subdir", "config.json"), []byte(`{"ok":true}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(worktreePath, "subdir"), 0o755))
	require.NoError(t, os.Remove(filepath.Join(worktreePath, "subdir")))
	require.NoError(t, os.Symlink(outside, filepath.Join(worktreePath, "subdir")))

	repo := NewRepository()

	err := repo.SeedWorkspace(context.Background(), core.SeedWorkspaceInput{
		RepoRoot:      repoRoot,
		WorktreePath:  worktreePath,
		RelativePaths: []string{"subdir/config.json"},
	}, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "symlink")
	_, statErr := os.Stat(filepath.Join(outside, "config.json"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}

func TestRepositorySeedWorkspaceRejectsNestedDirectorySymlinkWithoutMutatingDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior depends on local privileges on Windows")
	}

	repoRoot := t.TempDir()
	worktreePath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "local", "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "local", "config.json"), []byte(`{"ok":true}`), 0o644))
	require.NoError(t, os.Symlink("../config.json", filepath.Join(repoRoot, "local", "nested", "link.json")))

	repo := NewRepository()

	err := repo.SeedWorkspace(context.Background(), core.SeedWorkspaceInput{
		RepoRoot:      repoRoot,
		WorktreePath:  worktreePath,
		RelativePaths: []string{"local/"},
	}, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "symlink")
	_, statErr := os.Stat(filepath.Join(worktreePath, "local"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}

func TestRepositorySeedWorkspaceRejectsEscapingPaths(t *testing.T) {
	repo := NewRepository()

	err := repo.ValidateSeedPaths(context.Background(), t.TempDir(), []string{"../outside"})
	require.Error(t, err)
	require.ErrorContains(t, err, "escape")
}

func requireModeBits(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm())
}
