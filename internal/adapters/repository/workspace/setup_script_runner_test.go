package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePackage_ExposesSetupScriptRunner(t *testing.T) {
	repoRoot := t.TempDir()
	scriptDir := filepath.Join(repoRoot, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	if err := validateSetupScript(repoRoot, "scripts/setup.sh"); err != nil {
		t.Fatalf("expected runner to validate script, got %v", err)
	}
}
