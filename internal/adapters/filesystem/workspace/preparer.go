package workspace

import (
	"context"
	"fmt"
	"strings"

	agentconfigfs "rig/internal/adapters/filesystem/agentconfig"
	codexhooksfs "rig/internal/adapters/filesystem/codexhooks"
	setupscriptfs "rig/internal/adapters/filesystem/setupscript"
	"rig/internal/core"
)

type Preparer struct {
	configLoader core.RepoConfigLoader
	seeder       core.WorkspaceSeeder
	bootstrapper core.TaskWorkspaceBootstrapper
	setupRunner  core.SetupScriptRunner
}

func NewPreparer(agentExec string, sourceRoot string) *Preparer {
	return &Preparer{
		configLoader: agentconfigfs.NewLoader(),
		seeder:       NewSeeder(),
		bootstrapper: codexhooksfs.NewBootstrapper(agentExec, sourceRoot),
		setupRunner:  setupscriptfs.NewRunner(),
	}
}

func (p *Preparer) PrepareTaskWorkspace(ctx context.Context, task *core.Task, repoRoot string) error {
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

	if p.bootstrapper != nil {
		if err := p.bootstrapper.BootstrapTaskWorkspace(ctx, task); err != nil {
			return err
		}
	}

	return nil
}
