package codexhooks

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"rig/internal/core"
)

//go:embed forward-to-rig.sh.tmpl
var forwarderScriptTemplateText string

var forwarderScriptTemplate = template.Must(template.New("forward-to-rig.sh").Parse(forwarderScriptTemplateText))

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
	hooksDir := filepath.Join(hooksRoot, "hooks")
	hooksJSONPath := filepath.Join(hooksRoot, "hooks.json")
	forwarderPath := filepath.Join(hooksDir, "forward-to-rig.sh")
	if fileExists(hooksJSONPath) && fileExists(forwarderPath) {
		return nil
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	rawHooks, err := b.renderHooksJSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(hooksJSONPath, rawHooks, 0o644); err != nil {
		return err
	}

	rawScript, err := b.renderForwarderScript()
	if err != nil {
		return err
	}

	return os.WriteFile(forwarderPath, rawScript, 0o755)
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

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (b *Bootstrapper) commandForEvent(eventName string) string {
	eventName = strings.TrimSpace(eventName)

	cmd := `repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0;` +
		` exec /bin/sh "$repo_root/.codex/hooks/forward-to-rig.sh" ` + shellQuote(eventName)
	return "/bin/sh -c " + shellQuote(cmd)
}

func (b *Bootstrapper) renderForwarderScript() ([]byte, error) {
	var buf bytes.Buffer
	if err := forwarderScriptTemplate.Execute(&buf, struct {
		AgentExecQuoted  string
		SourceRootQuoted string
	}{
		AgentExecQuoted:  shellQuote(b.agentExec),
		SourceRootQuoted: shellQuote(b.sourceRoot),
	}); err != nil {
		return nil, fmt.Errorf("render codex forwarder script: %w", err)
	}

	return buf.Bytes(), nil
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
