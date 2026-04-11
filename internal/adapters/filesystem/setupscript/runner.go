package setupscript

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"agent/internal/core"
)

type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

func (r *Runner) ValidateSetupScript(_ context.Context, repoRoot string, scriptPath string) error {
	absPath := filepath.Join(repoRoot, scriptPath)

	rel, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("setup script %q escapes repo root", scriptPath)
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("setup script %q not found", scriptPath)
		}
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("setup script %q is a symlink", scriptPath)
	}
	if info.IsDir() {
		return fmt.Errorf("setup script %q is not a file", scriptPath)
	}

	return nil
}

func (r *Runner) RunSetupScript(ctx context.Context, in core.RunSetupScriptInput, output func(string)) error {
	scriptAbsPath := filepath.Join(in.RepoRoot, in.ScriptPath)

	cmd := exec.CommandContext(ctx, "bash", scriptAbsPath)
	cmd.Dir = in.WorktreePath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("setup script: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("setup script: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if output != nil {
			output(scanner.Text())
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("setup script %q failed: %w", in.ScriptPath, err)
	}

	return nil
}
