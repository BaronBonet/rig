package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"agent/internal/pkg/slug"
	"agent/internal/pkg/timeutil"
)

type DoctorResult struct {
	Notes    []string
	Failures []string
}

type NewTaskInput struct {
	Cwd                  string
	Prompt               string
	ConfirmedDisplayName string
}

type Service struct {
	tasks      TaskRepository
	git        GitRepository
	tmux       TmuxRepository
	codex      CodexRepository
	repoConfig RepoConfigRepository
	workspace  WorkspaceSeeder
	clock      timeutil.Clock
	cfg        Config
}

func (s *Service) SuggestTaskName(ctx context.Context, prompt string) (string, error) {
	name, err := s.codex.ProposeTaskName(ctx, prompt)
	if err == nil && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name), nil
	}

	return fallbackDisplayName(prompt), nil
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
	repoCtx, err := s.git.DetectRepo(ctx, input.Cwd)
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
		displayName, err = s.SuggestTaskName(ctx, input.Prompt)
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

	now := s.clock.Now().UTC()
	taskSlug := slug.EnsureUnique(slug.FromDisplayName(displayName), existingSlugs)
	task := &Task{
		ID:               fmt.Sprintf("%d", now.UnixNano()),
		Prompt:           input.Prompt,
		DisplayName:      displayName,
		Slug:             taskSlug,
		RepoRoot:         repoCtx.Root,
		RepoName:         repoCtx.Name,
		BaseBranch:       repoCtx.BaseBranch,
		BranchName:       "feat/" + taskSlug,
		WorktreePath:     filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug),
		TmuxSession:      repoCtx.Name + "-" + taskSlug,
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
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
	if err := s.git.CreateWorktree(ctx, CreateWorktreeInput{
		RepoRoot:     task.RepoRoot,
		BaseBranch:   task.BaseBranch,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
	}); err != nil {
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
	if err := s.tmux.CreateSession(ctx, CreateSessionInput{
		SessionName:      task.TmuxSession,
		WorkingDir:       task.WorktreePath,
		AgentWindowName:  task.AgentWindowName,
		EditorWindowName: task.EditorWindowName,
	}); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("start tmux session: %w", err))
	}

	task.SessionExists = true
	task.AgentWindowExists = true
	task.EditorWindowExists = true
	task.UpdatedAt = s.clock.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	command, err := s.codex.BuildLaunchCommand(task)
	if err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("build codex launch command: %w", err))
	}

	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressCodexLaunching,
		Message: "Launching Codex...",
		Task:    cloneTask(task),
	})
	if err := s.tmux.SendKeysToWindow(ctx, task.TmuxSession, task.AgentWindowName, command); err != nil {
		return s.markBroken(ctx, task, fmt.Errorf("launch codex: %w", err))
	}

	task.Status = TaskStatusRunning
	task.UpdatedAt = s.clock.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	_ = s.tasks.AppendEvent(ctx, task.ID, "codex_launch_requested", strings.Join(command, " "))

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
		if err := s.tmux.AttachOrSwitch(ctx, task.TmuxSession); err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("open tmux session: %w", err))
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

		reconciled = append(reconciled, nextTask)
	}

	return reconciled, nil
}

func (s *Service) GetTask(ctx context.Context, idOrSlug string) (*Task, error) {
	task, err := s.tasks.GetTask(ctx, idOrSlug)
	if err != nil {
		return nil, err
	}

	return s.reconcileTask(ctx, task)
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

	return s.tmux.AttachOrSwitch(ctx, task.TmuxSession)
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
		if err := s.tmux.KillSession(ctx, task.TmuxSession); err != nil {
			sessionExists, checkErr := s.tmux.SessionExists(ctx, task.TmuxSession)
			if checkErr != nil || sessionExists {
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
		if err := s.git.RemoveWorktree(ctx, task.RepoRoot, task.WorktreePath); err != nil {
			worktreeExists, checkErr := worktreePresence(task.WorktreePath)
			if checkErr != nil || worktreeExists {
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
	git GitRepository,
	tmux TmuxRepository,
	codex CodexRepository,
	repoConfig RepoConfigRepository,
	workspace WorkspaceSeeder,
	clock timeutil.Clock,
	cfg Config,
) *Service {
	return &Service{
		tasks:      tasks,
		git:        git,
		tmux:       tmux,
		codex:      codex,
		repoConfig: repoConfig,
		workspace:  workspace,
		clock:      clock,
		cfg:        cfg,
	}
}

func (s *Service) Doctor(ctx context.Context, cwd string) (DoctorResult, error) {
	result := DoctorResult{}

	if err := s.git.IsAvailable(ctx); err != nil {
		result.Failures = append(result.Failures, "git: "+err.Error())
	}

	if err := s.tmux.IsAvailable(ctx); err != nil {
		result.Failures = append(result.Failures, "tmux: "+err.Error())
	}

	if err := s.codex.IsAvailable(ctx); err != nil {
		result.Failures = append(result.Failures, "codex: "+err.Error())
	}

	if err := ensureDatabasePath(s.cfg.DatabasePath); err != nil {
		result.Failures = append(result.Failures, "database: "+err.Error())
	}

	if strings.TrimSpace(cwd) != "" {
		repoCtx, err := s.git.DetectRepo(ctx, cwd)
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

func ensureDatabasePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func (s *Service) reconcileTask(ctx context.Context, task *Task) (*Task, error) {
	reconciled := *task
	worktreeExists, worktreeErr := worktreePresence(reconciled.WorktreePath)
	reconciled.WorktreeExists = worktreeExists

	branchExists, err := s.git.BranchExists(ctx, reconciled.RepoRoot, reconciled.BranchName)
	if err != nil {
		return nil, err
	}
	reconciled.BranchExists = branchExists

	sessionExists, err := s.tmux.SessionExists(ctx, reconciled.TmuxSession)
	if err != nil {
		return nil, err
	}
	reconciled.SessionExists = sessionExists
	if reconciled.SessionExists {
		agentWindowExists, err := s.tmux.WindowExists(ctx, reconciled.TmuxSession, windowOrDefault(reconciled.AgentWindowName, "agent"))
		if err != nil {
			return nil, err
		}
		reconciled.AgentWindowExists = agentWindowExists
		editorWindowExists, err := s.tmux.WindowExists(ctx, reconciled.TmuxSession, windowOrDefault(reconciled.EditorWindowName, "editor"))
		if err != nil {
			return nil, err
		}
		reconciled.EditorWindowExists = editorWindowExists
	} else {
		reconciled.AgentWindowExists = false
		reconciled.EditorWindowExists = false
	}
	reconciled.LastReconciledAt = s.clock.Now().UTC()
	reconciled.UpdatedAt = s.clock.Now().UTC()
	problems := make([]string, 0, 1)
	if worktreeErr != nil {
		reconciled.WorktreeExists = true
		problems = append(problems, "worktree check failed")
	}

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

func worktreeExists(path string) bool {
	exists, _ := worktreePresence(path)
	return exists
}

func worktreePresence(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, err
	}

	return info.IsDir(), nil
}

func isCleanupFailure(message string) bool {
	return strings.HasPrefix(message, "cleanup failed: ")
}
