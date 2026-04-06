package core

import (
	"agent/internal/pkg/slug"
	"agent/internal/pkg/timeutil"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type DoctorResult struct {
	Notes    []string
	Failures []string
}

type NewTaskInput struct {
	Cwd                  string
	Prompt               string
	ConfirmedDisplayName string
	Provider             string
}

type Service struct {
	tasks      TaskRepository
	repo       RepoClient
	session    SessionClient
	providers  map[string]ProviderClient
	repoConfig RepoConfigRepository
	workspace  WorkspaceSeeder
	clock      timeutil.Clock
	cfg        Config
}

func (s *Service) SuggestTaskName(ctx context.Context, prompt string, provider string) (string, error) {
	repo := s.resolveProvider(provider)
	if repo == nil {
		return fallbackDisplayName(prompt), nil
	}
	name, err := repo.SuggestTaskName(ctx, prompt)
	if err == nil && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name), nil
	}

	return fallbackDisplayName(prompt), nil
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

func (s *Service) NewTask(ctx context.Context, input NewTaskInput) (*Task, error) {
	return s.createTask(ctx, input, CreateTaskOptions{}, nil)
}

func (s *Service) CreateTaskWithProgress(
	ctx context.Context,
	input NewTaskInput,
	options CreateTaskOptions,
	progress func(TaskProgress),
) (*Task, error) {
	return s.createTask(ctx, input, options, progress)
}

func (s *Service) createTask(
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

	displayName := strings.TrimSpace(input.ConfirmedDisplayName)
	if displayName == "" {
		emitTaskProgress(progress, TaskProgress{
			Step:    TaskProgressNaming,
			Message: "Naming task...",
		})
		displayName, err = s.SuggestTaskName(ctx, input.Prompt, input.Provider)
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

	now := s.clock.Now().UTC()
	taskSlug := slug.EnsureUnique(slug.FromDisplayName(displayName), existingSlugs)
	task := &Task{
		ID:          fmt.Sprintf("%d", now.UnixNano()),
		Prompt:      input.Prompt,
		DisplayName: displayName,
		Slug:        taskSlug,
		RepoRoot:    repoCtx.Root,
		RepoName:    repoCtx.Name,
		BaseBranch:  repoCtx.BaseBranch,
		// TODO: don't assume its a feat, the llm should figure that out
		BranchName:       "feat/" + taskSlug,
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
	_ = s.tasks.AppendEvent(ctx, task.ID, "task_created", task.DisplayName)

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
	task.UpdatedAt = s.clock.Now().UTC()
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
	task.UpdatedAt = s.clock.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	_ = s.tasks.AppendEvent(ctx, task.ID, "agent_launch_requested", strings.Join(launch.Command, " "))

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

	if task.Status != TaskStatusRunning && task.Status != TaskStatusDegraded {
		return ErrBrokenTask
	}

	if !task.SessionExists || !task.AgentWindowExists {
		return ErrBrokenTask
	}

	return s.session.OpenTaskSession(ctx, task)
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

	_ = s.tasks.AppendEvent(ctx, task.ID, "cleanup_requested", string(task.Status))

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
		task.UpdatedAt = s.clock.Now().UTC()
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
		task.UpdatedAt = s.clock.Now().UTC()
		if err := s.tasks.UpdateTask(ctx, task); err != nil {
			return task, err
		}
	}

	task.Status = TaskStatusCleaned
	task.LastError = ""
	task.UpdatedAt = s.clock.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	_ = s.tasks.AppendEvent(ctx, task.ID, "cleanup_completed", string(task.Status))

	return task, nil
}

func NewService(
	tasks TaskRepository,
	repo RepoClient,
	session SessionClient,
	providers map[string]ProviderClient,
	repoConfig RepoConfigRepository,
	workspace WorkspaceSeeder,
	clock timeutil.Clock,
	cfg Config,
) *Service {
	return &Service{
		tasks:      tasks,
		repo:       repo,
		session:    session,
		providers:  providers,
		repoConfig: repoConfig,
		workspace:  workspace,
		clock:      clock,
		cfg:        cfg,
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

	if strings.TrimSpace(cwd) != "" {
		repoCtx, err := s.repo.DetectRepo(ctx, cwd)
		if err != nil {
			result.Failures = append(result.Failures, "repo: "+err.Error())
		} else {
			repoConfig, err := s.repoConfig.LoadRepoConfig(ctx, repoCtx.Root)
			if err != nil {
				result.Failures = append(result.Failures, "config: "+err.Error())
			} else if !repoConfig.Exists {
				result.Notes = append(result.Notes, "config: agent.yaml not found")
			} else {
				result.Notes = append(result.Notes, "config: loaded agent.yaml")
				if err := s.workspace.ValidateSeedPaths(ctx, repoCtx.Root, repoConfig.Seed.Copy); err != nil {
					result.Failures = append(result.Failures, "config: "+err.Error())
				} else {
					for _, path := range repoConfig.Seed.Copy {
						result.Notes = append(result.Notes, "config: seed path ok: "+path)
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

	sessionResources, err := s.session.InspectTaskSession(ctx, &reconciled)
	if err != nil {
		return nil, err
	}
	reconciled.SessionExists = sessionResources.SessionExists
	reconciled.AgentWindowExists = sessionResources.AgentWindowExists
	reconciled.EditorWindowExists = sessionResources.EditorWindowExists
	reconciled.LastReconciledAt = s.clock.Now().UTC()
	reconciled.UpdatedAt = s.clock.Now().UTC()
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

	snapshot, err := s.session.SnapshotTaskSession(ctx, task)
	if err != nil {
		return nil
	}

	task.RuntimeState = provider.DetectRuntimeState(snapshot)
	if !snapshot.ObservedAt.IsZero() {
		task.RuntimeStateUpdatedAt = snapshot.ObservedAt.UTC()
		return nil
	}

	task.RuntimeStateUpdatedAt = s.clock.Now().UTC()
	return nil
}

func (s *Service) markBroken(ctx context.Context, task *Task, failure error) (*Task, error) {
	task.Status = TaskStatusBroken
	task.LastError = failure.Error()
	task.UpdatedAt = s.clock.Now().UTC()

	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	_ = s.tasks.AppendEvent(ctx, task.ID, "error_recorded", task.LastError)

	return task, failure
}

func (s *Service) markCleanupBroken(ctx context.Context, task *Task, failure error) (*Task, error) {
	task.Status = TaskStatusBroken
	task.LastError = "cleanup failed: " + failure.Error()
	task.UpdatedAt = s.clock.Now().UTC()

	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	_ = s.tasks.AppendEvent(ctx, task.ID, "cleanup_failed", task.LastError)

	return task, failure
}

func windowOrDefault(window, fallback string) string {
	if strings.TrimSpace(window) == "" {
		return fallback
	}

	return window
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
