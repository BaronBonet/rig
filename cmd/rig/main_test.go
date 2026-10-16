package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
)

func TestExecuteSource_UsesCobraCommandRuntime(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.Contains(t, source, `cmd := newRootCommand(newProductionCommandRuntime(stdout, stderr))`)
	require.Contains(t, source, `os.Getenv(daemonModeEnvKey) == daemonModeEnvValue`)
	require.Contains(t, source, `os.Unsetenv(daemonModeEnvKey)`)
	require.Contains(t, source, `if err := adapter.EnsureRunning(ctx); err != nil`)
	require.Contains(t, source, `displayVersion := taskdaemon.FrontendBuildVersion()`)
	require.Contains(t, source, `program := tui.NewProgram(`)
	require.NotContains(t, source, `return run(ctx,`)
}

func TestDaemonHookRoutes_ExposeAllSupportedProviderHookRoutes(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.NotContains(t, source, "func daemonHookRoutes(")
	require.NotContains(t, source, `"/codex-hook"`)
	require.NotContains(t, source, `"/claude-hook"`)
	require.NotContains(t, source, `"/hook"`)
	require.NotContains(t, source, "CollectorURL:")
	require.Contains(t, source, "providerregistry.NewProviderClients(")
	require.Contains(t, source, "providerregistry.NewHookRoutes(")
}

func TestExecuteSource_ConstructsSingleTaskdaemonAdapterForClientPath(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.Contains(t, source, `return taskdaemon.New(taskdaemon.Config{`)
	require.Contains(t, source, `frontend := adapter.Frontend()`)
	require.NotContains(t, source, `deps :=`)
	require.NotContains(t, source, `func newRuntimeDependencies(`)
}

func TestRootCommandRunsTUIByDefault(t *testing.T) {
	t.Parallel()

	var calls []string
	cmd := newRootCommand(commandRuntime{
		version: "test-version",
		runTUI: func() error {
			calls = append(calls, "tui")
			return nil
		},
	})
	cmd.SetArgs([]string{})

	require.NoError(t, cmd.Execute())
	require.Equal(t, []string{"tui"}, calls)
}

func TestRootCommandPrintsBareVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := newRootCommand(commandRuntime{
		stdout:  &stdout,
		version: "test-version",
		runTUI: func() error {
			t.Fatal("root TUI should not run for --version")
			return nil
		},
	})
	cmd.SetArgs([]string{"--version"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, "test-version\n", stdout.String())
}

func TestRootCommandRoutesDoctor(t *testing.T) {
	t.Parallel()

	var calls []string
	cmd := newRootCommand(commandRuntime{
		version: "test-version",
		runDoctor: func() error {
			calls = append(calls, "doctor")
			return nil
		},
	})
	cmd.SetArgs([]string{"doctor"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, []string{"doctor"}, calls)
}

func TestRootCommandRoutesUpdate(t *testing.T) {
	t.Parallel()

	var calls []string
	cmd := newRootCommand(commandRuntime{
		version: "test-version",
		runUpdate: func() error {
			calls = append(calls, "update")
			return nil
		},
	})
	cmd.SetArgs([]string{"update"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, []string{"update"}, calls)
}

func TestRootCommandRoutesDaemonCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "start", args: []string{"daemon", "start"}, want: "daemon-start"},
		{name: "stop", args: []string{"daemon", "stop"}, want: "daemon-stop"},
		{name: "restart", args: []string{"daemon", "restart"}, want: "daemon-restart"},
		{name: "status", args: []string{"daemon", "status"}, want: "daemon-status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			cmd := newRootCommand(commandRuntime{
				version: "test-version",
				runDaemonStart: func() error {
					calls = append(calls, "daemon-start")
					return nil
				},
				runDaemonStop: func() error {
					calls = append(calls, "daemon-stop")
					return nil
				},
				runDaemonRestart: func() error {
					calls = append(calls, "daemon-restart")
					return nil
				},
				runDaemonStatus: func() error {
					calls = append(calls, "daemon-status")
					return nil
				},
			})
			cmd.SetArgs(tt.args)

			require.NoError(t, cmd.Execute())
			require.Equal(t, []string{tt.want}, calls)
		})
	}
}

func TestRootCommandRoutesTaskCreateToTheCLIHandler(t *testing.T) {
	t.Parallel()

	frontend := core.NewMockTaskFrontend(t)
	var input core.CreateTaskInput
	frontend.EXPECT().CreateTaskStream(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, in core.CreateTaskInput) (<-chan core.TaskCreateEvent, error) {
			input = in
			events := make(chan core.TaskCreateEvent, 1)
			events <- core.TaskCreateEvent{Task: &core.Task{ID: "task-1", DisplayName: "Call the new endpoint"}}
			close(events)
			return events, nil
		},
	)

	var stdout bytes.Buffer
	cmd := newRootCommand(commandRuntime{
		stdout:  &stdout,
		stderr:  &bytes.Buffer{},
		stdin:   strings.NewReader(""),
		version: "test-version",
		runTUI: func() error {
			t.Fatal("root TUI should not run for task create")
			return nil
		},
		taskFrontend: func(context.Context) (core.TaskFrontend, error) {
			return frontend, nil
		},
		getwd:           func() (string, error) { return "/work/backend", nil },
		stdinIsTerminal: func() bool { return true },
	})
	cmd.SetArgs([]string{"task", "create", "--submit", "call the new endpoint"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, core.CreateTaskInput{
		Cwd:               "/work/backend",
		Prompt:            "call the new endpoint",
		SubmitPrompt:      true,
		LinkToCallingTask: true,
	}, input)
	require.Contains(t, stdout.String(), "Call the new endpoint")
}

func TestRootCommandRoutesRepoListToTheCLIHandler(t *testing.T) {
	t.Parallel()

	frontend := core.NewMockTaskFrontend(t)
	frontend.EXPECT().ListRepos(mock.Anything).Return([]core.KnownRepo{
		{Name: "frontend", Root: "/work/frontend", TaskCount: 1},
	}, nil)

	var stdout bytes.Buffer
	cmd := newRootCommand(commandRuntime{
		stdout:  &stdout,
		stderr:  &bytes.Buffer{},
		version: "test-version",
		runTUI: func() error {
			t.Fatal("root TUI should not run for repo list")
			return nil
		},
		taskFrontend: func(context.Context) (core.TaskFrontend, error) {
			return frontend, nil
		},
	})
	cmd.SetArgs([]string{"repo", "list", "--json"})

	require.NoError(t, cmd.Execute())
	require.JSONEq(t, `[{"name":"frontend","root":"/work/frontend","task_count":1}]`, stdout.String())
}

// An agent reads a successful exit as success, so a mistyped subcommand of a
// command group must fail rather than print help.
func TestRootCommandRejectsUnknownSubcommandsOfCommandGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "task",
			args:    []string{"task", "crate", "call the new endpoint"},
			wantErr: `unknown command "crate" for "rig task"`,
		},
		{name: "repo", args: []string{"repo", "lsit"}, wantErr: `unknown command "lsit" for "rig repo"`},
		{name: "daemon", args: []string{"daemon", "stat"}, wantErr: `unknown command "stat" for "rig daemon"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newRootCommand(commandRuntime{
				stdout:  &bytes.Buffer{},
				stderr:  &bytes.Buffer{},
				version: "test-version",
				taskFrontend: func(context.Context) (core.TaskFrontend, error) {
					t.Fatal("no task frontend is needed for an unknown command")
					return nil, nil
				},
				runDaemonStatus: func() error {
					t.Fatal("daemon status should not run for an unknown command")
					return nil
				},
			})
			cmd.SetArgs(tt.args)

			require.EqualError(t, cmd.Execute(), tt.wantErr)
		})
	}
}

func TestRootCommandPrintsTheHelpOfACommandGroupWithoutASubcommand(t *testing.T) {
	t.Parallel()

	for _, group := range []string{"task", "repo", "daemon"} {
		t.Run(group, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			cmd := newRootCommand(commandRuntime{stdout: &stdout, stderr: &bytes.Buffer{}, version: "test-version"})
			cmd.SetArgs([]string{group})

			require.NoError(t, cmd.Execute())
			require.Contains(t, stdout.String(), "Available Commands:")
		})
	}
}

func TestCLITaskFrontendRefusesToRunInTheCodexSandbox(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "seatbelt")

	_, err := newCLITaskFrontend(t.Context())

	require.ErrorIs(t, err, errInCodexSandbox)
	require.EqualError(t, err, "rig must run outside the Codex sandbox: rerun the command with escalated permissions")
}

func TestExecuteWithArgs_VersionFlagPrintsConfiguredVersion(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	originalVersion := version
	version = "v9.9.9"
	t.Cleanup(func() {
		version = originalVersion
	})

	err := executeWithArgs([]string{"--version"}, stdout, stderr)
	require.NoError(t, err)
	require.Equal(t, "v9.9.9\n", stdout.String())
	require.Empty(t, stderr.String())
}

func TestExecuteWithArgsDoctorReportsHealthyEnvironment(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "git")
	writeExecutable(t, binDir, "tmux")
	writeExecutable(t, binDir, "codex")

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")
	writeUserProviderConfig(t, home)
	installRigCodexHooksFixture(t, home, "http://127.0.0.1:4124/codex-hook")

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.NoError(t, err)
	require.Contains(t, stdout.String(), "Rig doctor")
	require.Contains(t, stdout.String(), "OK   git")
	require.Contains(t, stdout.String(), "OK   tmux")
	require.Contains(t, stdout.String(), "OK   codex")
	require.Contains(t, stdout.String(), "OK   sqlite")
	require.Contains(t, stdout.String(), "OK   repo dirs")
	require.Contains(t, stdout.String(), "WARN gh")
	// rig setup was never run, so the Rig skill is missing, which only warns.
	require.Contains(t, stdout.String(), "WARN codex skill not installed at "+
		filepath.Join(home, ".agents", "skills", "rig", "SKILL.md"))
	require.Contains(t, stdout.String(), "Provider override (RIG_PROVIDER): codex")
}

// A repo directory that no longer exists hides its repositories from rig
// repo list, but does not stop rig from working.
func TestExecuteWithArgsDoctorWarnsAboutRepoDirectoriesThatDoNotExist(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "git")
	writeExecutable(t, binDir, "tmux")
	writeExecutable(t, binDir, "codex")

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")
	moved := filepath.Join(home, "moved")
	configDir := filepath.Join(home, ".config", "rig")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"codex",`+
			`"repo_dirs":["`+home+`","`+moved+`"]}`),
		0o600,
	))
	installRigCodexHooksFixture(t, home, "http://127.0.0.1:4124/codex-hook")

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.NoError(t, err)
	require.Contains(t, stdout.String(), "WARN repo dirs not found: "+moved+" (run rig setup to change them)")
}

func writeUserProviderConfig(t *testing.T, home string) {
	t.Helper()
	configDir := filepath.Join(home, ".config", "rig")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"codex"}`),
		0o600,
	))
}

func TestExecuteWithArgsDoctorFailsWhenRequiredCommandMissing(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "git")
	writeExecutable(t, binDir, "codex")

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, "tmux")
	require.Contains(t, stdout.String(), "FAIL tmux")
}

func TestExecuteWithArgsDoctorFailsWhenSQLiteIsUnhealthy(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "git")
	writeExecutable(t, binDir, "tmux")
	writeExecutable(t, binDir, "codex")

	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("not a sqlite database"), 0o644))

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")
	t.Setenv("RIG_SQLITE_PATH", dbPath)

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, "sqlite")
	require.Contains(t, stdout.String(), "FAIL sqlite")
}

func TestTaskDaemonBuildIdentityIncludesExecutableMetadataForDevBuilds(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rig")
	require.NoError(t, os.WriteFile(path, []byte("first"), 0o755))

	first, err := taskDaemonBuildIdentity(path, "dev")
	require.NoError(t, err)
	require.Contains(t, first, path)

	require.NoError(t, os.WriteFile(path, []byte("second-build"), 0o755))
	second, err := taskDaemonBuildIdentity(path, "dev")
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.Equal(t, "v9.9.9", mustTaskDaemonBuildIdentity(t, path, "v9.9.9"))
}

func mustTaskDaemonBuildIdentity(t *testing.T, path string, version string) string {
	t.Helper()

	identity, err := taskDaemonBuildIdentity(path, version)
	require.NoError(t, err)
	return identity
}

func writeExecutable(t *testing.T, dir string, name string) {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

func installRigCodexHooksFixture(t *testing.T, home string, collectorURL string) {
	t.Helper()

	codexHome := filepath.Join(home, ".codex")
	hooksDir := filepath.Join(codexHome, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))

	scriptPath := filepath.Join(hooksDir, "forward-to-rig.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\n# "+collectorURL+"\n"), 0o700))

	var hooks strings.Builder
	hooks.WriteString(`{"hooks":{`)
	events := []string{
		"SessionStart",
		"SessionEnd",
		"UserPromptSubmit",
		"Stop",
		"PreToolUse",
		"PermissionRequest",
		"PostToolUse",
		"SubagentStart",
	}
	for i, eventName := range events {
		if i > 0 {
			hooks.WriteString(",")
		}
		hooks.WriteString(`"` + eventName + `":[{"hooks":[{"type":"command","command":"`)
		hooks.WriteString(scriptPath + " " + eventName)
		hooks.WriteString(`"}]}]`)
	}
	hooks.WriteString("}}\n")

	require.NoError(t, os.WriteFile(filepath.Join(codexHome, "hooks.json"), []byte(hooks.String()), 0o644))
}

// The Rig skill teaches agents to run rig commands, so every command it runs
// must exist as built, with every flag it passes: renaming one must fail here
// rather than in an agent's session. A flag the skill names in a rig command
// must be one of that command's; a flag it names on its own, such as
// `--no-parent`, cannot be told apart from its command reliably, so it only
// has to be a flag of some rig command.
func TestRigSkillUsesOnlyCommandsAndFlagsThatExist(t *testing.T) {
	t.Parallel()

	root := newRootCommand(commandRuntime{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, version: "test-version"})
	invocations, looseFlags := rigSkillCode(string(providerkit.RigSkill()))

	var commandPaths []string
	for _, invocation := range invocations {
		cmd, rest := findSubcommand(root, invocation)
		require.NotSame(t, root, cmd, "rig %s names no rig command", strings.Join(invocation, " "))
		require.False(t, cmd.HasSubCommands(),
			"rig %s names the command group %q, not one of its commands", strings.Join(invocation, " "), cmd.Name())
		for _, word := range rest {
			if flag, ok := flagName(word); ok {
				require.True(t, hasFlag(cmd, flag), "%q has no flag %s", cmd.CommandPath(), word)
			}
		}
		commandPaths = append(commandPaths, cmd.CommandPath())
	}
	for _, word := range looseFlags {
		flag, ok := flagName(word)
		require.True(t, ok)
		require.True(t, anyCommandHasFlag(root, flag), "no rig command has the flag %s", word)
	}

	// The skill's commands were found at all.
	require.Contains(t, commandPaths, "rig task create")
	require.Contains(t, commandPaths, "rig repo list")
	require.Contains(t, looseFlags, "--no-parent")
}

var inlineCode = regexp.MustCompile("`([^`]+)`")

// rigSkillCode returns the rig commands the skill's code runs, each split
// into words after rig, and the flags its code names on their own.
func rigSkillCode(skill string) ([][]string, []string) {
	var snippets []string
	var prose strings.Builder
	inFence := false
	for line := range strings.Lines(skill) {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "```"):
			inFence = !inFence
		case inFence:
			snippets = append(snippets, line)
		default:
			prose.WriteString(line)
		}
	}
	for _, match := range inlineCode.FindAllStringSubmatch(prose.String(), -1) {
		snippets = append(snippets, match[1])
	}

	var invocations [][]string
	var looseFlags []string
	for _, snippet := range snippets {
		// A heredoc is the command's input, not its arguments.
		snippet, _, _ = strings.Cut(snippet, "<<")
		words := strings.Fields(snippet)
		switch {
		case len(words) > 1 && words[0] == "rig":
			invocations = append(invocations, words[1:])
		case len(words) > 0 && strings.HasPrefix(words[0], "--"):
			looseFlags = append(looseFlags, words[0])
		}
	}
	return invocations, looseFlags
}

// findSubcommand returns the deepest command that leading words of args name
// below cmd, and the words after them.
func findSubcommand(cmd *cobra.Command, args []string) (*cobra.Command, []string) {
	for len(args) > 0 {
		var next *cobra.Command
		for _, sub := range cmd.Commands() {
			if sub.Name() == args[0] || sub.HasAlias(args[0]) {
				next = sub
			}
		}
		if next == nil {
			break
		}
		cmd, args = next, args[1:]
	}
	return cmd, args
}

// flagName returns the name of the long flag a word passes, such as repo for
// --repo or --repo=x. A lone - is an argument, which reads stdin.
func flagName(word string) (string, bool) {
	name, ok := strings.CutPrefix(word, "--")
	if !ok || name == "" {
		return "", false
	}
	name, _, _ = strings.Cut(name, "=")
	return name, true
}

func hasFlag(cmd *cobra.Command, name string) bool {
	return cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil
}

func anyCommandHasFlag(cmd *cobra.Command, name string) bool {
	if hasFlag(cmd, name) {
		return true
	}
	for _, sub := range cmd.Commands() {
		if anyCommandHasFlag(sub, name) {
			return true
		}
	}
	return false
}
