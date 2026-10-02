package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

// newRepoWithLinkedWorktree creates <tmp>/api with a commit on main and a
// linked worktree <tmp>/api-1 on feat/billing.
func newRepoWithLinkedWorktree(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}

	base := t.TempDir()
	main := filepath.Join(base, "api")
	linked := filepath.Join(base, "api-1")
	require.NoError(t, os.MkdirAll(filepath.Join(main, "app", "models"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(main, "app", "models", "user.rb"), []byte("x\n"), 0o600))
	runGit(t, main, "init", "-q", "-b", "main")
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-q", "-m", "init")
	runGit(t, main, "worktree", "add", "-q", "-b", "feat/billing", linked)
	return main, linked
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=rig", "GIT_AUTHOR_EMAIL=rig@example.com",
		"GIT_COMMITTER_NAME=rig", "GIT_COMMITTER_EMAIL=rig@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestRepositoryWorktreeRootOf_FindsMainAndLinkedWorktreeRoots(t *testing.T) {
	main, linked := newRepoWithLinkedWorktree(t)
	repo := New(subprocess.ExecRunner{})

	require.Equal(t, main, repo.WorktreeRootOf(filepath.Join(main, "app", "models")))
	require.Equal(t, linked, repo.WorktreeRootOf(filepath.Join(linked, "app", "models")))
	require.Equal(t, linked, repo.WorktreeRootOf(linked))
}

func TestRepositoryWorktreeRootOf_DoesNotClimbOutOfAMissingDirectory(t *testing.T) {
	main, _ := newRepoWithLinkedWorktree(t)
	repo := New(subprocess.ExecRunner{})

	// An edit in a since-deleted worktree nested inside another checkout must
	// not be attributed to the checkout around it.
	require.Empty(t, repo.WorktreeRootOf(filepath.Join(main, ".claude", "worktrees", "gone", "src")))
	require.Empty(t, repo.WorktreeRootOf(filepath.Dir(main)))
	require.Empty(t, repo.WorktreeRootOf("relative/dir"))
}

func TestRepositoryInspectWorktree_ReportsSharedRepoNameAndCurrentBranch(t *testing.T) {
	main, linked := newRepoWithLinkedWorktree(t)
	repo := New(subprocess.ExecRunner{})

	mainRef, err := repo.InspectWorktree(t.Context(), main)
	require.NoError(t, err)
	require.Equal(t, &core.WorktreeRef{Root: main, RepoName: "api", Branch: "main"}, mainRef)

	linkedRef, err := repo.InspectWorktree(t.Context(), linked)
	require.NoError(t, err)
	require.Equal(t, &core.WorktreeRef{Root: linked, RepoName: "api", Branch: "feat/billing"}, linkedRef)

	runGit(t, linked, "checkout", "-q", "--detach")
	detachedRef, err := repo.InspectWorktree(t.Context(), linked)
	require.NoError(t, err)
	require.Empty(t, detachedRef.Branch)
}

func TestRepositoryInspectWorktree_ReturnsNilForARemovedWorktree(t *testing.T) {
	main, linked := newRepoWithLinkedWorktree(t)
	runGit(t, main, "worktree", "remove", linked)
	repo := New(subprocess.ExecRunner{})

	ref, err := repo.InspectWorktree(t.Context(), linked)

	require.NoError(t, err)
	require.Nil(t, ref)
}

func TestRepositoryInspectWorktree_ReturnsNilForAPrunedWorktreeLeftOnDisk(t *testing.T) {
	main, linked := newRepoWithLinkedWorktree(t)
	require.NoError(t, os.RemoveAll(filepath.Join(main, ".git", "worktrees", "api-1")))
	repo := New(subprocess.ExecRunner{})

	ref, err := repo.InspectWorktree(t.Context(), linked)

	require.NoError(t, err)
	require.Nil(t, ref)
}

func TestRepoNameFromCommonDir(t *testing.T) {
	require.Equal(t, "api", repoNameFromCommonDir("/src/api/.git"))
	require.Equal(t, "api", repoNameFromCommonDir("/src/api.git"))
	require.Equal(t, "api", repoNameFromCommonDir("/src/api/.bare"))
	require.Equal(t, "api", repoNameFromCommonDir("/src/api/.git/"))
}
