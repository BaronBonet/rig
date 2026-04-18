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
	DefaultProvider AgentProvider
}

type taskService struct {
	tasks           TaskStore
	gitWorktree     GitWorktreeClient
	tmuxSession     TmuxSessionClient
	agents          map[string]AgentClient
	preparer        WorkspacePreparer
	defaultProvider AgentProvider
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
	agent, err := s.agentFor(input.Provider)
	if err != nil {
		return nil, err
	}

	suggestion, err := s.suggestTaskName(ctx, agent, input.Prompt)
	if err != nil {
		return nil, err
	}

	task := newPromptTaskRecord(repoCtx, input.Provider, s.defaultProvider, suggestion.Name, suggestion.BranchType)
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

	if err := s.preparer.PrepareTaskWorkspace(ctx, task, repoCtx.Root, bootstrapSpec); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("prepare workspace: %w", err))
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

	task := newPullRequestTaskRecord(
		repoCtx,
		input.Provider,
		s.defaultProvider,
		prDisplayName(*pr),
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

func (s *taskService) suggestTaskName(ctx context.Context, agent AgentClient, prompt string) (TaskSuggestion, error) {
	suggestion, err := agent.SuggestTaskName(ctx, prompt)
	if err != nil {
		return TaskSuggestion{}, fmt.Errorf("suggest task name: %w", err)
	}

	suggestion.Name = strings.TrimSpace(suggestion.Name)
	if suggestion.Name == "" {
		return TaskSuggestion{}, fmt.Errorf("suggest task name: empty task name")
	}

	return suggestion, nil
}

func (s *taskService) agentFor(provider string) (AgentClient, error) {
	if provider == "" {
		provider = string(s.defaultProvider)
	}

	agent, ok := s.agents[provider]
	if !ok {
		return nil, fmt.Errorf("agent provider %q unavailable", provider)
	}

	return agent, nil
}

func (s *taskService) startTaskRuntime(ctx context.Context, task *Task) (*Task, error) {
	agent, err := s.agentFor(string(task.Provider))
	if err != nil {
		return s.markBroken(ctx, task, err)
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
	agent, err := s.agentFor(string(task.Provider))
	if err != nil {
		return WorkspaceBootstrapSpec{}, err
	}

	return agent.BuildWorkspaceBootstrapSpec(task)
}

func (s *taskService) markBroken(ctx context.Context, task *Task, failure error) (*Task, error) {
	task.Status = TaskStatusBroken
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}
	return task, failure
}

func newPromptTaskRecord(
	repoCtx RepoContext,
	provider string,
	defaultProvider AgentProvider,
	displayName string,
	branchType string,
) *Task {
	if provider == "" {
		provider = string(defaultProvider)
	}

	now := time.Now().UTC()
	taskID := fmt.Sprintf("%d", now.UnixNano())

	return &Task{
		ID:           taskID,
		DisplayName:  displayName,
		RepoRoot:     repoCtx.Root,
		RepoName:     repoCtx.Name,
		BranchName:   branchNameForTask(taskID, branchType),
		WorktreePath: filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskID),
		TmuxSession:  repoCtx.Name + "_" + taskID,
		Provider:     AgentProvider(provider),
		Status:       TaskStatusCreating,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func newPullRequestTaskRecord(
	repoCtx RepoContext,
	provider string,
	defaultProvider AgentProvider,
	displayName string,
	branchName string,
) *Task {
	if provider == "" {
		provider = string(defaultProvider)
	}

	now := time.Now().UTC()
	taskID := fmt.Sprintf("%d", now.UnixNano())

	return &Task{
		ID:           taskID,
		DisplayName:  displayName,
		RepoRoot:     repoCtx.Root,
		RepoName:     repoCtx.Name,
		BranchName:   strings.TrimSpace(branchName),
		WorktreePath: filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskID),
		TmuxSession:  repoCtx.Name + "_" + taskID,
		Provider:     AgentProvider(provider),
		Status:       TaskStatusCreating,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func branchNameForTask(taskID string, branchType string) string {
	return TaskSuggestion{BranchType: branchType}.BranchTypeOrDefault() + "/" + taskID
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
