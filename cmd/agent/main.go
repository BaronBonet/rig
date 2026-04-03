package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"agent/internal/adapters/handler/cli"
	codexrepo "agent/internal/adapters/repository/codex"
	gitrepo "agent/internal/adapters/repository/git"
	sqliterepo "agent/internal/adapters/repository/sqlite"
	"agent/internal/core"
	"agent/internal/pkg/execx"
	"agent/internal/pkg/timeutil"
)

var _ core.TmuxRepository = (*runtimeTmuxRepository)(nil)

func main() {
	deps, err := buildDependencies()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := cli.NewRootCommand(deps).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildDependencies() (cli.Dependencies, error) {
	cfg := core.DefaultConfig()
	service := &runtimeService{
		cfg:    cfg,
		runner: execx.ExecRunner{},
	}

	return cli.Dependencies{
		Service: service,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}, nil
}

type runtimeService struct {
	cfg    core.Config
	runner execx.ExecRunner
}

func (r *runtimeService) Doctor(ctx context.Context, cwd string) (core.DoctorResult, error) {
	service, err := r.newService(false)
	if err != nil {
		return core.DoctorResult{}, err
	}

	return service.Doctor(ctx, cwd)
}

func (r *runtimeService) SuggestTaskName(ctx context.Context, prompt string) (string, error) {
	service, err := r.newService(false)
	if err != nil {
		return "", err
	}

	return service.SuggestTaskName(ctx, prompt)
}

func (r *runtimeService) NewTask(ctx context.Context, input core.NewTaskInput) (*core.Task, error) {
	service, err := r.newService(true)
	if err != nil {
		return nil, err
	}

	return service.NewTask(ctx, input)
}

func (r *runtimeService) ListTasks(ctx context.Context) ([]*core.Task, error) {
	service, err := r.newService(true)
	if err != nil {
		return nil, err
	}

	return service.ListTasks(ctx)
}

func (r *runtimeService) GetTask(ctx context.Context, idOrSlug string) (*core.Task, error) {
	service, err := r.newService(true)
	if err != nil {
		return nil, err
	}

	return service.GetTask(ctx, idOrSlug)
}

func (r *runtimeService) OpenTask(ctx context.Context, idOrSlug string) error {
	service, err := r.newService(true)
	if err != nil {
		return err
	}

	return service.OpenTask(ctx, idOrSlug)
}

func (r *runtimeService) newService(withSQLite bool) (*core.Service, error) {
	var taskRepo core.TaskRepository = noopTaskRepository{}
	if withSQLite {
		sqliteRepo, err := sqliterepo.NewRepository(r.cfg.DatabasePath)
		if err != nil {
			return nil, err
		}

		taskRepo = sqliteRepo
	}

	return core.NewService(
		taskRepo,
		gitrepo.NewRepository(r.runner),
		&runtimeTmuxRepository{runner: r.runner, paneIDs: map[string]string{}},
		codexrepo.NewRepository(r.runner, r.cfg.CodexBinary),
		timeutil.RealClock{},
		r.cfg,
	), nil
}

type runtimeTmuxRepository struct {
	runner  execx.Runner
	paneIDs map[string]string
}

func (r *runtimeTmuxRepository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "tmux", "-V")
	return err
}

func (r *runtimeTmuxRepository) SessionExists(ctx context.Context, session string) (bool, error) {
	_, err := r.runner.Run(ctx, "", "tmux", "has-session", "-t", tmuxSessionName(session))
	if err != nil {
		return false, nil
	}

	return true, nil
}

func (r *runtimeTmuxRepository) CreateSession(ctx context.Context, in core.CreateSessionInput) error {
	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-session",
		"-d",
		"-P",
		"-F",
		"#{pane_id}",
		"-s",
		tmuxSessionName(in.SessionName),
		"-c",
		in.WorkingDir,
	)
	if err != nil {
		return err
	}

	r.paneIDs[in.SessionName] = strings.TrimSpace(result.Stdout)
	return nil
}

func (r *runtimeTmuxRepository) KillSession(ctx context.Context, session string) error {
	_, err := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", tmuxSessionName(session))
	return err
}

func (r *runtimeTmuxRepository) AttachOrSwitch(ctx context.Context, session string) error {
	_, err := r.runner.Run(ctx, "", "tmux", "switch-client", "-t", tmuxSessionName(session))
	return err
}

func (r *runtimeTmuxRepository) SendKeys(ctx context.Context, session string, command []string) error {
	target := r.paneIDs[session]
	if target == "" {
		target = tmuxSessionName(session)
	}

	quoted := make([]string, 0, len(command))
	for _, part := range command {
		if strings.ContainsRune(part, ' ') {
			quoted = append(quoted, "'"+strings.ReplaceAll(part, "'", "'\\''")+"'")
			continue
		}

		quoted = append(quoted, part)
	}

	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"send-keys",
		"-t",
		target,
		strings.Join(quoted, " "),
		"C-m",
	)
	return err
}

func tmuxSessionName(session string) string {
	return strings.ReplaceAll(session, ":", "-")
}

type noopTaskRepository struct{}

func (noopTaskRepository) CreateTask(context.Context, *core.Task) error {
	return fmt.Errorf("task repository unavailable")
}
func (noopTaskRepository) UpdateTask(context.Context, *core.Task) error {
	return fmt.Errorf("task repository unavailable")
}
func (noopTaskRepository) GetTask(context.Context, string) (*core.Task, error) {
	return nil, fmt.Errorf("task repository unavailable")
}
func (noopTaskRepository) ListTasks(context.Context) ([]*core.Task, error) {
	return nil, fmt.Errorf("task repository unavailable")
}
func (noopTaskRepository) AppendEvent(context.Context, string, string, string) error {
	return fmt.Errorf("task repository unavailable")
}
