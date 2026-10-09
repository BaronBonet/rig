package providerkit

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/BaronBonet/rig/internal/core"
)

//go:embed forward-to-rig.sh.tmpl
var forwarderScriptTemplateText string

var forwarderScriptTemplate = template.Must(template.New("forward-to-rig.sh").Parse(forwarderScriptTemplateText))

const (
	// TmuxPaneHeader carries the hook's $TMUX_PANE: the ID of the tmux pane
	// the hook's agent runs in.
	TmuxPaneHeader = "X-Rig-Tmux-Pane"
	// TmuxHeader carries the hook's $TMUX: the socket path and PID of the
	// tmux server that owns the pane, then a session ID.
	TmuxHeader = "X-Rig-Tmux"
	// HookPIDHeader carries the forwarder's own process ID. Its ancestors
	// include the agent process that ran the hook.
	HookPIDHeader = "X-Rig-Hook-Pid"
)

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
		TmuxPaneHeader     string
		TmuxHeader         string
		HookPIDHeader      string
		CollectorURLQuoted string
		HookSecretQuoted   string
	}{
		EnvVarPrefix:       strings.ToUpper(label),
		TmpPrefix:          label,
		EventHeader:        f.EventHeader,
		TmuxPaneHeader:     TmuxPaneHeader,
		TmuxHeader:         TmuxHeader,
		HookPIDHeader:      HookPIDHeader,
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

// DecodeTmuxHeaders returns the tmux pane and server a forwarded hook ran in.
// Both are zero when the hook ran outside tmux or either header is missing or
// malformed: a pane ID means nothing without its server.
func DecodeTmuxHeaders(header http.Header) (string, core.TmuxServer) {
	pane := strings.TrimSpace(header.Get(TmuxPaneHeader))
	server, ok := parseTmuxEnv(header.Get(TmuxHeader))
	if !ok || !isPaneID(pane) {
		return "", core.TmuxServer{}
	}
	return pane, server
}

// DecodeHookPID returns the forwarding hook's process ID, or zero when the
// header is missing or not a positive integer.
func DecodeHookPID(header http.Header) int {
	pid, err := strconv.Atoi(strings.TrimSpace(header.Get(HookPIDHeader)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// parseTmuxEnv parses tmux's $TMUX value, "socket_path,server_pid,session_id".
// The socket path may itself contain commas, so the PID and session ID are
// taken from the end.
func parseTmuxEnv(value string) (core.TmuxServer, bool) {
	value = strings.TrimSpace(value)
	sessionCut := strings.LastIndex(value, ",")
	if sessionCut < 0 {
		return core.TmuxServer{}, false
	}
	if _, err := strconv.Atoi(value[sessionCut+1:]); err != nil {
		return core.TmuxServer{}, false
	}
	pidCut := strings.LastIndex(value[:sessionCut], ",")
	if pidCut <= 0 {
		return core.TmuxServer{}, false
	}
	pid, err := strconv.Atoi(value[pidCut+1 : sessionCut])
	if err != nil || pid <= 0 {
		return core.TmuxServer{}, false
	}
	return core.TmuxServer{SocketPath: value[:pidCut], PID: pid}, true
}

// isPaneID reports whether value is a tmux pane ID: % followed by digits.
func isPaneID(value string) bool {
	digits, ok := strings.CutPrefix(value, "%")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ShellQuote single-quotes a value for safe interpolation into shell text.
func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
