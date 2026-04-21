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
