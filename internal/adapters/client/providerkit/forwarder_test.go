package providerkit

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

// fakeCurl records the arguments it is called with, one per line, its parent
// process ID (the forwarder's), and the payload file it was asked to send.
const fakeCurl = `#!/bin/sh
printf '%s\n' "$PPID" >"$CURL_PPID_FILE"
for arg in "$@"; do
  case $arg in
    @*) cat "${arg#@}" >"$CURL_PAYLOAD_FILE" ;;
  esac
  printf '%s\n' "$arg"
done >"$CURL_ARGS_FILE"
`

// forwarderTestPayload is deliberately not JSON: the forwarder posts the hook
// payload's bytes untouched.
const forwarderTestPayload = "hook payload\nacross two lines"

func TestForwarderScript_SendsHookIdentityHeaders(t *testing.T) {
	cases := []struct {
		name        string
		env         []string
		tmuxHeaders []string
	}{
		{
			name: "inside tmux",
			env:  []string{"TMUX_PANE=%44", "TMUX=/private/tmp/tmux-501/default,4722,3"},
			tmuxHeaders: []string{
				"X-Rig-Tmux-Pane: %44",
				"X-Rig-Tmux: /private/tmp/tmux-501/default,4722,3",
			},
		},
		{
			name: "socket path containing a space",
			env:  []string{"TMUX_PANE=%7", "TMUX=/private/tmp/tmux 501/default,4722,3"},
			tmuxHeaders: []string{
				"X-Rig-Tmux-Pane: %7",
				"X-Rig-Tmux: /private/tmp/tmux 501/default,4722,3",
			},
		},
		{name: "outside tmux"},
		{name: "empty tmux variables", env: []string{"TMUX_PANE=", "TMUX="}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := runForwarderScript(t, tc.env)

			want := []string{
				"-fsS", "--max-time", "2", "-X", "POST",
				"-H", "Content-Type: application/json",
				"-H", "X-Claude-Hook-Event: SessionStart",
				"-H", "X-Rig-Hook-Secret: secret-token",
				"-H", "X-Rig-Hook-Pid: " + run.forwarderPID,
			}
			for _, header := range tc.tmuxHeaders {
				want = append(want, "-H", header)
			}
			want = append(want, "--data-binary", run.payloadArg, "http://127.0.0.1:4318/hooks/claude")
			require.Equal(t, want, run.curlArgs)
			require.Equal(t, forwarderTestPayload, run.payload)
		})
	}
}

type forwarderRun struct {
	curlArgs     []string
	forwarderPID string
	payloadArg   string
	payload      string
}

// runForwarderScript runs the rendered forwarder with a fake curl and only
// the given environment, so the test's own tmux never leaks in.
func runForwarderScript(t *testing.T, env []string) forwarderRun {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(binDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "curl"), []byte(fakeCurl), 0o700))

	scriptPath := filepath.Join(dir, "forward-to-rig.sh")
	require.NoError(t, Forwarder{
		ProviderLabel: "claude",
		EventHeader:   "X-Claude-Hook-Event",
		CollectorURL:  "http://127.0.0.1:4318/hooks/claude",
		HookSecret:    "secret-token",
	}.WriteScript(scriptPath))

	argsPath := filepath.Join(dir, "curl-args")
	ppidPath := filepath.Join(dir, "curl-ppid")
	payloadPath := filepath.Join(dir, "curl-payload")
	cmd := exec.CommandContext(t.Context(), "/bin/sh", scriptPath, "SessionStart")
	cmd.Env = append([]string{
		"PATH=" + binDir + ":/usr/bin:/bin",
		"TMPDIR=" + dir,
		"CURL_ARGS_FILE=" + argsPath,
		"CURL_PPID_FILE=" + ppidPath,
		"CURL_PAYLOAD_FILE=" + payloadPath,
	}, env...)
	cmd.Stdin = bytes.NewBufferString(forwarderTestPayload)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	run := forwarderRun{
		curlArgs:     readLines(t, argsPath),
		forwarderPID: strings.TrimSpace(strings.Join(readLines(t, ppidPath), "")),
		payload:      string(readFile(t, payloadPath)),
	}
	require.NotEmpty(t, run.forwarderPID)
	// The payload is staged in a mktemp file whose name is random.
	for index, arg := range run.curlArgs {
		if arg == "--data-binary" && index+1 < len(run.curlArgs) {
			run.payloadArg = run.curlArgs[index+1]
		}
	}
	require.True(t, strings.HasPrefix(run.payloadArg, "@"+filepath.Join(dir, "claude-hook.")), run.payloadArg)
	return run
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(string(readFile(t, path)), "\n"), "\n")
}

func TestDecodeTmuxHeaders(t *testing.T) {
	server := core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722}
	cases := []struct {
		name       string
		pane       string
		tmux       string
		wantPane   string
		wantServer core.TmuxServer
	}{
		{
			name:       "pane and server",
			pane:       "%44",
			tmux:       "/private/tmp/tmux-501/default,4722,3",
			wantPane:   "%44",
			wantServer: server,
		},
		{
			name:       "socket path containing commas",
			pane:       "%2",
			tmux:       "/tmp/a,b/default,4722,0",
			wantPane:   "%2",
			wantServer: core.TmuxServer{SocketPath: "/tmp/a,b/default", PID: 4722},
		},
		{
			name:       "socket path containing a space",
			pane:       "%7",
			tmux:       "/private/tmp/tmux 501/default,4722,3",
			wantPane:   "%7",
			wantServer: core.TmuxServer{SocketPath: "/private/tmp/tmux 501/default", PID: 4722},
		},
		{name: "outside tmux"},
		{name: "pane without server", pane: "%44"},
		{name: "server without pane", tmux: "/private/tmp/tmux-501/default,4722,3"},
		{name: "pane missing percent", pane: "44", tmux: "/private/tmp/tmux-501/default,4722,3"},
		{name: "pane without digits", pane: "%", tmux: "/private/tmp/tmux-501/default,4722,3"},
		{name: "pane with letters", pane: "%4a", tmux: "/private/tmp/tmux-501/default,4722,3"},
		{name: "tmux without commas", pane: "%44", tmux: "garbage"},
		{name: "tmux without session ID", pane: "%44", tmux: "/private/tmp/tmux-501/default,4722"},
		{name: "tmux with empty socket path", pane: "%44", tmux: ",4722,3"},
		{name: "tmux with non-numeric PID", pane: "%44", tmux: "/private/tmp/tmux-501/default,abc,3"},
		{name: "tmux with zero PID", pane: "%44", tmux: "/private/tmp/tmux-501/default,0,3"},
		{name: "tmux with non-numeric session ID", pane: "%44", tmux: "/private/tmp/tmux-501/default,4722,x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.pane != "" {
				header.Set(TmuxPaneHeader, tc.pane)
			}
			if tc.tmux != "" {
				header.Set(TmuxHeader, tc.tmux)
			}

			pane, server := DecodeTmuxHeaders(header)

			require.Equal(t, tc.wantPane, pane)
			require.Equal(t, tc.wantServer, server)
		})
	}
}

func TestDecodeHookPID(t *testing.T) {
	cases := map[string]struct {
		value string
		want  int
	}{
		"process ID":      {value: "51234", want: 51234},
		"padded":          {value: " 51234 ", want: 51234},
		"missing":         {},
		"not a number":    {value: "abc"},
		"zero":            {value: "0"},
		"negative":        {value: "-5"},
		"trailing letter": {value: "51234x"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			if tc.value != "" {
				header.Set(HookPIDHeader, tc.value)
			}

			require.Equal(t, tc.want, DecodeHookPID(header))
		})
	}
}
