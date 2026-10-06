package providerkit

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func TestEnvCommand_RunsTheProviderWithTheConfigurationTheEnvDecides(t *testing.T) {
	names := []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"}

	require.Equal(t,
		[]string{"env", "-u", "CODEX_HOME", "CLAUDE_CONFIG_DIR=/home/me/.claude-work", "claude", "--resume", "s1"},
		EnvCommand(core.ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-work", "CODEX_HOME": ""},
			names, []string{"claude", "--resume", "s1"}),
		"unsets come before assignments, which env(1) would otherwise run as the command",
	)
	require.Equal(t,
		[]string{"env", "-u", "CLAUDE_CONFIG_DIR", "claude"},
		EnvCommand(core.ProviderEnv{"CLAUDE_CONFIG_DIR": " "}, names[:1], []string{"claude"}),
	)
	require.Equal(t, []string{"claude"}, EnvCommand(nil, names, []string{"claude"}))
	require.Equal(t, []string{"claude"},
		EnvCommand(core.ProviderEnv{"CODEX_HOME": "/x"}, names[:1], []string{"claude"}),
		"a variable of another provider is left alone")
}

func TestEnvOverrides_KeepsOnlyTheVariablesTheEnvDecides(t *testing.T) {
	names := []string{"CLAUDE_CONFIG_DIR"}

	require.Equal(t, map[string]string{"CLAUDE_CONFIG_DIR": ""},
		EnvOverrides(core.ProviderEnv{"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "/x"}, names))
	require.Nil(t, EnvOverrides(core.ProviderEnv{"CODEX_HOME": "/x"}, names))
	require.Nil(t, EnvOverrides(nil, names))
}
