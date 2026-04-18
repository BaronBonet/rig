package setupscript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryPackage_ExposesRunner(t *testing.T) {
	repoRoot := t.TempDir()
	scriptDir := filepath.Join(repoRoot, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	runner := NewRunner()
	if err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh"); err != nil {
		t.Fatalf("expected runner to validate script, got %v", err)
	}
}
