package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceGetLaunchSettings_ListsConfiguredProvidersOptionsAndLastUsed(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.providerRepo.launchOptions = ProviderLaunchOptions{Models: []string{"gpt-5"}, Efforts: []string{"high"}}
	svc.claudeRepo.launchOptions = ProviderLaunchOptions{
		Models:  []string{"opus"},
		Efforts: []string{"max"},
		Handoff: true,
	}
	svc.providerConfig.launchDefaults = map[Provider]LaunchOptions{
		ProviderClaude: {Model: "opus", Effort: "max"},
		"other":        {Model: "x"},
	}

	settings, err := svc.service.GetLaunchSettings(t.Context())

	require.NoError(t, err)
	require.Equal(t, &LaunchSettings{
		Options: map[Provider]ProviderLaunchOptions{
			ProviderCodex:  {Models: []string{"gpt-5"}, Efforts: []string{"high"}},
			ProviderClaude: {Models: []string{"opus"}, Efforts: []string{"max"}, Handoff: true},
		},
		Defaults: map[Provider]LaunchOptions{ProviderClaude: {Model: "opus", Effort: "max"}},
	}, settings)
}

func TestTaskServiceGetLaunchSettings_RequiresProviderSetup(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = nil

	_, err := svc.service.GetLaunchSettings(t.Context())

	require.ErrorIs(t, err, ErrProviderSetupRequired)
}

func TestTaskServiceCreateTask_RecordsLaunchOptionsAndRemembersThem(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
		Model:  " gpt-5 ",
		Effort: "high",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "gpt-5", task.Model)
	require.Equal(t, "high", task.Effort)
	require.Equal(t, "gpt-5", svc.taskRepo.createdTask.Model)
	require.Equal(
		t,
		LaunchOptions{Model: "gpt-5", Effort: "high"},
		svc.providerConfig.savedLaunchDefaults[ProviderCodex],
	)
}
