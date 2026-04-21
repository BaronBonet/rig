package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"rig/internal/adapters/taskdaemon"
	"rig/internal/core"
	"rig/internal/infrastructure"

	"github.com/stretchr/testify/require"
)

func TestRun_EnsuresTaskDaemonBeforeLaunchingTUI(t *testing.T) {
	t.Parallel()

	frontend := &stubTaskFrontend{}
	calls := make([]string, 0, 2)

	err := run(context.Background(), dependencies{
		Daemon: stubTaskDaemonRuntime{
			ensureRunning: func(context.Context) error {
				calls = append(calls, "ensure")
				return nil
			},
			frontend: frontend,
		},
		RunUI: func(got core.TaskFrontend) error {
			calls = append(calls, "ui")
			require.Same(t, frontend, got)
			require.Equal(t, []string{"ensure", "ui"}, calls)
			return nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"ensure", "ui"}, calls)
}

func TestDaemonHookRoutes_ExposeCodexHooksOnly(t *testing.T) {
	t.Parallel()

	routes := daemonHookRoutes(nil)
	require.Len(t, routes, 2)
	require.Equal(t, []string{"/hook", "/codex-hook"}, []string{
		routes[0].Path,
		routes[1].Path,
	})
}

func TestNewRuntimeDependencies_UsesUnifiedTaskdaemonAdapter(t *testing.T) {
	t.Parallel()

	cfg := &infrastructure.ApplicationConfig{
		TaskDaemon: taskdaemon.Config{
			SocketPath: filepath.Join(t.TempDir(), "taskdaemon.sock"),
			ExecPath:   "/tmp/rig",
			Env:        []string{"RIG_MODE=task-daemon"},
		},
	}

	deps, err := newRuntimeDependencies(cfg, "/tmp/rig")
	require.NoError(t, err)
	require.NotNil(t, deps.Daemon)
	require.IsType(t, &taskdaemon.Adapter{}, deps.Daemon)
	require.NotNil(t, deps.Daemon.Frontend())
}

func TestDependenciesShape_UsesUnifiedDaemonObjectAndRunUIOnly(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(dependencies{})
	require.Equal(t, 2, typ.NumField())
	require.Equal(t, "Daemon", typ.Field(0).Name)
	require.Equal(t, "RunUI", typ.Field(1).Name)
}

type stubTaskFrontend struct{}

type stubTaskDaemonRuntime struct {
	ensureRunning func(context.Context) error
	frontend      core.TaskFrontend
}

func (s stubTaskDaemonRuntime) EnsureRunning(ctx context.Context) error {
	if s.ensureRunning != nil {
		return s.ensureRunning(ctx)
	}

	return nil
}

func (s stubTaskDaemonRuntime) Frontend() core.TaskFrontend {
	return s.frontend
}

func (*stubTaskFrontend) CreateTask(context.Context, core.CreateTaskInput) (*core.Task, error) {
	return nil, nil
}

func (*stubTaskFrontend) ListTasks(context.Context) ([]*core.Task, error) {
	return nil, nil
}

func (*stubTaskFrontend) LatestTaskStatus(context.Context, string) (*core.TaskStatusUpdate, error) {
	return nil, nil
}

func (*stubTaskFrontend) SubscribeTaskStatus(context.Context, string) (<-chan core.TaskStatusUpdate, error) {
	return nil, nil
}
