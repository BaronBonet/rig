package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveStatus_DecisionTable pins the runtime-status decision as pure
// values: no fakes, no goroutines, no I/O. The persisted status is one input
// alongside the live tmux runtime state and the active provider's expected
// session command.
func TestResolveStatus_DecisionTable(t *testing.T) {
	working := &TaskStatusUpdate{TaskID: "task-1", Phase: TaskStatusPhaseWorking}
	stopped := &TaskStatusUpdate{TaskID: "task-1", Phase: TaskStatusPhaseStopped}

	cases := []struct {
		name            string
		update          *TaskStatusUpdate
		runtime         TaskSessionRuntimeState
		providerCommand string
		want            statusResolution
	}{
		{
			name:            "nil update stands",
			update:          nil,
			runtime:         TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}},
			providerCommand: "codex",
			want:            statusKeep,
		},
		{
			name:            "already stopped stands even with a live session",
			update:          stopped,
			runtime:         TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}},
			providerCommand: "codex",
			want:            statusKeep,
		},
		{
			name:            "tmux session gone flips working to stopped",
			update:          working,
			runtime:         TaskSessionRuntimeState{Exists: false},
			providerCommand: "codex",
			want:            statusStopped,
		},
		{
			name:            "session alive but provider process gone flips to stopped",
			update:          working,
			runtime:         TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"zsh"}},
			providerCommand: "codex",
			want:            statusStopped,
		},
		{
			name:            "provider running invites recovery of stale status",
			update:          working,
			runtime:         TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}},
			providerCommand: "codex",
			want:            statusTryRecover,
		},
		{
			name:   "provider running under a suffixed process title still counts",
			update: working,
			runtime: TaskSessionRuntimeState{
				Exists:         true,
				ActiveCommands: []string{"claude-3-x-rewritten-title"},
			},
			providerCommand: "claude",
			want:            statusTryRecover,
		},
		{
			name:            "unrelated command sharing a prefix without dash does not count",
			update:          working,
			runtime:         TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codexish"}},
			providerCommand: "codex",
			want:            statusStopped,
		},
		{
			name:            "empty provider command never matches",
			update:          working,
			runtime:         TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}},
			providerCommand: "",
			want:            statusStopped,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, resolveStatus(tc.update, tc.runtime, tc.providerCommand))
		})
	}
}
