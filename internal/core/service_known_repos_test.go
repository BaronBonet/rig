package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceListRepos_ListsTheRepositoriesOfTasks(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{
		backendTask(),
		{ID: "task-frontend", RepoRoot: "/work/frontend", RepoName: "frontend"},
		{ID: "task-backend-2", RepoRoot: "/work/backend/", RepoName: "backend"},
	}

	repos, err := svc.service.ListRepos(t.Context())

	require.NoError(t, err)
	require.Equal(t, []KnownRepo{
		{Name: "backend", Root: "/work/backend", TaskCount: 2},
		{Name: "frontend", Root: "/work/frontend", TaskCount: 1},
	}, repos)
}

func TestTaskServiceListRepos_ListsTheCheckoutsInRepoDirectories(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.repoDirs = []string{"/work", "/code"}
	svc.repoClient.checkouts = map[string][]string{
		"/work": {"/work/backend", "/work/Notes"},
		"/code": {"/code/api", "/code/backend"},
	}

	repos, err := svc.service.ListRepos(t.Context())

	require.NoError(t, err)
	require.Equal(t, []KnownRepo{
		{Name: "api", Root: "/code/api"},
		{Name: "backend", Root: "/code/backend"},
		{Name: "backend", Root: "/work/backend"},
		{Name: "Notes", Root: "/work/Notes"},
	}, repos)
}

func TestTaskServiceListRepos_ListsARepositoryWithTasksInARepoDirectoryOnce(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{
		backendTask(),
		{ID: "task-backend-2", RepoRoot: "/work/backend", RepoName: "backend"},
	}
	svc.userConfig.repoDirs = []string{"/work"}
	svc.repoClient.checkouts = map[string][]string{"/work": {"/work/backend", "/work/frontend"}}

	repos, err := svc.service.ListRepos(t.Context())

	require.NoError(t, err)
	require.Equal(t, []KnownRepo{
		{Name: "backend", Root: "/work/backend", TaskCount: 2},
		{Name: "frontend", Root: "/work/frontend"},
	}, repos)
}

func TestTaskServiceListRepos_SkipsRepoDirectoriesThatDoNotExist(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.repoDirs = []string{"/moved", "/work"}
	svc.repoClient.checkouts = map[string][]string{"/work": {"/work/frontend"}}

	repos, err := svc.service.ListRepos(t.Context())

	require.NoError(t, err)
	require.Equal(t, []KnownRepo{{Name: "frontend", Root: "/work/frontend"}}, repos)
}

// A repo directory that cannot be listed hides only its own repositories;
// the doctor reports it.
func TestTaskServiceListRepos_SkipsRepoDirectoriesThatCannotBeListed(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{backendTask()}
	svc.userConfig.repoDirs = []string{"/locked", "/code"}
	svc.repoClient.checkouts = map[string][]string{"/code": {"/code/frontend"}}
	svc.repoClient.checkoutErrs = map[string]error{"/locked": errors.New("open /locked: permission denied")}

	repos, err := svc.service.ListRepos(t.Context())

	require.NoError(t, err)
	require.Equal(t, []KnownRepo{
		{Name: "backend", Root: "/work/backend", TaskCount: 1},
		{Name: "frontend", Root: "/code/frontend"},
	}, repos)
}

// The User config may be edited by hand, so its repo directories are used as
// SaveRepoDirs would save them, and the ones it would refuse are skipped.
func TestTaskServiceListRepos_UsesHandEditedRepoDirectoriesAsTheyWouldBeSaved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	code := filepath.Join(home, "code")
	svc := newTestTaskService(t)
	svc.userConfig.repoDirs = []string{"~/code", "code", "/work/", "~alice/code", " ", code}
	svc.repoClient.checkouts = map[string][]string{
		code:          {filepath.Join(code, "frontend")},
		"/work":       {"/work/backend"},
		"code":        {"/relative/notes"},
		"~alice/code": {"/alice/notes"},
	}

	repos, err := svc.service.ListRepos(t.Context())

	require.NoError(t, err)
	require.Equal(t, []KnownRepo{
		{Name: "backend", Root: "/work/backend"},
		{Name: "frontend", Root: filepath.Join(code, "frontend")},
	}, repos)
}

func TestTaskServiceListRepos_FailsWhenTheRepoDirectoriesCannotBeRead(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.getRepoDirsErr = errors.New("decode user config")

	_, err := svc.service.ListRepos(t.Context())

	require.EqualError(t, err, "read the repo directories: decode user config")
}

func TestTaskServiceListRepos_ListsNothingWithoutTasksOrRepoDirectories(t *testing.T) {
	svc := newTestTaskService(t)

	repos, err := svc.service.ListRepos(t.Context())

	require.NoError(t, err)
	require.Empty(t, repos)
}

func TestTaskServiceGetRepoDirs_ReturnsTheSavedRepoDirectories(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.repoDirs = []string{"/work", "/code"}

	dirs, err := svc.service.GetRepoDirs(t.Context())

	require.NoError(t, err)
	require.Equal(t, []string{"/work", "/code"}, dirs)
}

func TestTaskServiceSaveRepoDirs_SavesCleanAbsolutePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		dirs []string
		want []string
	}{
		{name: "absolute paths in the order given", dirs: []string{"/work", "/code"}, want: []string{"/work", "/code"}},
		{
			name: "paths that are not clean",
			dirs: []string{" /work//code/ ", "/work/a/../b"},
			want: []string{"/work/code", "/work/b"},
		},
		{name: "home directory", dirs: []string{"~/code", "~"}, want: []string{filepath.Join(home, "code"), home}},
		{name: "blank entries", dirs: []string{"", "  ", "/work"}, want: []string{"/work"}},
		{
			name: "repeats keep the first",
			dirs: []string{"/work", "/code", "/work/", "/code"},
			want: []string{"/work", "/code"},
		},
		{
			name: "a repeat of the home directory",
			dirs: []string{filepath.Join(home, "code"), "~/code"},
			want: []string{filepath.Join(home, "code")},
		},
		{name: "none", dirs: nil, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.userConfig.repoDirs = []string{"/old"}

			require.NoError(t, svc.service.SaveRepoDirs(t.Context(), tt.dirs))

			require.Equal(t, 1, svc.userConfig.repoDirsSaves)
			require.Equal(t, tt.want, svc.userConfig.repoDirs)
		})
	}
}

func TestTaskServiceSaveRepoDirs_RejectsAPathThatIsNotAbsolute(t *testing.T) {
	tests := []struct {
		name    string
		dirs    []string
		wantErr string
	}{
		{
			name:    "relative path",
			dirs:    []string{"/work", "code"},
			wantErr: `repo directory "code" must be an absolute path or start with ~/`,
		},
		{
			name:    "dot",
			dirs:    []string{"."},
			wantErr: `repo directory "." must be an absolute path or start with ~/`,
		},
		{
			name:    "another user's home directory",
			dirs:    []string{"~alice/code"},
			wantErr: `repo directory "~alice/code": only ~ and ~/ expand to the home directory`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.userConfig.repoDirs = []string{"/old"}

			require.EqualError(t, svc.service.SaveRepoDirs(t.Context(), tt.dirs), tt.wantErr)

			require.Zero(t, svc.userConfig.repoDirsSaves)
			require.Equal(t, []string{"/old"}, svc.userConfig.repoDirs)
		})
	}
}

func TestTaskServiceSaveRepoDirs_RequiresProviderSetup(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.saveRepoDirsErr = ErrProviderSetupRequired

	err := svc.service.SaveRepoDirs(t.Context(), []string{"/work"})

	require.ErrorIs(t, err, ErrProviderSetupRequired)
}

func TestTaskServiceHealthCheck_WarnsAboutRepoDirectoriesThatDoNotExist(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.repoDirs = []string{"/moved", "/work", "/gone"}
	svc.repoClient.checkouts = map[string][]string{"/work": {"/work/frontend"}}

	checks, err := svc.service.HealthCheck(t.Context())

	require.NoError(t, err)
	requireHealthCheck(t, checks, "repo dirs", false, "not found: /moved, /gone (run rig setup to change them)")
}

func TestTaskServiceHealthCheck_WarnsAboutRepoDirectoriesThatCannotBeListed(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.repoDirs = []string{"/moved", "/work"}
	svc.repoClient.checkoutErrs = map[string]error{"/work": errors.New("open /work: permission denied")}

	checks, err := svc.service.HealthCheck(t.Context())

	require.NoError(t, err)
	requireHealthCheck(t, checks, "repo dirs", false,
		"not found: /moved; /work: open /work: permission denied (run rig setup to change them)")
}

func TestTaskServiceHealthCheck_WarnsAboutHandEditedRepoDirectoriesThatCannotBeUsed(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.repoDirs = []string{"code", "/work", "~alice/code"}
	svc.repoClient.checkouts = map[string][]string{"/work": nil}

	checks, err := svc.service.HealthCheck(t.Context())

	require.NoError(t, err)
	requireHealthCheck(t, checks, "repo dirs", false, `repo directory "code" must be an absolute path or `+
		`start with ~/; repo directory "~alice/code": only ~ and ~/ expand to the home directory `+
		`(run rig setup to change them)`)
}

// Rig works without its repo directories, so failing to read them is a
// warning rather than a failure.
func TestTaskServiceHealthCheck_WarnsWhenTheRepoDirectoriesCannotBeRead(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.getRepoDirsErr = errors.New("decode user config: unexpected end of JSON input")

	checks, err := svc.service.HealthCheck(t.Context())

	require.NoError(t, err)
	requireHealthCheck(t, checks, "repo dirs", false,
		"read the repo directories: decode user config: unexpected end of JSON input")
}

func TestTaskServiceHealthCheck_PassesTheRepoDirectoriesCheck(t *testing.T) {
	tests := []struct {
		name     string
		repoDirs []string
	}{
		{name: "every repo directory exists", repoDirs: []string{"/work", "/code"}},
		{name: "no repo directories"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.userConfig.repoDirs = tt.repoDirs
			svc.repoClient.checkouts = map[string][]string{"/work": {"/work/frontend"}, "/code": nil}

			checks, err := svc.service.HealthCheck(t.Context())

			require.NoError(t, err)
			requireHealthCheck(t, checks, "repo dirs", false, "")
		})
	}
}

func TestTaskServiceSuggestRepoDir_SuggestsTheParentDirectoryOfTheRepository(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
	}{
		{name: "inside a repository", cwd: "/work/backend/internal"},
		// Repository detection maps a worktree to its main repository, and the
		// worktree sits beside it.
		{name: "inside a worktree", cwd: "/work/backend_fix-flake"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.repoClient.repoContext = RepoContext{Root: "/work/backend", Name: "backend", BaseBranch: "main"}

			suggestion, err := svc.service.SuggestRepoDir(t.Context(), tt.cwd)

			require.NoError(t, err)
			require.Equal(t, "/work", suggestion)
			require.Equal(t, []string{tt.cwd}, svc.repoClient.detectedCwds)
		})
	}
}

func TestTaskServiceSuggestRepoDir_SuggestsNothingOutsideARepository(t *testing.T) {
	svc := newTestTaskService(t)
	svc.repoClient.detectRepoErr = errors.New("fatal: not a git repository (or any of the parent directories): .git")

	suggestion, err := svc.service.SuggestRepoDir(t.Context(), "/tmp/notes")

	require.NoError(t, err)
	require.Empty(t, suggestion)
}

func TestTaskServiceSuggestRepoDir_SuggestsNothingWithoutAnAbsoluteWorkingDirectory(t *testing.T) {
	for _, cwd := range []string{"", " ", "work/backend"} {
		t.Run(cwd, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.repoClient.repoContext = RepoContext{Root: "/work/backend", Name: "backend", BaseBranch: "main"}

			suggestion, err := svc.service.SuggestRepoDir(t.Context(), cwd)

			require.NoError(t, err)
			require.Empty(t, suggestion)
			require.Empty(t, svc.repoClient.detectedCwds)
		})
	}
}

func TestTaskServiceSuggestRepoDir_FailsWhenCancelled(t *testing.T) {
	svc := newTestTaskService(t)
	svc.repoClient.detectRepoErr = context.Canceled
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := svc.service.SuggestRepoDir(ctx, "/work/backend")

	require.ErrorIs(t, err, context.Canceled)
}
