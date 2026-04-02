package core

import (
	"fmt"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"agent/internal/pkg/timeutil"
	"agent/internal/pkg/slug"
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
		ID:            fmt.Sprintf("%d", now.UnixNano()),
		Prompt:        input.Prompt,
		DisplayName:   displayName,
		Slug:          taskSlug,
		RepoRoot:      repoCtx.Root,
		BaseBranch:    repoCtx.BaseBranch,
		BranchName:    "feat/" + taskSlug,
		WorktreePath:  filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug),
		TmuxSession:   repoCtx.Name + ":" + taskSlug,
		Provider:      "codex",
		Status:        TaskStatusCreating,
		CreatedAt:     now,
		UpdatedAt:     now,
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
