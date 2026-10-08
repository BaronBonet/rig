package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePackage_ExposesRepoConfigLoader(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".rig.yaml"), []byte("seed:\n  copy: []\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := loadRepoConfig(repoRoot); err != nil {
		t.Fatalf("expected loader to parse config, got %v", err)
	}
}

func writeRigConfig(t *testing.T, contents string) string {
	t.Helper()
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".rig.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return repoRoot
}

func TestLoadRepoSettings_ReadsBaseBranchAndWorktreeName(t *testing.T) {
	repoRoot := writeRigConfig(t, "base_branch: develop\nworktree_name: \"{repo}-{slug}\"\nseed:\n  copy: []\n")

	settings, err := New().LoadRepoSettings(repoRoot)

	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if settings.BaseBranch != "develop" || settings.WorktreeName != "{repo}-{slug}" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
}

func TestLoadRepoSettings_RejectsInvalidValues(t *testing.T) {
	for name, contents := range map[string]string{
		"template without slug":  "worktree_name: \"{repo}-task\"\n",
		"template with a path":   "worktree_name: \"../{repo}-{slug}\"\n",
		"branch with spaces":     "base_branch: \"my branch\"\n",
		"branch as option":       "base_branch: \"--force\"\n",
		"non-string branch":      "base_branch: 3\n",
		"unknown top-level key":  "base: develop\n",
		"template with a folder": "worktree_name: \"wt/{slug}\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New().LoadRepoSettings(writeRigConfig(t, contents)); err == nil {
				t.Fatalf("expected %q to be rejected", contents)
			}
		})
	}
}

func TestLoadRepoSettings_DefaultsWithoutConfig(t *testing.T) {
	settings, err := New().LoadRepoSettings(t.TempDir())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if settings.BaseBranch != "" || settings.WorktreeName != "" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
}
