package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func TestCanEnterMatrix(t *testing.T) {
	allModes := []modelMode{
		modeBrowse, modePromptInput, modePRPicker,
		modeCleanupConfirm, modeProviderSetup, modeSwitchProvider,
	}

	cases := []struct {
		name      string
		pending   pendingOp
		setupOnly bool
		hasSetup  bool
		allowed   map[modelMode]bool
	}{
		{
			name:     "configured and idle: everything reachable",
			hasSetup: true,
			allowed: map[modelMode]bool{
				modeBrowse: true, modePromptInput: true, modePRPicker: true,
				modeCleanupConfirm: true, modeProviderSetup: true, modeSwitchProvider: true,
			},
		},
		{
			name:     "no provider setup: only browse and setup",
			hasSetup: false,
			allowed: map[modelMode]bool{
				modeBrowse: true, modeProviderSetup: true,
			},
		},
		{
			name:      "setup-only session: locked to provider setup",
			setupOnly: true,
			hasSetup:  true,
			allowed: map[modelMode]bool{
				modeProviderSetup: true,
			},
		},
		{
			name:     "operation pending: only browse, one op at a time",
			pending:  opCreating,
			hasSetup: true,
			allowed: map[modelMode]bool{
				modeBrowse: true,
			},
		},
		{
			name:     "delete pending blocks the same set",
			pending:  opDeleting,
			hasSetup: true,
			allowed: map[modelMode]bool{
				modeBrowse: true,
			},
		},
		{
			name:     "switch pending blocks the same set",
			pending:  opSwitching,
			hasSetup: true,
			allowed: map[modelMode]bool{
				modeBrowse: true,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, to := range allModes {
				got := canEnter(to, tc.pending, tc.setupOnly, tc.hasSetup)
				require.Equalf(t, tc.allowed[to], got, "canEnter(%v)", to)
			}
		})
	}
}

func TestBackTargetMatrix(t *testing.T) {
	cases := []struct {
		name      string
		mode      modelMode
		hasSetup  bool
		setupOnly bool
		want      backDest
	}{
		{name: "browse quits", mode: modeBrowse, hasSetup: true, want: backDest{quit: true}},
		{name: "prompt input returns to browse", mode: modePromptInput, hasSetup: true, want: backDest{to: modeBrowse}},
		{
			name:     "pr picker returns to the prompt",
			mode:     modePRPicker,
			hasSetup: true,
			want:     backDest{to: modePromptInput},
		},
		{
			name:     "cleanup confirm returns to browse",
			mode:     modeCleanupConfirm,
			hasSetup: true,
			want:     backDest{to: modeBrowse},
		},
		{
			name:     "switch provider returns to browse",
			mode:     modeSwitchProvider,
			hasSetup: true,
			want:     backDest{to: modeBrowse},
		},
		{
			name:     "provider setup returns to browse once configured",
			mode:     modeProviderSetup,
			hasSetup: true,
			want:     backDest{to: modeBrowse},
		},
		{
			name:     "provider setup quits without a valid setup",
			mode:     modeProviderSetup,
			hasSetup: false,
			want:     backDest{quit: true},
		},
		{
			name:      "provider setup quits in a setup-only session",
			mode:      modeProviderSetup,
			hasSetup:  true,
			setupOnly: true,
			want:      backDest{quit: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, backTarget(tc.mode, tc.hasSetup, tc.setupOnly))
		})
	}
}

func TestModel_CycledProviderResetsWithEachNewDraft(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.providerSetup = &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderCodex,
	}
	m := newLoadedModel(frontend)

	next, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	inPrompt := next.(model)
	require.Equal(t, modePromptInput, inPrompt.mode)

	next, _ = inPrompt.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	cycled := next.(model)
	require.Equal(t, core.ProviderClaude, cycled.effectiveCreateProvider())

	next, _ = cycled.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	abandoned := next.(model)
	require.Equal(t, modeBrowse, abandoned.mode)

	next, _ = abandoned.Update(tea.KeyPressMsg{Text: "a"})
	fresh := next.(model)
	require.Equal(t, modePromptInput, fresh.mode)
	require.Equal(t, core.ProviderCodex, fresh.effectiveCreateProvider())
}

func TestModel_QReturnsFromPRPickerKeepingPrompt(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePRPicker
	m.draft.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Text: "q"})
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "fix the retry loop", got.draft.prompt)
}
