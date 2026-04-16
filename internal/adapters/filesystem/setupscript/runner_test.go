package setupscript

import (
	"os"
	"path/filepath"
	"testing"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRunner_ValidateSetupScript(t *testing.T) {
	t.Run("valid script passes validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte("#!/bin/bash\necho hi"), 0o755))

		runner := NewRunner()
		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.NoError(t, err)
	})

	t.Run("missing script fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		runner := NewRunner()
		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "not found")
	})

	t.Run("directory instead of file fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "scripts", "setup.sh"), 0o755))

		runner := NewRunner()
		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "not a file")
	})

	t.Run("symlink fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "real.sh"), []byte("#!/bin/bash"), 0o755))
		require.NoError(t, os.Symlink(filepath.Join(scriptDir, "real.sh"), filepath.Join(scriptDir, "setup.sh")))

		runner := NewRunner()
		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "symlink")
	})

	t.Run("script escaping repo root fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		runner := NewRunner()
		err := runner.ValidateSetupScript(t.Context(), repoRoot, "../escape.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "escapes")
	})
}

func TestRunner_RunSetupScript(t *testing.T) {
	t.Run("runs script in worktree directory and streams output", func(t *testing.T) {
		repoRoot := t.TempDir()
		worktreePath := t.TempDir()

		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(
			t,
			os.WriteFile(
				filepath.Join(scriptDir, "setup.sh"),
				[]byte("#!/bin/bash\necho \"line one\"\necho \"line two\"\npwd\n"),
				0o755,
			),
		)

		runner := NewRunner()
		var lines []string
		err := runner.RunSetupScript(t.Context(), core.RunSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: worktreePath,
			ScriptPath:   "scripts/setup.sh",
		}, func(line string) {
			lines = append(lines, line)
		})

		require.NoError(t, err)
		require.Contains(t, lines, "line one")
		require.Contains(t, lines, "line two")
		require.Contains(t, lines, worktreePath)
	})

	t.Run("returns error when script exits non-zero", func(t *testing.T) {
		repoRoot := t.TempDir()
		worktreePath := t.TempDir()

		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(
			t,
			os.WriteFile(
				filepath.Join(scriptDir, "setup.sh"),
				[]byte("#!/bin/bash\necho \"about to fail\"\nexit 1\n"),
				0o755,
			),
		)

		runner := NewRunner()
		var lines []string
		err := runner.RunSetupScript(t.Context(), core.RunSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: worktreePath,
			ScriptPath:   "scripts/setup.sh",
		}, func(line string) {
			lines = append(lines, line)
		})

		require.Error(t, err)
		require.ErrorContains(t, err, "exit status 1")
		require.ErrorContains(t, err, "about to fail")
		require.Contains(t, lines, "about to fail")
	})

	t.Run("script reads from repo root but runs in worktree", func(t *testing.T) {
		repoRoot := t.TempDir()
		worktreePath := t.TempDir()

		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte("#!/bin/bash\ntouch marker.txt\n"), 0o755),
		)

		runner := NewRunner()
		err := runner.RunSetupScript(t.Context(), core.RunSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: worktreePath,
			ScriptPath:   "scripts/setup.sh",
		}, func(string) {})

		require.NoError(t, err)
		_, err = os.Stat(filepath.Join(worktreePath, "marker.txt"))
		require.NoError(t, err, "marker.txt should be created in worktree")
	})
}
