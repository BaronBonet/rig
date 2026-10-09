package providerkit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestCatalogStatusUpdate_BindingWithoutPhaseYieldsNoStatusUpdate(t *testing.T) {
	catalog := Catalog{
		{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
		{Event: "SubagentStart"},
	}

	update, err := catalog.StatusUpdate(core.ProviderCodex, core.HookEventInput{
		TaskID:     "task-123",
		OccurredAt: time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC),
		EventName:  "SubagentStart",
	})

	require.NoError(t, err)
	require.Nil(t, update)
	require.Equal(t, []string{core.HookEventStop, "SubagentStart"}, catalog.EventNames())
}

func TestCatalogStatusUpdate_BindingWithPhaseYieldsStatusUpdate(t *testing.T) {
	catalog := Catalog{
		{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
		{Event: "SubagentStart"},
	}
	observedAt := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)

	update, err := catalog.StatusUpdate(core.ProviderCodex, core.HookEventInput{
		TaskID:     "task-123",
		OccurredAt: observedAt,
		EventName:  core.HookEventStop,
	})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: core.HookEventStop,
		ObservedAt:   observedAt,
	}, update)
}

func TestCatalogHookRules_RendersABindingsTimeoutInWholeSeconds(t *testing.T) {
	catalog := Catalog{
		{Event: core.HookEventSessionEnd, Timeout: SessionEndTimeout},
		{Event: core.HookEventStop, Timeout: 1500 * time.Millisecond},
		{Event: core.HookEventUserPromptSubmit, Phase: core.TaskStatusPhaseWorking},
	}

	rendered, err := catalog.RenderHookConfig(func(eventName string) string { return "forward " + eventName })

	require.NoError(t, err)
	// Codex reads the timeout as an unsigned integer, so it must not render
	// as 3.0.
	require.Contains(t, string(rendered), "\"timeout\": 3\n")
	var config struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(rendered, &config))
	require.Equal(t, map[string]any{
		"type":    "command",
		"command": "forward SessionEnd",
		"timeout": float64(3),
	}, config.Hooks[core.HookEventSessionEnd][0].Hooks[0])
	// Providers take whole seconds, so a fraction rounds up rather than
	// cutting the hook short.
	require.Equal(t, map[string]any{
		"type":    "command",
		"command": "forward Stop",
		"timeout": float64(2),
	}, config.Hooks[core.HookEventStop][0].Hooks[0])
	// Without a timeout the provider's default applies.
	require.NotContains(t, config.Hooks[core.HookEventUserPromptSubmit][0].Hooks[0], "timeout")
}

func TestMergeRigHookRules_ReplacesRigRulesAndKeepsForeignTimeouts(t *testing.T) {
	scriptPath := "/home/user/.codex/hooks/forward-to-rig.sh"
	catalog := Catalog{
		{Event: core.HookEventSessionStart, Matcher: "startup|resume|clear"},
		{Event: core.HookEventSessionEnd, Timeout: SessionEndTimeout},
	}
	command := func(eventName string) string { return "/bin/sh " + scriptPath + " " + eventName }
	existing := map[string][]HookRule{
		// Rig's registration from before SessionStart matched /clear.
		core.HookEventSessionStart: {
			{Matcher: "startup|resume", Hooks: []HookCommand{{Type: "command", Command: command("SessionStart")}}},
		},
		core.HookEventSessionEnd: {
			{Hooks: []HookCommand{{Type: "command", Command: "/usr/local/bin/save-notes", Timeout: 1.5}}},
		},
	}

	merged := MergeRigHookRules(existing, catalog.HookRules(command), scriptPath)
	again := MergeRigHookRules(merged, catalog.HookRules(command), scriptPath)

	want := map[string][]HookRule{
		core.HookEventSessionStart: {
			{
				Matcher: "startup|resume|clear",
				Hooks:   []HookCommand{{Type: "command", Command: command("SessionStart")}},
			},
		},
		core.HookEventSessionEnd: {
			{Hooks: []HookCommand{{Type: "command", Command: "/usr/local/bin/save-notes", Timeout: 1.5}}},
			{Hooks: []HookCommand{{Type: "command", Command: command("SessionEnd"), Timeout: 3}}},
		},
	}
	require.Equal(t, want, merged)
	require.Equal(t, want, again)
}
