package providerkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/prompts"
)

// HandoffPrompt is the handoff request a previous session gets, told what the
// next session will be asked to do so the note covers what it needs for that.
func HandoffPrompt(focus string) string {
	focus = strings.TrimSpace(focus)
	if focus == "" {
		return prompts.HandoffPrompt
	}
	return prompts.HandoffPrompt +
		"\n\nThe next session will start with this instruction from the user:\n" + focus +
		"\n\nMake sure the note covers everything it needs for that: " +
		"the context, files, decisions and open questions that bear on it."
}

// SaveHandoffNote writes note under dataDir, rig's data dir, as one file per
// session it was written from, so a task's earlier notes stay readable, and
// returns its path.
func SaveHandoffNote(dataDir string, task *core.Task, sessionID string, note string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", fmt.Errorf("rig data dir is required")
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	short := strings.TrimSpace(sessionID)
	if len(short) > 8 {
		short = short[:8]
	}
	path := filepath.Join(dataDir, "handoffs", strings.TrimSpace(task.ID), stamp+"-"+short+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create handoff directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(note)+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write handoff note: %w", err)
	}
	return path, nil
}

// DefaultRigDataDir is rig's data dir, ~/.local/share/rig.
func DefaultRigDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve rig data dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "rig"), nil
}
