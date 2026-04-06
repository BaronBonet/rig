package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"agent/internal/adapters/handler/cli"
	agentconfigrepo "agent/internal/adapters/repository/agentconfig"
	clauderepo "agent/internal/adapters/repository/claude"
	codexrepo "agent/internal/adapters/repository/codex"
	gitrepo "agent/internal/adapters/repository/git"
	sqliterepo "agent/internal/adapters/repository/sqlite"
	tmuxrepo "agent/internal/adapters/repository/tmux"
	workspacerepo "agent/internal/adapters/repository/workspace"
	"agent/internal/core"
	"agent/internal/infrastructure"
	"agent/internal/pkg/execx"
	"agent/internal/pkg/timeutil"
)

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
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return cli.Dependencies{}, err
	}

	runtimeMonitor := tmuxrepo.NewRuntimeMonitor()
	service := &runtimeService{
		cfg:            *cfg,
		runner:         execx.ExecRunner{},
		runtimeMonitor: runtimeMonitor,
		runtimeDetectors: map[string]core.RuntimeStateDetector{
			"codex":  codexrepo.NewRuntimeDetector(2 * time.Second),
			"claude": clauderepo.NewRuntimeDetector(2 * time.Second),
		},
	}

	return cli.Dependencies{
		Service: service,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}, nil
}

type runtimeService struct {
	runner           execx.ExecRunner
	cfg              infrastructure.Config
	runtimeMonitor   core.RuntimeMonitor
	runtimeDetectors map[string]core.RuntimeStateDetector
}

func (r *runtimeService) Doctor(ctx context.Context, cwd string) (core.DoctorResult, error) {
	service, err := r.newService(true)
	if err != nil {
		return core.DoctorResult{}, err
	}

	return service.Doctor(ctx, cwd)
}

func (r *runtimeService) SuggestTaskName(ctx context.Context, prompt string, provider string) (string, error) {
	service, err := r.newService(false)
	if err != nil {
		return "", err
	}

	return service.SuggestTaskName(ctx, prompt, provider)
}

func (r *runtimeService) NewTask(ctx context.Context, input core.NewTaskInput) (*core.Task, error) {
	service, err := r.newService(true)
	if err != nil {
		return nil, err
	}

	return service.NewTask(ctx, input)
}

func (r *runtimeService) CreateTaskWithProgress(
	ctx context.Context,
	input core.NewTaskInput,
	options core.CreateTaskOptions,
	progress func(core.TaskProgress),
) (*core.Task, error) {
	service, err := r.newService(true)
	if err != nil {
		return nil, err
	}

	return service.CreateTaskWithProgress(ctx, input, options, progress)
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

func (r *runtimeService) DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error) {
	service, err := r.newService(true)
	if err != nil {
		return nil, err
	}

	return service.DeleteTaskResources(ctx, idOrSlug)
}

func (r *runtimeService) newService(withSQLite bool) (*core.Service, error) {
	var taskRepo core.TaskRepository = noopTaskRepository{}
	if withSQLite {
		sqliteRepo, err := sqliterepo.NewRepository(r.cfg.SQLite)
		if err != nil {
			return nil, err
		}

		taskRepo = sqliteRepo
	}

	providers := map[string]core.ProviderRepository{
		"codex":  codexrepo.NewRepository(r.runner, r.cfg.Codex),
		"claude": clauderepo.NewRepository(r.runner, r.cfg.Claude),
	}

	return core.NewService(
		taskRepo,
		gitrepo.NewRepository(r.runner),
		tmuxrepo.NewRepository(r.runner),
		providers,
		r.runtimeMonitor,
		r.runtimeDetectors,
		agentconfigrepo.NewRepository(),
		workspacerepo.NewRepository(),
		timeutil.RealClock{},
		r.cfg.Service,
	), nil
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
