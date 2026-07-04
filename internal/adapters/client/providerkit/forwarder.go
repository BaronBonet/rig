package providerkit

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed forward-to-rig.sh.tmpl
var forwarderScriptTemplateText string

var forwarderScriptTemplate = template.Must(template.New("forward-to-rig.sh").Parse(forwarderScriptTemplateText))

// Forwarder describes one provider's hook forwarding: the script that ships
// hook payloads to Rig's collector, and the HTTP header the collector reads
// the event name from. EventHeader must match the header the provider's hook
// HTTP handler decodes — both sides derive it from the same constant.
type Forwarder struct {
	// ProviderLabel names the provider in script artifacts (env var prefix,
	// temp file prefix), e.g. "claude".
	ProviderLabel string
	// EventHeader carries the hook event name to the collector,
	// e.g. "X-Claude-Hook-Event".
	EventHeader string
	// CollectorURL is the loopback hook endpoint the script posts to.
	CollectorURL string
	// HookSecret authenticates the script to the collector.
	HookSecret string
}

// RenderScript renders the forward-to-rig shell script for this provider.
func (f Forwarder) RenderScript() ([]byte, error) {
	label := strings.ToLower(strings.TrimSpace(f.ProviderLabel))
	if label == "" {
		return nil, fmt.Errorf("forwarder provider label is required")
	}

	var buf bytes.Buffer
	if err := forwarderScriptTemplate.Execute(&buf, struct {
		EnvVarPrefix       string
		TmpPrefix          string
		EventHeader        string
		CollectorURLQuoted string
		HookSecretQuoted   string
	}{
		EnvVarPrefix:       strings.ToUpper(label),
		TmpPrefix:          label,
		EventHeader:        f.EventHeader,
		CollectorURLQuoted: ShellQuote(f.CollectorURL),
		HookSecretQuoted:   ShellQuote(f.HookSecret),
	}); err != nil {
		return nil, fmt.Errorf("render %s forwarder script: %w", label, err)
	}

	return buf.Bytes(), nil
}

// HealthCheckScript verifies an installed forwarder script: it must be an
// executable file whose contents still reference the collector URL the
// daemon expects.
func HealthCheckScript(scriptPath string, collectorURL string) error {
	scriptInfo, err := os.Stat(scriptPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", scriptPath, err)
	}
	if scriptInfo.IsDir() {
		return fmt.Errorf("%s must be a file", scriptPath)
	}
	if scriptInfo.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must be executable", scriptPath)
	}
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", scriptPath, err)
	}
	if !strings.Contains(string(scriptBytes), collectorURL) {
		return fmt.Errorf("%s collector URL must include %s", scriptPath, collectorURL)
	}

	return nil
}

// WriteScript installs the rendered forwarder script at scriptPath with a
// private parent directory, creating or repairing both as needed.
func (f Forwarder) WriteScript(scriptPath string) error {
	label := strings.ToLower(strings.TrimSpace(f.ProviderLabel))
	dir := filepath.Dir(scriptPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s hooks dir: %w", label, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure %s hooks dir: %w", label, err)
	}

	scriptBytes, err := f.RenderScript()
	if err != nil {
		return err
	}
	if err := os.WriteFile(scriptPath, scriptBytes, 0o700); err != nil {
		return fmt.Errorf("write %s forwarder script: %w", label, err)
	}

	return nil
}

// ShellQuote single-quotes a value for safe interpolation into shell text.
func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
