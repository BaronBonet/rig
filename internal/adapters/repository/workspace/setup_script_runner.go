package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"rig/internal/core"
)

const outputTailLineLimit = 20

func validateSetupScript(repoRoot string, scriptPath string) error {
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

func runSetupScript(ctx context.Context, in core.RunSetupScriptInput, output func(string)) error {
	scriptAbsPath := filepath.Join(in.RepoRoot, in.ScriptPath)

	cmd := exec.CommandContext(ctx, "bash", scriptAbsPath)
	cmd.Dir = in.WorktreePath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("setup script: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("setup script: %w", err)
	}

	outputTail := newLineTail(outputTailLineLimit)
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		outputTail.Add(scanner.Text())
		if output != nil {
			output(scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("setup script %q output failed: %w", in.ScriptPath, err)
	}

	if err := cmd.Wait(); err != nil {
		if outputTail.Empty() {
			return fmt.Errorf("setup script %q failed: %w", in.ScriptPath, err)
		}
		return fmt.Errorf("setup script %q failed: %w\nlast output:\n%s", in.ScriptPath, err, outputTail.String())
	}

	return nil
}

type lineTail struct {
	lines []string
	max   int
}

func newLineTail(max int) *lineTail {
	return &lineTail{max: max}
}

func (t *lineTail) Add(line string) {
	if t.max <= 0 {
		return
	}
	if len(t.lines) == t.max {
		copy(t.lines, t.lines[1:])
		t.lines[t.max-1] = line
		return
	}
	t.lines = append(t.lines, line)
}

func (t *lineTail) Empty() bool {
	return len(t.lines) == 0
}

func (t *lineTail) String() string {
	return strings.Join(t.lines, "\n")
}
