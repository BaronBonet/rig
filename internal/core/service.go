package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"rig/internal/pkg/slug"
)

type DoctorResult struct {
	Notes    []string
	Failures []string
}

const runtimeSnapshotTimeout = 250 * time.Millisecond

type NewTaskInput struct {
	Cwd                  string
	Prompt               string
	ConfirmedDisplayName string
	ConfirmedBranchType  string
	Provider             string
}

type CreateTaskFromPRInput struct {
	RepoRoot string
	PR       RepoPullRequest
	Provider string
}

type CreateTaskOptions struct {
	OpenSession bool
}

type Service struct {
	tasks       TaskRepository
	hooks       HookObservabilityRepository
	observers   ObserverRuntimeRepository
	repo        RepoClient
	session     SessionClient
	providers   map[string]ProviderClient
	repoConfig  RepoConfigLoader
	workspace   WorkspaceSeeder
	bootstrap   TaskWorkspaceBootstrapper
	setupRunner SetupScriptRunner
	cfg         Config

	usageReader SessionUsageReader

	prChecker  PRStatusChecker
	prCacheTTL time.Duration
	prCache    map[string]prCacheEntry
	prCacheMu  sync.Mutex
}

type prCacheEntry struct {
	status    *PRStatus
	fetchedAt time.Time
}

func (s *Service) SuggestTaskName(ctx context.Context, prompt string, provider string) (TaskSuggestion, error) {
	repo := s.resolveProvider(provider)
	if repo == nil {
		return TaskSuggestion{Name: fallbackDisplayName(prompt), BranchType: "feat"}, nil
	}
	suggestion, err := repo.SuggestTaskName(ctx, prompt)
	if err == nil && strings.TrimSpace(suggestion.Name) != "" {
		suggestion.Name = strings.TrimSpace(suggestion.Name)
		return suggestion, nil
	}

	return TaskSuggestion{Name: fallbackDisplayName(prompt), BranchType: "feat"}, nil
}

func (s *Service) resolveProvider(name string) ProviderClient {
	if name != "" {
		if repo, ok := s.providers[name]; ok {
			return repo
		}
	}
	if repo, ok := s.providers[s.cfg.Provider]; ok {
		return repo
	}
	for _, repo := range s.providers {
		return repo
	}
	return nil
}

func (s *Service) CreateTaskWithProgress(
	ctx context.Context,
	input NewTaskInput,
	options CreateTaskOptions,
	progress func(TaskProgress),
) (*Task, error) {
	repoCtx, err := s.repo.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	repoConfig, err := s.repoConfig.LoadRepoConfig(ctx, repoCtx.Root)
	if err != nil {
		return nil, err
	}
	if len(repoConfig.Seed.Copy) > 0 {
		if err := s.workspace.ValidateSeedPaths(ctx, repoCtx.Root, repoConfig.Seed.Copy); err != nil {
			return nil, fmt.Errorf("seed workspace: %w", err)
		}
	}

	if repoConfig.Seed.SetupScript != "" && s.setupRunner != nil {
		if err := s.setupRunner.ValidateSetupScript(ctx, repoCtx.Root, repoConfig.Seed.SetupScript); err != nil {
			return nil, fmt.Errorf("setup script: %w", err)
		}
	}

	var suggestion TaskSuggestion
	if strings.TrimSpace(input.ConfirmedDisplayName) != "" {
		suggestion = TaskSuggestion{
			Name:       strings.TrimSpace(input.ConfirmedDisplayName),
			BranchType: input.ConfirmedBranchType,
		}
	} else {
		emitTaskProgress(progress, TaskProgress{
			Step:    TaskProgressNaming,
			Message: "Naming task...",
		})
		suggestion, err = s.SuggestTaskName(ctx, input.Prompt, input.Provider)
		if err != nil {
			return nil, err
		}
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	existingSlugs := make(map[string]struct{}, len(existingTasks))
	for _, task := range existingTasks {
		existingSlugs[task.Slug] = struct{}{}
	}

	provider := input.Provider
	if provider == "" {
		provider = s.cfg.Provider
	}

	now := time.Now().UTC()
	taskSlug := slug.EnsureUnique(slug.FromDisplayName(suggestion.Name), existingSlugs)
	task := &Task{
		ID:               fmt.Sprintf("%d", now.UnixNano()),
		Prompt:           input.Prompt,
		DisplayName:      suggestion.Name,
		Slug:             taskSlug,
		RepoRoot:         repoCtx.Root,
		RepoName:         repoCtx.Name,
		BaseBranch:       repoCtx.BaseBranch,
		BranchName:       suggestion.BranchTypeOrDefault() + "/" + taskSlug,
		WorktreePath:     filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug),
		TmuxSession:      repoCtx.Name + "_" + taskSlug,
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         provider,
		Status:           TaskStatusCreating,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressNameSelected,
		Message: fmt.Sprintf("Selected name: %s", task.DisplayName),
		Task:    cloneTask(task),
	})

	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressWorktreeCreating,
		Message: "Creating worktree...",
		Task:    cloneTask(task),
	})
	if err := s.repo.CreateTaskWorkspace(ctx, task); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("create worktree: %w", err))
	}

	task.WorktreeExists = true
	task.BranchExists = true
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	if len(repoConfig.Seed.Copy) > 0 {
		emitTaskProgress(progress, TaskProgress{
			Step:    TaskProgressWorkspaceSeeding,
			Message: "Seeding workspace...",
			Task:    cloneTask(task),
		})

		err := s.workspace.SeedWorkspace(ctx, SeedWorkspaceInput{
			RepoRoot:      task.RepoRoot,
			WorktreePath:  task.WorktreePath,
			RelativePaths: repoConfig.Seed.Copy,
		}, func(path string) {
			emitTaskProgress(progress, TaskProgress{
				Step:    TaskProgressWorkspaceSeeded,
				Message: fmt.Sprintf("Copied %s", path),
				Task:    cloneTask(task),
			})
		})
		if err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("seed workspace: %w", err))
		}
	}

	if repoConfig.Seed.SetupScript != "" && s.setupRunner != nil {
		emitTaskProgress(progress, TaskProgress{
			Step:    TaskProgressSetupScriptRunning,
			Message: "Running setup script...",
			Task:    cloneTask(task),
		})

		err := s.setupRunner.RunSetupScript(ctx, RunSetupScriptInput{
			RepoRoot:     task.RepoRoot,
			WorktreePath: task.WorktreePath,
			ScriptPath:   repoConfig.Seed.SetupScript,
		}, func(line string) {
			emitTaskProgress(progress, TaskProgress{
				Step:    TaskProgressSetupScriptRunning,
				Message: line,
				Task:    cloneTask(task),
			})
		})
		if err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("setup script: %w", err))
		}
	}

	if s.bootstrap != nil {
		if err := s.bootstrap.BootstrapTaskWorkspace(ctx, task); err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("prepare workspace: %w", err))
		}
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressTmuxStarting,
		Message: "Starting tmux session...",
		Task:    cloneTask(task),
	})
	providerRepo := s.resolveProvider(task.Provider)
	if providerRepo == nil {
		return s.markBroken(ctx, task, fmt.Errorf("build launch request: provider %q unavailable", task.Provider))
	}
	launch, err := providerRepo.LaunchRequest(task)
	if err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("build launch request: %w", err))
	}

	if err := writeSetupFiles(task.WorktreePath, launch.SetupFiles); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("write setup files: %w", err))
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressAgentLaunching,
		Message: fmt.Sprintf("Launching %s...", task.Provider),
		Task:    cloneTask(task),
	})

	if err := s.session.StartTaskSession(ctx, task, launch); err != nil {
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

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressTaskCreated,
		Message: fmt.Sprintf("Created task %s in session %s", task.DisplayName, task.TmuxSession),
		Task:    cloneTask(task),
	})

	if options.OpenSession {
		emitTaskProgress(progress, TaskProgress{
			Step:    TaskProgressSessionOpening,
			Message: "Opening tmux session...",
			Task:    cloneTask(task),
		})
		if err := s.session.OpenTaskSession(ctx, task); err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("open task session: %w", err))
		}
	}

	return task, nil
}

func (s *Service) ListRepoPullRequests(ctx context.Context, repoRoot string) ([]RepoPullRequest, error) {
	if s.prChecker == nil {
		return nil, fmt.Errorf("pr listing unavailable")
	}

	prs, err := s.prChecker.ListRepoPullRequests(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	annotated := make([]RepoPullRequest, 0, len(prs))
	for _, pr := range prs {
		inUseByTask := existingTaskForBranch(existingTasks, repoRoot, pr.BranchName) != nil
		inUseByWorktree, err := s.repo.IsBranchUsedByWorktree(ctx, repoRoot, pr.BranchName)
		if err != nil {
			return nil, err
		}
		pr.HasExistingTask = inUseByTask || inUseByWorktree
		annotated = append(annotated, pr)
	}

	return annotated, nil
}

func (s *Service) CreateTaskFromPRWithProgress(
	ctx context.Context,
	input CreateTaskFromPRInput,
	options CreateTaskOptions,
	progress func(TaskProgress),
) (*Task, error) {
	repoCtx, err := s.repo.DetectRepo(ctx, input.RepoRoot)
	if err != nil {
		return nil, err
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	if existingTaskForBranch(existingTasks, repoCtx.Root, input.PR.BranchName) != nil {
		return nil, fmt.Errorf("PR already has workspace")
	}
	inUseByWorktree, err := s.repo.IsBranchUsedByWorktree(ctx, repoCtx.Root, input.PR.BranchName)
	if err != nil {
		return nil, err
	}
	if inUseByWorktree {
		return nil, fmt.Errorf("PR already has workspace")
	}

	provider := input.Provider
	if provider == "" {
		provider = s.cfg.Provider
	}

	existingSlugs := make(map[string]struct{}, len(existingTasks))
	for _, task := range existingTasks {
		existingSlugs[task.Slug] = struct{}{}
	}

	now := time.Now().UTC()
	displayName := prDisplayName(input.PR)
	taskSlug := slug.EnsureUnique(slug.FromDisplayName(displayName), existingSlugs)
	task := &Task{
		ID:               fmt.Sprintf("%d", now.UnixNano()),
		Prompt:           "",
		DisplayName:      displayName,
		Slug:             taskSlug,
		RepoRoot:         repoCtx.Root,
		RepoName:         repoCtx.Name,
		BaseBranch:       repoCtx.BaseBranch,
		BranchName:       input.PR.BranchName,
		WorktreePath:     filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug),
		TmuxSession:      repoCtx.Name + "_" + taskSlug,
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         provider,
		Status:           TaskStatusCreating,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressWorktreeCreating,
		Message: "Creating worktree...",
		Task:    cloneTask(task),
	})
	if err := s.repo.CreateTaskWorkspaceFromBranch(ctx, task); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("create worktree: %w", err))
	}

	task.WorktreeExists = true
	task.BranchExists = true
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressTmuxStarting,
		Message: "Starting tmux session...",
		Task:    cloneTask(task),
	})
	providerRepo := s.resolveProvider(task.Provider)
	if providerRepo == nil {
		return s.markBroken(ctx, task, fmt.Errorf("build launch request: provider %q unavailable", task.Provider))
	}
	launch, err := providerRepo.LaunchRequest(task)
	if err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("build launch request: %w", err))
	}

	if err := writeSetupFiles(task.WorktreePath, launch.SetupFiles); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("write setup files: %w", err))
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressAgentLaunching,
		Message: fmt.Sprintf("Launching %s...", task.Provider),
		Task:    cloneTask(task),
	})

	if err := s.session.StartTaskSession(ctx, task, launch); err != nil {
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

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressTaskCreated,
		Message: fmt.Sprintf("Created task %s in session %s", task.DisplayName, task.TmuxSession),
		Task:    cloneTask(task),
	})

	if options.OpenSession {
		emitTaskProgress(progress, TaskProgress{
			Step:    TaskProgressSessionOpening,
			Message: "Opening tmux session...",
			Task:    cloneTask(task),
		})
		if err := s.session.OpenTaskSession(ctx, task); err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("open task session: %w", err))
		}
	}

	return task, nil
}

func (s *Service) ListTasks(ctx context.Context) ([]*Task, error) {
	tasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	reconciled := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		nextTask, reconcileErr := s.reconcileTask(ctx, task)
		if reconcileErr != nil {
			return nil, reconcileErr
		}
		if err := s.enrichRuntimeState(ctx, nextTask); err != nil {
			return nil, err
		}

		reconciled = append(reconciled, nextTask)
	}

	return reconciled, nil
}

func (s *Service) ListTasksByRepo(ctx context.Context, repoRoot string) ([]*Task, error) {
	tasks, err := s.tasks.ListTasksByRepo(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	reconciled := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		nextTask, reconcileErr := s.reconcileTask(ctx, task)
		if reconcileErr != nil {
			return nil, reconcileErr
		}
		if err := s.enrichRuntimeState(ctx, nextTask); err != nil {
			return nil, err
		}

		reconciled = append(reconciled, nextTask)
	}

	return reconciled, nil
}

func (s *Service) ListTaskViews(ctx context.Context) ([]*TaskView, error) {
	tasks, err := s.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	return s.buildTaskViews(ctx, tasks)
}

func (s *Service) ListTaskViewsByRepo(ctx context.Context, repoRoot string) ([]*TaskView, error) {
	tasks, err := s.ListTasksByRepo(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	return s.buildTaskViews(ctx, tasks)
}

func (s *Service) buildTaskViews(ctx context.Context, tasks []*Task) ([]*TaskView, error) {
	views := make([]*TaskView, 0, len(tasks))
	if len(tasks) == 0 {
		return views, nil
	}

	var err error
	var summaries map[string]*HookSessionSummary
	var observerSummaries map[string]*ObserverSummary
	if s.hooks != nil {
		taskIDs := make([]string, 0, len(tasks))
		for _, task := range tasks {
			if task == nil {
				continue
			}
			taskIDs = append(taskIDs, task.ID)
		}

		summaries, err = s.hooks.ListHookSessionSummaries(ctx, taskIDs)
		if err != nil {
			return nil, err
		}
	}
	if s.observers != nil {
		taskIDs := make([]string, 0, len(tasks))
		for _, task := range tasks {
			if task == nil {
				continue
			}
			taskIDs = append(taskIDs, task.ID)
		}

		observerSummaries, err = s.observers.ListObserverSummaries(ctx, taskIDs)
		if err != nil {
			return nil, err
		}
	}

	for _, task := range tasks {
		view := &TaskView{Task: task}
		if summaries != nil && task != nil {
			view.HookSession = summaries[task.ID]
		}
		if observerSummaries != nil && task != nil {
			view.Observer = observerSummaries[task.ID]
		}
		if s.usageReader != nil && task != nil && view.HookSession != nil {
			transcriptPath := strings.TrimSpace(view.HookSession.TranscriptPath)
			if transcriptPath != "" {
				usage, usageErr := s.usageReader.ReadSessionTokenUsage(ctx, task.Provider, transcriptPath)
				if usageErr != nil {
					return nil, usageErr
				}
				if usage != nil && !usage.IsZero() {
					view.TokenUsage = usage
				}
			}
		}
		views = append(views, view)
	}

	return views, nil
}

func (s *Service) GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]HookEvent, error) {
	if s.hooks == nil {
		return nil, nil
	}

	return s.hooks.ListHookEvents(ctx, taskID, limit)
}

func (s *Service) SubscribeTaskHookUpdates(ctx context.Context) (<-chan HookSessionSummary, func(), error) {
	if s.hooks == nil {
		ch := make(chan HookSessionSummary)
		close(ch)
		return ch, func() {}, nil
	}

	return s.hooks.SubscribeHookSessionUpdates(ctx)
}

func (s *Service) SubscribeTaskUpdates(ctx context.Context) (<-chan ObserverTaskUpdate, func(), error) {
	if s.observers == nil {
		ch := make(chan ObserverTaskUpdate)
		close(ch)
		return ch, func() {}, nil
	}

	return s.observers.SubscribeObserverTaskUpdates(ctx)
}

func (s *Service) GetTask(ctx context.Context, idOrSlug string) (*Task, error) {
	task, err := s.tasks.GetTask(ctx, idOrSlug)
	if err != nil {
		return nil, err
	}

	task, err = s.reconcileTask(ctx, task)
	if err != nil {
		return nil, err
	}
	if err := s.enrichRuntimeState(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

func (s *Service) OpenTask(ctx context.Context, idOrSlug string) error {
	task, err := s.GetTask(ctx, idOrSlug)
	if err != nil {
		return err
	}

	if task.Status == TaskStatusCleaned {
		return ErrCleanedTask
	}

	if task.Status == TaskStatusBroken {
		if err := s.restoreTaskSession(ctx, task); err != nil {
			return err
		}
	}

	if task.Status != TaskStatusRunning && task.Status != TaskStatusDegraded {
		return ErrBrokenTask
	}

	if !task.SessionExists || !task.AgentWindowExists {
		return ErrBrokenTask
	}

	return s.session.OpenTaskSession(ctx, task)
}

func (s *Service) restoreTaskSession(ctx context.Context, task *Task) error {
	if task == nil {
		return ErrBrokenTask
	}
	if !task.WorktreeExists || !task.BranchExists || task.SessionExists {
		return ErrBrokenTask
	}

	providerRepo := s.resolveProvider(task.Provider)
	if providerRepo == nil {
		return ErrBrokenTask
	}

	hookSession := s.lookupHookSessionSummary(ctx, task.ID)
	launch, err := restoreLaunchRequest(providerRepo, task, hookSession)
	if err != nil {
		_, markErr := s.markBroken(ctx, task, fmt.Errorf("build launch request: %w", err))
		return markErr
	}

	if err := writeSetupFiles(task.WorktreePath, launch.SetupFiles); err != nil {
		_, markErr := s.markBroken(ctx, task, fmt.Errorf("write setup files: %w", err))
		return markErr
	}

	if err := s.session.StartTaskSession(ctx, task, launch); err != nil {
		_, markErr := s.markBroken(ctx, task, fmt.Errorf("restore task session: %w", err))
		return markErr
	}

	task.SessionExists = true
	task.AgentWindowExists = true
	task.EditorWindowExists = true
	task.Status = TaskStatusRunning
	task.LastError = ""
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return err
	}

	return nil
}

func restoreLaunchRequest(
	providerRepo ProviderClient,
	task *Task,
	hookSession *HookSessionSummary,
) (LaunchRequest, error) {
	if restorer, ok := providerRepo.(RestoreLaunchProvider); ok {
		return restorer.RestoreLaunchRequest(task, hookSession)
	}

	return providerRepo.LaunchRequest(task)
}

func (s *Service) lookupHookSessionSummary(ctx context.Context, taskID string) *HookSessionSummary {
	if s.hooks == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}

	summaries, err := s.hooks.ListHookSessionSummaries(ctx, []string{taskID})
	if err != nil {
		return nil
	}

	return summaries[taskID]
}

func (s *Service) DeleteTaskResources(ctx context.Context, idOrSlug string) (*Task, error) {
	task, err := s.tasks.GetTask(ctx, idOrSlug)
	if err != nil {
		return nil, err
	}

	task, err = s.reconcileTask(ctx, task)
	if err != nil {
		return nil, err
	}

	if task.SessionExists {
		if err := s.session.DeleteTaskSession(ctx, task); err != nil {
			sessionResources, checkErr := s.session.InspectTaskSession(ctx, task)
			if checkErr != nil || sessionResources.SessionExists {
				return s.markCleanupBroken(ctx, task, fmt.Errorf("kill tmux session: %w", err))
			}
		}

		task.SessionExists = false
		task.AgentWindowExists = false
		task.EditorWindowExists = false
		task.UpdatedAt = time.Now().UTC()
		if err := s.tasks.UpdateTask(ctx, task); err != nil {
			return task, err
		}
	}

	if task.WorktreeExists {
		if err := s.repo.RemoveTaskWorkspace(ctx, task); err != nil {
			repoResources, checkErr := s.repo.InspectTaskWorkspace(ctx, task)
			if checkErr != nil || repoResources.WorktreeExists {
				return s.markCleanupBroken(ctx, task, fmt.Errorf("remove worktree: %w", err))
			}
		}

		task.WorktreeExists = false
		task.UpdatedAt = time.Now().UTC()
		if err := s.tasks.UpdateTask(ctx, task); err != nil {
			return task, err
		}
	}

	task.Status = TaskStatusCleaned
	task.LastError = ""
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	return task, nil
}

func (s *Service) GetPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error) {
	if s.prChecker == nil {
		return &PRStatus{State: PRStateNone}, nil
	}

	key := repoRoot + ":" + branchName
	ttl := s.prCacheTTL
	if ttl == 0 {
		ttl = time.Minute
	}

	s.prCacheMu.Lock()
	if s.prCache == nil {
		s.prCache = make(map[string]prCacheEntry)
	}
	if entry, ok := s.prCache[key]; ok && time.Since(entry.fetchedAt) < ttl {
		s.prCacheMu.Unlock()
		return entry.status, nil
	}
	s.prCacheMu.Unlock()

	status, err := s.prChecker.CheckPRStatus(ctx, repoRoot, branchName)
	if err != nil {
		return &PRStatus{State: PRStateNone}, nil
	}

	s.prCacheMu.Lock()
	if s.prCache == nil {
		s.prCache = make(map[string]prCacheEntry)
	}
	s.prCache[key] = prCacheEntry{status: status, fetchedAt: time.Now()}
	s.prCacheMu.Unlock()

	return status, nil
}

func (s *Service) InvalidatePRCache() {
	s.prCacheMu.Lock()
	s.prCache = nil
	s.prCacheMu.Unlock()
}

func (s *Service) SetSessionUsageReader(reader SessionUsageReader) {
	s.usageReader = reader
}

func (s *Service) SetPRStatusChecker(checker PRStatusChecker) {
	s.prChecker = checker
}

func NewService(
	tasks TaskRepository,
	hooks HookObservabilityRepository,
	observers ObserverRuntimeRepository,
	repo RepoClient,
	session SessionClient,
	providers map[string]ProviderClient,
	repoConfig RepoConfigLoader,
	workspace WorkspaceSeeder,
	bootstrap TaskWorkspaceBootstrapper,
	setupRunner SetupScriptRunner,
	cfg Config,
) *Service {
	return &Service{
		tasks:       tasks,
		hooks:       hooks,
		observers:   observers,
		repo:        repo,
		session:     session,
		providers:   providers,
		repoConfig:  repoConfig,
		workspace:   workspace,
		bootstrap:   bootstrap,
		setupRunner: setupRunner,
		cfg:         cfg,
	}
}

func (s *Service) Doctor(ctx context.Context, cwd string) (DoctorResult, error) {
	result := DoctorResult{}

	if err := s.tasks.IsAvailable(ctx); err != nil {
		result.Failures = append(result.Failures, "storage: "+err.Error())
	}

	if err := s.repo.IsAvailable(ctx); err != nil {
		result.Failures = append(result.Failures, "git: "+err.Error())
	}

	if err := s.session.IsAvailable(ctx); err != nil {
		result.Failures = append(result.Failures, "tmux: "+err.Error())
	}

	for name, repo := range s.providers {
		if err := repo.IsAvailable(ctx); err != nil {
			result.Failures = append(result.Failures, "provider("+name+"): "+err.Error())
		}
	}

	if s.prChecker != nil {
		if err := s.prChecker.IsAvailable(ctx); err != nil {
			result.Notes = append(result.Notes, "gh: gh CLI not found, PR status checks will be unavailable")
		}
	}

	if strings.TrimSpace(cwd) != "" {
		repoCtx, err := s.repo.DetectRepo(ctx, cwd)
		if err != nil {
			result.Failures = append(result.Failures, "repo: "+err.Error())
		} else {
			repoConfig, err := s.repoConfig.LoadRepoConfig(ctx, repoCtx.Root)
			if err != nil {
				result.Failures = append(result.Failures, "config: "+err.Error())
			} else if !repoConfig.Exists {
				result.Notes = append(result.Notes, "config: no .rig.yaml or rig.yaml found")
			} else {
				configFileName := repoConfig.ConfigFileName
				if configFileName == "" {
					configFileName = "rig.yaml"
				}
				result.Notes = append(result.Notes, "config: loaded "+configFileName)
				if err := s.workspace.ValidateSeedPaths(ctx, repoCtx.Root, repoConfig.Seed.Copy); err != nil {
					result.Failures = append(result.Failures, "config: "+err.Error())
				} else {
					for _, path := range repoConfig.Seed.Copy {
						result.Notes = append(result.Notes, "config: seed path ok: "+path)
					}
				}
				if repoConfig.Seed.SetupScript != "" && s.setupRunner != nil {
					scriptPath := repoConfig.Seed.SetupScript
					if err := s.setupRunner.ValidateSetupScript(ctx, repoCtx.Root, scriptPath); err != nil {
						result.Failures = append(result.Failures, "config: "+err.Error())
					} else {
						result.Notes = append(result.Notes, "config: setup script ok: "+scriptPath)
					}
				}
			}
		}
	}

	return result, nil
}

func (s *Service) reconcileTask(ctx context.Context, task *Task) (*Task, error) {
	reconciled := *task
	repoResources, err := s.repo.InspectTaskWorkspace(ctx, &reconciled)
	if err != nil {
		return nil, err
	}
	reconciled.WorktreeExists = repoResources.WorktreeExists
	reconciled.BranchExists = repoResources.BranchExists
	if reconciled.WorktreeExists && s.bootstrap != nil {
		_ = s.bootstrap.BootstrapTaskWorkspace(ctx, &reconciled)
	}

	sessionResources, err := s.session.InspectTaskSession(ctx, &reconciled)
	if err != nil {
		return nil, err
	}
	reconciled.SessionExists = sessionResources.SessionExists
	reconciled.AgentWindowExists = sessionResources.AgentWindowExists
	reconciled.EditorWindowExists = sessionResources.EditorWindowExists
	now := time.Now().UTC()
	reconciled.LastReconciledAt = now
	reconciled.UpdatedAt = now
	problems := make([]string, 0, 1)

	if task.Status == TaskStatusCleaned {
		if len(problems) == 0 && !reconciled.WorktreeExists && !reconciled.SessionExists {
			reconciled.Status = TaskStatusCleaned
			reconciled.LastError = ""
		} else {
			unexpected := append([]string{}, problems...)
			if reconciled.WorktreeExists {
				unexpected = append(unexpected, "unexpected worktree")
			}
			if reconciled.SessionExists {
				unexpected = append(unexpected, "unexpected tmux session")
			}
			if reconciled.AgentWindowExists {
				unexpected = append(unexpected, "unexpected tmux agent window")
			}
			if reconciled.EditorWindowExists {
				unexpected = append(unexpected, "unexpected tmux editor window")
			}

			reconciled.Status = TaskStatusBroken
			reconciled.LastError = strings.Join(unexpected, ", ")
		}
	} else {
		missing := append([]string{}, problems...)
		if !reconciled.WorktreeExists {
			missing = append(missing, "missing worktree")
		}
		if !reconciled.BranchExists {
			missing = append(missing, "missing branch")
		}
		if !reconciled.SessionExists {
			missing = append(missing, "missing tmux session")
		}
		if !reconciled.AgentWindowExists {
			missing = append(missing, "missing tmux agent window")
		}

		if len(missing) > 0 {
			reconciled.Status = TaskStatusBroken
			if task.Status == TaskStatusBroken && isCleanupFailure(task.LastError) {
				reconciled.LastError = task.LastError
			} else {
				reconciled.LastError = strings.Join(missing, ", ")
			}
		} else if !reconciled.EditorWindowExists {
			reconciled.Status = TaskStatusDegraded
			reconciled.LastError = "missing tmux editor window"
		} else {
			reconciled.Status = TaskStatusRunning
			reconciled.LastError = ""
		}
	}

	if err := s.tasks.UpdateTask(ctx, &reconciled); err != nil && !errors.Is(err, ErrTaskNotFound) {
		return nil, err
	}

	return &reconciled, nil
}

func (s *Service) enrichRuntimeState(ctx context.Context, task *Task) error {
	if task == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	task.RuntimeState = RuntimeStateNone
	task.RuntimeStateUpdatedAt = time.Time{}

	if task.Status == TaskStatusBroken || task.Status == TaskStatusCleaned {
		return nil
	}
	if !task.SessionExists || !task.AgentWindowExists {
		return nil
	}

	provider, ok := s.providers[task.Provider]
	if !ok || provider == nil {
		return nil
	}

	snapshotCtx, cancel := context.WithTimeout(ctx, runtimeSnapshotTimeout)
	defer cancel()

	snapshot, err := s.session.SnapshotTaskSession(snapshotCtx, task)
	if err != nil {
		// Runtime snapshots are best-effort enrichment; task listing should still work without them.
		return nil
	}

	task.RuntimeState = provider.DetectRuntimeState(snapshot)
	if !snapshot.ObservedAt.IsZero() {
		task.RuntimeStateUpdatedAt = snapshot.ObservedAt.UTC()
		return nil
	}

	task.RuntimeStateUpdatedAt = time.Now().UTC()
	return nil
}

func (s *Service) markBroken(ctx context.Context, task *Task, failure error) (*Task, error) {
	task.Status = TaskStatusBroken
	task.LastError = failure.Error()
	task.UpdatedAt = time.Now().UTC()

	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	return task, failure
}

func (s *Service) markCleanupBroken(ctx context.Context, task *Task, failure error) (*Task, error) {
	task.Status = TaskStatusBroken
	task.LastError = "cleanup failed: " + failure.Error()
	task.UpdatedAt = time.Now().UTC()

	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	return task, failure
}

func fallbackDisplayName(prompt string) string {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", ":", " ")
	normalized = replacer.Replace(normalized)

	words := strings.Fields(normalized)
	if len(words) == 0 {
		return "task"
	}

	leadingVerbs := []string{"add", "fix", "create", "implement", "build", "make", "update"}
	if len(words) > 1 && slices.Contains(leadingVerbs, words[0]) {
		words = words[1:]
	}

	return strings.Join(words, " ")
}

func prDisplayName(pr RepoPullRequest) string {
	title := strings.TrimSpace(pr.Title)
	if title == "" {
		return fmt.Sprintf("PR #%d", pr.Number)
	}

	return fmt.Sprintf("PR #%d %s", pr.Number, title)
}

func existingTaskForBranch(tasks []*Task, repoRoot string, branch string) *Task {
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.RepoRoot == repoRoot && task.BranchName == branch {
			return task
		}
	}

	return nil
}

func emitTaskProgress(progress func(TaskProgress), event TaskProgress) {
	if progress == nil {
		return
	}

	progress(event)
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}

	clone := *task
	return &clone
}

func isCleanupFailure(message string) bool {
	return strings.HasPrefix(message, "cleanup failed: ")
}

func writeSetupFiles(worktreePath string, files map[string][]byte) error {
	for relPath, content := range files {
		absPath := filepath.Join(worktreePath, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(absPath), err)
		}
		if err := os.WriteFile(absPath, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", relPath, err)
		}
	}
	return nil
}
