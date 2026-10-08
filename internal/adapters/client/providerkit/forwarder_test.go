package providerkit

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func TestForwarderScript_SendsTaskIDFromSessionEnvironment(t *testing.T) {
	headers := runForwarderScript(t, []string{core.TaskIDEnvVar + "=task-123"})

	require.Equal(t, "task-123", headers.Get(TaskIDHeader))
	require.Equal(t, "Stop", headers.Get("X-Test-Hook-Event"))
}

func TestForwarderScript_SendsEmptyTaskIDOutsideRigSessions(t *testing.T) {
	headers := runForwarderScript(t, nil)

	require.Empty(t, headers.Get(TaskIDHeader))
	require.Equal(t, "Stop", headers.Get("X-Test-Hook-Event"))
}

// runForwarderScript renders the real forwarder, runs it under sh the way a
// provider hook does, and returns the headers the collector received.
func runForwarderScript(t *testing.T, env []string) http.Header {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("forwarder script needs curl")
	}

	received := make(chan http.Header, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(collector.Close)

	scriptPath := filepath.Join(t.TempDir(), "forward-to-rig.sh")
	forwarder := Forwarder{
		ProviderLabel: "test",
		EventHeader:   "X-Test-Hook-Event",
		CollectorURL:  collector.URL,
		HookSecret:    "secret-token",
	}
	require.NoError(t, forwarder.WriteScript(scriptPath))

	cmd := exec.CommandContext(t.Context(), "sh", scriptPath, "Stop")
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop"}`)
	cmd.Env = append(baseEnvWithout(core.TaskIDEnvVar), env...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	select {
	case headers := <-received:
		return headers
	default:
		t.Fatal("collector received no request")
		return nil
	}
}

func baseEnvWithout(name string) []string {
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, name+"=") {
			env = append(env, entry)
		}
	}
	return env
}
