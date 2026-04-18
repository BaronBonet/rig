package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	repositoryagentconfig "rig/internal/adapters/repository/agentconfig"
	repositorysetupscript "rig/internal/adapters/repository/setupscript"
	"rig/internal/core"
)

type Preparer struct {
	configLoader core.RepoConfigLoader
	seeder       core.WorkspaceSeeder
	setupRunner  core.SetupScriptRunner
}

func NewPreparer() *Preparer {
	return &Preparer{
		configLoader: repositoryagentconfig.NewLoader(),
		seeder:       NewSeeder(),
		setupRunner:  repositorysetupscript.NewRunner(),
	}
}

func (p *Preparer) PrepareTaskWorkspace(ctx context.Context, task *core.Task, repoRoot string, bootstrapSpec core.WorkspaceBootstrapSpec) error {
	if task == nil {
		return nil
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = task.RepoRoot
	}

	repoConfig, err := p.configLoader.LoadRepoConfig(ctx, repoRoot)
	if err != nil {
		return err
	}

	if len(repoConfig.Seed.Copy) > 0 {
		if err := p.seeder.ValidateSeedPaths(ctx, repoRoot, repoConfig.Seed.Copy); err != nil {
			return fmt.Errorf("seed workspace: %w", err)
		}
		if err := p.seeder.SeedWorkspace(ctx, core.SeedWorkspaceInput{
			RepoRoot:      repoRoot,
			WorktreePath:  task.WorktreePath,
			RelativePaths: repoConfig.Seed.Copy,
		}, nil); err != nil {
			return fmt.Errorf("seed workspace: %w", err)
		}
	}

	if repoConfig.Seed.SetupScript != "" {
		if err := p.setupRunner.ValidateSetupScript(ctx, repoRoot, repoConfig.Seed.SetupScript); err != nil {
			return fmt.Errorf("setup script: %w", err)
		}
		if err := p.setupRunner.RunSetupScript(ctx, core.RunSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: task.WorktreePath,
			ScriptPath:   repoConfig.Seed.SetupScript,
		}, nil); err != nil {
			return fmt.Errorf("setup script: %w", err)
		}
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
