package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

type preparer struct{}

func New() core.TaskWorkspaceManager {
	return &preparer{}
}

func (p *preparer) SetupTaskWorkspace(ctx context.Context, task *core.Task, repoRoot string) error {
	if task == nil {
		return nil
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = task.RepoRoot
	}

	repoConfig, err := loadRepoConfig(repoRoot)
	if err != nil {
		return err
	}

	if len(repoConfig.Seed.Copy) > 0 {
		if err := validateSeedPaths(ctx, repoRoot, repoConfig.Seed.Copy); err != nil {
			return fmt.Errorf("seed workspace: %w", err)
		}
		if err := seedWorkspace(ctx, seedWorkspaceInput{
			RepoRoot:      repoRoot,
			WorktreePath:  task.WorktreePath,
			RelativePaths: repoConfig.Seed.Copy,
		}, nil); err != nil {
			return fmt.Errorf("seed workspace: %w", err)
		}
	}

	if repoConfig.Seed.SetupScript != "" {
		if err := validateSetupScript(repoRoot, repoConfig.Seed.SetupScript); err != nil {
			return fmt.Errorf("setup script: %w", err)
		}
		if err := runSetupScript(ctx, runSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: task.WorktreePath,
			ScriptPath:   repoConfig.Seed.SetupScript,
		}, nil); err != nil {
			return fmt.Errorf("setup script: %w", err)
		}
	}

	return nil
}

func (p *preparer) BootstrapTaskWorkspace(
	_ context.Context,
	task *core.Task,
	bootstrapSpec core.WorkspaceBootstrapSpec,
) error {
	if task == nil {
		return nil
	}

	for _, file := range bootstrapSpec.Files {
		if err := writeBootstrapFile(task.WorktreePath, file); err != nil {
			return fmt.Errorf("write bootstrap file %s: %w", file.Path, err)
		}
	}

	return nil
}

func writeBootstrapFile(worktreePath string, file core.WorkspaceBootstrapFile) error {
	relPath := filepath.Clean(strings.TrimSpace(file.Path))
	if relPath == "." || relPath == string(filepath.Separator) || relPath == "" {
		return fmt.Errorf("invalid bootstrap file path")
	}
	if filepath.IsAbs(relPath) {
		return fmt.Errorf("bootstrap file path %q must be relative", file.Path)
	}

	absPath := filepath.Join(worktreePath, relPath)
	rel, err := filepath.Rel(worktreePath, absPath)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("bootstrap file path %q escapes worktree", file.Path)
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	mode := file.FileMode
	if mode == 0 {
		mode = 0o644
	}

	return os.WriteFile(absPath, file.Content, mode)
}
