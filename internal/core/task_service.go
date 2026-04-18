package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type TaskServiceDependencies struct {
	Tasks           TaskStore
	GitWorktree     GitWorktreeClient
	TmuxSession     TmuxSessionClient
	Agents          map[string]AgentClient
	Preparer        WorkspacePreparer
	DefaultProvider string
}

type taskService struct {
	tasks           TaskStore
	gitWorktree     GitWorktreeClient
	tmuxSession     TmuxSessionClient
	agents          map[string]AgentClient
	preparer        WorkspacePreparer
	defaultProvider string
}

func NewTaskService(deps TaskServiceDependencies) TaskService {
	return &taskService{
		tasks:           deps.Tasks,
		gitWorktree:     deps.GitWorktree,
		tmuxSession:     deps.TmuxSession,
		agents:          deps.Agents,
		preparer:        deps.Preparer,
		defaultProvider: deps.DefaultProvider,
	}
}

func (s *taskService) CreateTask(ctx context.Context, input CreateTaskInput) (*Task, error) {
	repoCtx, err := s.gitWorktree.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	if input.Source.PullRequest != nil {
		return s.createTaskFromPullRequest(ctx, repoCtx, input)
	}

	return s.createTaskFromPrompt(ctx, repoCtx, input)
}

type CreateTaskSource struct {
	PullRequest *RepoPullRequest
}

func (s *taskService) createTaskFromPrompt(
	ctx context.Context,
	repoCtx RepoContext,
	input CreateTaskInput,
) (*Task, error) {
	displayName, branchType, err := s.resolveTaskName(ctx, input)
	if err != nil {
		return nil, err
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	task := newTaskRecord(repoCtx, existingTasks, input.Provider, s.defaultProvider, displayName, branchType, "")
	task.Prompt = input.Prompt

	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.gitWorktree.CreateTaskWorkspace(ctx, task); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("create worktree: %w", err))
	}

	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	bootstrapSpec, err := s.buildWorkspaceBootstrapSpec(ctx, task)
	if err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("build workspace bootstrap spec: %w", err))
	}

	if s.preparer != nil {
		if err := s.preparer.PrepareTaskWorkspace(ctx, task, repoCtx.Root, bootstrapSpec); err != nil {
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

	inUseByWorktree, err := s.gitWorktree.IsBranchUsedByWorktree(ctx, repoCtx.Root, pr.BranchName)
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
		s.defaultProvider,
		prDisplayName(*pr),
		"",
		pr.BranchName,
	)
	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.gitWorktree.CreateTaskWorkspaceFromBranch(ctx, task); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("create worktree: %w", err))
	}

	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	return s.startTaskRuntime(ctx, task)
}

func (s *taskService) resolveTaskName(ctx context.Context, input CreateTaskInput) (string, string, error) {
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
	if agent, ok := s.agents[s.defaultProvider]; ok {
		return agent
	}
	for _, agent := range s.agents {
		return agent
	}
	return nil
}

func (s *taskService) startTaskRuntime(ctx context.Context, task *Task) (*Task, error) {
	agent := s.resolveAgent(string(task.Provider))
	if agent == nil {
		return s.markBroken(ctx, task, fmt.Errorf("build task session launch spec: provider %q unavailable", task.Provider))
	}

	launch, err := agent.BuildTaskSessionLaunchSpec(task)
	if err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("build task session launch spec: %w", err))
	}
	if err := s.tmuxSession.StartTaskSession(ctx, task, launch); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("start task session: %w", err))
	}

	task.Status = TaskStatusRunning
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	return task, nil
}

func (s *taskService) buildWorkspaceBootstrapSpec(ctx context.Context, task *Task) (WorkspaceBootstrapSpec, error) {
	agent := s.resolveAgent(string(task.Provider))
	if agent == nil {
		return WorkspaceBootstrapSpec{}, fmt.Errorf("provider %q unavailable", task.Provider)
	}

	return agent.BuildWorkspaceBootstrapSpec(task)
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
	if provider == "" {
		provider = defaultProvider
	}

	now := time.Now().UTC()
	taskID := fmt.Sprintf("%d", now.UnixNano())
	if branchName == "" {
		branchName = TaskSuggestion{BranchType: branchType}.BranchTypeOrDefault() + "/" + taskID
	}

	return &Task{
		ID:           taskID,
		DisplayName:  displayName,
		RepoRoot:     repoCtx.Root,
		RepoName:     repoCtx.Name,
		BranchName:   branchName,
		WorktreePath: filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskID),
		TmuxSession:  repoCtx.Name + "_" + taskID,
		Provider:     AgentProvider(provider),
		Status:       TaskStatusCreating,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func existingTaskForBranch(tasks []*Task, repoRoot string, branchName string) *Task {
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.RepoRoot == repoRoot && task.BranchName == branchName {
			return task
		}
	}
	return nil
}

func prDisplayName(pr RepoPullRequest) string {
	title := strings.TrimSpace(pr.Title)
	if title != "" {
		return title
	}
	return strings.TrimSpace(pr.BranchName)
}

func fallbackDisplayName(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "task"
	}

	fields := strings.Fields(prompt)
	if len(fields) > 6 {
		fields = fields[:6]
	}

	return strings.Join(fields, " ")
}
