package agentconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryPackage_ExposesLoader(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte("seed:\n  copy: []\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader := NewLoader()
	if _, err := loader.LoadRepoConfig(t.Context(), repoRoot); err != nil {
		t.Fatalf("expected loader to parse config, got %v", err)
	}
}
