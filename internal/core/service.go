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
	Failures []string
}

type NewTaskInput struct {
	Cwd                  string
	Prompt               string
	ConfirmedDisplayName string
}

type Service struct {
	tasks TaskRepository
	git   GitRepository
	tmux  TmuxRepository
	codex CodexRepository
	clock timeutil.Clock
	cfg   Config
}

func (s *Service) SuggestTaskName(ctx context.Context, prompt string) (string, error) {
	name, err := s.codex.ProposeTaskName(ctx, prompt)
	if err == nil && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name), nil
	}

	return fallbackDisplayName(prompt), nil
}

func (s *Service) NewTask(ctx context.Context, input NewTaskInput) (*Task, error) {
	repoCtx, err := s.git.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	displayName := strings.TrimSpace(input.ConfirmedDisplayName)
	if displayName == "" {
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
		ID:           fmt.Sprintf("%d", now.UnixNano()),
		Prompt:       input.Prompt,
		DisplayName:  displayName,
		Slug:         taskSlug,
		RepoRoot:     repoCtx.Root,
		BaseBranch:   repoCtx.BaseBranch,
		BranchName:   "feat/" + taskSlug,
		WorktreePath: filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug),
		TmuxSession:  repoCtx.Name + "-" + taskSlug,
		Provider:     "codex",
		Status:       TaskStatusCreating,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	_ = s.tasks.AppendEvent(ctx, task.ID, "task_created", task.DisplayName)

	if err := s.git.CreateWorktree(ctx, CreateWorktreeInput{
		RepoRoot:     task.RepoRoot,
		BaseBranch:   task.BaseBranch,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
	}); err != nil {
		return s.markBroken(ctx, task, err)
	}

	task.WorktreeExists = true
	task.BranchExists = true
	task.UpdatedAt = s.clock.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	if err := s.tmux.CreateSession(ctx, CreateSessionInput{
		SessionName: task.TmuxSession,
		WorkingDir:  task.WorktreePath,
	}); err != nil {
		return s.markBroken(ctx, task, err)
	}

	task.SessionExists = true
	task.UpdatedAt = s.clock.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	command, err := s.codex.BuildLaunchCommand(task)
	if err != nil {
		return s.markBroken(ctx, task, err)
	}

	if err := s.tmux.SendKeys(ctx, task.TmuxSession, command); err != nil {
		return s.markBroken(ctx, task, err)
	}

	task.Status = TaskStatusRunning
	task.UpdatedAt = s.clock.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return task, err
	}

	_ = s.tasks.AppendEvent(ctx, task.ID, "codex_launch_requested", strings.Join(command, " "))

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

	if !task.SessionExists {
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
	clock timeutil.Clock,
	cfg Config,
) *Service {
	return &Service{
		tasks: tasks,
		git:   git,
		tmux:  tmux,
		codex: codex,
		clock: clock,
		cfg:   cfg,
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
		if _, err := s.git.DetectRepo(ctx, cwd); err != nil {
			result.Failures = append(result.Failures, "repo: "+err.Error())
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

		if len(missing) > 0 {
			reconciled.Status = TaskStatusBroken
			if task.Status == TaskStatusBroken && isCleanupFailure(task.LastError) {
				reconciled.LastError = task.LastError
			} else {
				reconciled.LastError = strings.Join(missing, ", ")
			}
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
