package codexhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agent/internal/core"
)

type Bootstrapper struct {
	agentExec  string
	sourceRoot string
}

func NewBootstrapper(agentExec string, sourceRoot string) *Bootstrapper {
	return &Bootstrapper{
		agentExec:  strings.TrimSpace(agentExec),
		sourceRoot: strings.TrimSpace(sourceRoot),
	}
}

func (b *Bootstrapper) BootstrapTaskWorkspace(_ context.Context, task *core.Task) error {
	if task == nil || task.Provider != "codex" || strings.TrimSpace(task.WorktreePath) == "" {
		return nil
	}

	hooksRoot := filepath.Join(task.WorktreePath, ".codex")
	if err := os.MkdirAll(hooksRoot, 0o755); err != nil {
		return err
	}

	rawHooks, err := b.renderHooksJSON()
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(hooksRoot, "hooks.json"), rawHooks, 0o644)
}

func (b *Bootstrapper) renderHooksJSON() ([]byte, error) {
	config := hookConfig{
		Hooks: map[string][]hookRule{
			"SessionStart": {
				{
					Matcher: "startup|resume",
					Hooks:   []hookCommand{{Type: "command", Command: b.commandForEvent("SessionStart")}},
				},
			},
			"PreToolUse": {
				{Matcher: "Bash", Hooks: []hookCommand{{Type: "command", Command: b.commandForEvent("PreToolUse")}}},
			},
			"PostToolUse": {
				{Matcher: "Bash", Hooks: []hookCommand{{Type: "command", Command: b.commandForEvent("PostToolUse")}}},
			},
			"UserPromptSubmit": {
				{Hooks: []hookCommand{{Type: "command", Command: b.commandForEvent("UserPromptSubmit")}}},
			},
			"Stop": {
				{Hooks: []hookCommand{{Type: "command", Command: b.commandForEvent("Stop")}}},
			},
		},
	}

	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(raw, '\n'), nil
}

func (b *Bootstrapper) commandForEvent(eventName string) string {
	eventName = strings.TrimSpace(eventName)

	commands := []string{
		fmt.Sprintf(
			"if command -v agent >/dev/null 2>&1; then exec agent observer forward-hook %s; fi",
			shellQuote(eventName),
		),
	}

	if b.agentExec != "" {
		commands = append(commands, fmt.Sprintf("if [ -x %s ]; then exec %s observer forward-hook %s; fi",
			shellQuote(b.agentExec),
			shellQuote(b.agentExec),
			shellQuote(eventName),
		))
	}

	if b.sourceRoot != "" {
		commands = append(
			commands,
			fmt.Sprintf(
				"if command -v go >/dev/null 2>&1 && [ -f %s ]; then cd %s && exec go run ./cmd/agent observer forward-hook %s; fi",
				shellQuote(filepath.Join(b.sourceRoot, "go.mod")),
				shellQuote(b.sourceRoot),
				shellQuote(eventName),
			),
		)
	}

	commands = append(commands, "exit 0")

	return "/bin/sh -c " + shellQuote(strings.Join(commands, "; "))
}

type hookConfig struct {
	Hooks map[string][]hookRule `json:"hooks"`
}

type hookRule struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
