package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"rig/internal/pkg/slug"
)

func (s *taskService) CreateTask(ctx context.Context, input CreateTaskInput) (*Task, error) {
	repoCtx, err := s.workspace.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	if input.Source.PullRequest != nil {
		return s.createTaskFromPullRequest(ctx, repoCtx, input)
	}

	return s.createTaskFromPrompt(ctx, repoCtx, input)
}

func (s *taskService) createTaskFromPrompt(
	ctx context.Context,
	repoCtx RepoContext,
	input CreateTaskInput,
) (*Task, error) {
	repoConfig, err := s.loadRepoConfig(ctx, repoCtx.Root)
	if err != nil {
		return nil, err
	}
	if len(repoConfig.Seed.Copy) > 0 && s.seeder != nil {
		if err := s.seeder.ValidateSeedPaths(ctx, repoCtx.Root, repoConfig.Seed.Copy); err != nil {
			return nil, fmt.Errorf("seed workspace: %w", err)
		}
	}
	if repoConfig.Seed.SetupScript != "" && s.setupRunner != nil {
		if err := s.setupRunner.ValidateSetupScript(ctx, repoCtx.Root, repoConfig.Seed.SetupScript); err != nil {
			return nil, fmt.Errorf("setup script: %w", err)
		}
	}

	displayName, branchType, err := s.resolveTaskName(ctx, input)
	if err != nil {
		return nil, err
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	task := newTaskRecord(repoCtx, existingTasks, input.Provider, s.cfg.Provider, displayName, branchType, "")
	task.Prompt = input.Prompt

	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.workspace.CreateTaskWorkspace(ctx, task); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("create worktree: %w", err))
	}

	task.WorktreeExists = true
	task.BranchExists = true
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	if len(repoConfig.Seed.Copy) > 0 && s.seeder != nil {
		if err := s.seeder.SeedWorkspace(ctx, SeedWorkspaceInput{
			RepoRoot:      task.RepoRoot,
			WorktreePath:  task.WorktreePath,
			RelativePaths: repoConfig.Seed.Copy,
		}, nil); err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("seed workspace: %w", err))
		}
	}

	if repoConfig.Seed.SetupScript != "" && s.setupRunner != nil {
		if err := s.setupRunner.RunSetupScript(ctx, RunSetupScriptInput{
			RepoRoot:     task.RepoRoot,
			WorktreePath: task.WorktreePath,
			ScriptPath:   repoConfig.Seed.SetupScript,
		}, nil); err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("setup script: %w", err))
		}
	}

	if s.bootstrap != nil {
		if err := s.bootstrap.BootstrapTaskWorkspace(ctx, task); err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("prepare workspace: %w", err))
		}
	}

	return s.startTaskRuntime(ctx, task)
}

func (s *taskService) createTaskFromPullRequest(
	ctx context.Context,
	repoCtx RepoContext,
	input CreateTaskInput,
) (*Task, error) {
	pr := input.Source.PullRequest
	if pr == nil {
		return nil, fmt.Errorf("pull request source is required")
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	if existingTaskForBranch(existingTasks, repoCtx.Root, pr.BranchName) != nil {
		return nil, fmt.Errorf("PR already has workspace")
	}

	inUseByWorktree, err := s.workspace.IsBranchUsedByWorktree(ctx, repoCtx.Root, pr.BranchName)
	if err != nil {
		return nil, err
	}
	if inUseByWorktree {
		return nil, fmt.Errorf("PR already has workspace")
	}

	task := newTaskRecord(
		repoCtx,
		existingTasks,
		input.Provider,
		s.cfg.Provider,
		prDisplayName(*pr),
		"",
		pr.BranchName,
	)
	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.workspace.CreateTaskWorkspaceFromBranch(ctx, task); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("create worktree: %w", err))
	}

	task.WorktreeExists = true
	task.BranchExists = true
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	return s.startTaskRuntime(ctx, task)
}

func (s *taskService) loadRepoConfig(ctx context.Context, repoRoot string) (RepoConfig, error) {
	if s.projectConfig == nil {
		return RepoConfig{}, nil
	}
	return s.projectConfig.LoadRepoConfig(ctx, repoRoot)
}

func (s *taskService) resolveTaskName(ctx context.Context, input CreateTaskInput) (string, string, error) {
	if strings.TrimSpace(input.ConfirmedDisplayName) != "" {
		return strings.TrimSpace(input.ConfirmedDisplayName), input.ConfirmedBranchType, nil
	}

	agent := s.resolveAgent(input.Provider)
	if agent == nil {
		return fallbackDisplayName(input.Prompt), "feat", nil
	}

	suggestion, err := agent.SuggestTaskName(ctx, input.Prompt)
	if err != nil || strings.TrimSpace(suggestion.Name) == "" {
		return fallbackDisplayName(input.Prompt), "feat", nil
	}

	return strings.TrimSpace(suggestion.Name), suggestion.BranchType, nil
}

func (s *taskService) resolveAgent(name string) AgentClient {
	if name != "" {
		if agent, ok := s.agents[name]; ok {
			return agent
		}
	}
	if agent, ok := s.agents[s.cfg.Provider]; ok {
		return agent
	}
	for _, agent := range s.agents {
		return agent
	}
	return nil
}

func (s *taskService) startTaskRuntime(ctx context.Context, task *Task) (*Task, error) {
	agent := s.resolveAgent(task.Provider)
	if agent == nil {
		return s.markBroken(ctx, task, fmt.Errorf("build launch request: provider %q unavailable", task.Provider))
	}

	launch, err := agent.LaunchRequest(task)
	if err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("build launch request: %w", err))
	}
	if err := writeSetupFiles(task.WorktreePath, launch.SetupFiles); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("write setup files: %w", err))
	}
	if err := s.runtime.StartTaskSession(ctx, task, launch); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("start task session: %w", err))
	}

	task.SessionExists = true
	task.AgentWindowExists = true
	task.EditorWindowExists = true
	task.Status = TaskStatusRunning
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	return task, nil
}

func (s *taskService) markBroken(ctx context.Context, task *Task, failure error) (*Task, error) {
	task.Status = TaskStatusBroken
	task.LastError = failure.Error()
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}
	return task, failure
}

func newTaskRecord(
	repoCtx RepoContext,
	existingTasks []*Task,
	provider string,
	defaultProvider string,
	displayName string,
	branchType string,
	branchName string,
) *Task {
	existingSlugs := make(map[string]struct{}, len(existingTasks))
	for _, task := range existingTasks {
		existingSlugs[task.Slug] = struct{}{}
	}

	if provider == "" {
		provider = defaultProvider
	}

	now := time.Now().UTC()
	taskSlug := slug.EnsureUnique(slug.FromDisplayName(displayName), existingSlugs)
	if branchName == "" {
		branchName = TaskSuggestion{BranchType: branchType}.BranchTypeOrDefault() + "/" + taskSlug
	}

	return &Task{
		ID:               fmt.Sprintf("%d", now.UnixNano()),
		DisplayName:      displayName,
		Slug:             taskSlug,
		RepoRoot:         repoCtx.Root,
		RepoName:         repoCtx.Name,
		BaseBranch:       repoCtx.BaseBranch,
		BranchName:       branchName,
		WorktreePath:     filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug),
		TmuxSession:      repoCtx.Name + "_" + taskSlug,
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         provider,
		Status:           TaskStatusCreating,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}
